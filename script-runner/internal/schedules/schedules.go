// Package schedules liest schedules.json, die der Materialisierer des
// Gateways neben pins.json in den Plugin-Ordner schreibt (#1039, Kernvertrag
// docs/skript-zeitplaene.md, Golden script-runner-schedules.golden.json).
// Die Datei trägt weder Token-ID noch Eingabe noch Secret; der Leser kennt
// genau die sechs Felder je Zeitplan. Anders als bei pins.json macht eine
// ungültige Zeile die ganze Datei ungültig: dann gibt es keine Zeitpläne.
package schedules

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // IANA-Zeitzonen ohne Host-Dateien: das Image bringt keine mit.

	"github.com/robfig/cron/v3"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

// Max ist die Grenze v1; weitere Zeitpläne werden ignoriert und gezählt.
const Max = contract.MaxSchedules

// MaxFileBytes ist die Lesegrenze für schedules.json (SEC R4 H1). 20 Zeitpläne
// brauchen weniger als 5 KiB; eine größere Datei gilt als ungültig.
const MaxFileBytes = 64 << 10

// ErrInvalid sagt nur „ungültig“ — nie, was in der Datei stand.
var ErrInvalid = errors.New("schedules.json ungültig")

var nameRe = regexp.MustCompile(`^[a-z0-9_]{1,48}$`)

// File ist der gelesene Inhalt: höchstens Max Einträge in Dateireihenfolge,
// Ignored zählt die darüber hinaus.
type File struct {
	Entries []Entry
	Ignored int
}

// Entry ist ein geprüfter Zeitplan mit übersetztem Cron-Ausdruck.
type Entry struct {
	Name     string
	Pin      string
	Commit   string
	Cron     string
	Timezone string
	Enabled  bool
	// External is set by the mediator for Desktop-Pins, never read from JSON.
	// The device owns their clock; the local ticker reports them only.
	External bool
	spec     *cron.SpecSchedule
}

type rawFile struct {
	Version   *int       `json:"version"`
	Schedules *[]rawLine `json:"schedules"`
}

type rawLine struct {
	Name     string `json:"name"`
	Pin      string `json:"pin"`
	Commit   string `json:"commit"`
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
	Enabled  *bool  `json:"enabled"`
}

// Parse liest schedules.json streng: unbekannte Felder, fehlende Felder,
// Daten nach dem Objekt oder eine ungültige Zeile ergeben ErrInvalid.
func Parse(data []byte) (File, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raw rawFile
	if err := dec.Decode(&raw); err != nil {
		return File{}, ErrInvalid
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return File{}, ErrInvalid
	}
	if raw.Version == nil || *raw.Version != 1 || raw.Schedules == nil {
		return File{}, ErrInvalid
	}
	entries := make([]Entry, 0, len(*raw.Schedules))
	seen := map[string]bool{}
	for _, line := range *raw.Schedules {
		e, ok := entry(line)
		if !ok || seen[e.Name] {
			return File{}, ErrInvalid
		}
		seen[e.Name] = true
		entries = append(entries, e)
	}
	ignored := max(len(entries)-Max, 0)
	return File{Entries: entries[:len(entries)-ignored], Ignored: ignored}, nil
}

func entry(l rawLine) (Entry, bool) {
	if !nameRe.MatchString(l.Name) || !gitref.ValidPinName(l.Pin) || !gitref.ValidCommit(l.Commit) || l.Enabled == nil {
		return Entry{}, false
	}
	spec, ok := parseSpec(l.Cron, l.Timezone)
	if !ok {
		return Entry{}, false
	}
	return Entry{Name: l.Name, Pin: l.Pin, Commit: l.Commit, Cron: l.Cron, Timezone: l.Timezone, Enabled: *l.Enabled, spec: spec}, true
}

// parseSpec folgt den Regeln des Kerns (internal/scriptschedule, parse):
// fünf Felder, Pflicht-Zeitzone ohne Host-`Local`, Mindestabstand fünf
// Minuten, mindestens ein Termin. Derselbe Parser wie im Kern, damit beide
// dieselben Termine sehen.
func parseSpec(expr, zone string) (*cron.SpecSchedule, bool) {
	if zone == "" || zone == "Local" || len(strings.Fields(expr)) != 5 {
		return nil, false
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, false
	}
	parsed, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(expr)
	if err != nil {
		return nil, false
	}
	s, ok := parsed.(*cron.SpecSchedule)
	if !ok {
		return nil, false
	}
	s.Location = loc
	if !spacedFiveMinutes(s) || s.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)).IsZero() {
		return nil, false
	}
	return s, true
}

// spacedFiveMinutes: Die kürzesten Abstände liegen innerhalb einer Stunde
// oder zwischen zwei benachbarten gewählten Stunden; Kalenderlücken
// verlängern sie nur.
func spacedFiveMinutes(s *cron.SpecSchedule) bool {
	prev, first, last := -1, -1, -1
	for minute := 0; minute < 60; minute++ {
		if s.Minute&(uint64(1)<<minute) == 0 {
			continue
		}
		if prev >= 0 && minute-prev < 5 {
			return false
		}
		if first < 0 {
			first = minute
		}
		prev, last = minute, minute
	}
	if first < 0 {
		return false
	}
	if 60-last+first >= 5 {
		return true
	}
	for hour := 0; hour < 24; hour++ {
		if s.Hour&(uint64(1)<<hour) != 0 && s.Hour&(uint64(1)<<((hour+1)%24)) != 0 {
			return false
		}
	}
	return true
}

// Next ist der erste Termin nach t (UTC), nach der Semantik des Kerns: Ein
// Termin in der Sommerzeit-Lücke existiert nicht.
func (e Entry) Next(t time.Time) time.Time { return e.spec.Next(t).UTC() }

// GapMisses zählt die Termine, die in (from, to] in eine Sommerzeit-Lücke
// fallen: Wanduhrzeiten, die der Ausdruck trifft, die es an diesem Tag
// aber nicht gibt. Next überspringt sie; gezählt werden sie hier.
func (e Entry) GapMisses(from, to time.Time) int {
	n := 0
	for cur := from; ; {
		_, end := cur.In(e.spec.Location).ZoneBounds()
		if end.IsZero() || end.After(to) {
			return n
		}
		_, before := cur.In(e.spec.Location).Zone()
		_, after := end.In(e.spec.Location).Zone()
		if after > before {
			// Die fehlenden Wanduhrzeiten als UTC-Felder: [end+before, end+after).
			stop := end.Add(time.Duration(after) * time.Second).UTC()
			for w := end.Add(time.Duration(before) * time.Second).UTC(); w.Before(stop); w = w.Add(time.Minute) {
				if e.matchesWall(w) {
					n++
				}
			}
		}
		cur = end
	}
}

// starBit setzt robfig/cron, wenn ein Feld `*` war (spec.go, dayMatches).
const starBit = uint64(1) << 63

// matchesWall prüft die Felder einer Wanduhrzeit (in UTC-Feldern getragen)
// wie robfig/cron: Tag-im-Monat und Wochentag mit ODER, außer eines ist `*`.
func (e Entry) matchesWall(w time.Time) bool {
	s := e.spec
	if s.Minute&(uint64(1)<<w.Minute()) == 0 || s.Hour&(uint64(1)<<w.Hour()) == 0 || s.Month&(uint64(1)<<int(w.Month())) == 0 {
		return false
	}
	dom := s.Dom&(uint64(1)<<w.Day()) != 0
	dow := s.Dow&(uint64(1)<<int(w.Weekday())) != 0
	if s.Dom&starBit != 0 || s.Dow&starBit != 0 {
		return dom && dow
	}
	return dom || dow
}
