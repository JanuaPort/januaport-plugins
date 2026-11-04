package client

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"github.com/robfig/cron/v3"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"
	"unicode/utf8"
)

func parseSchedule(s Schedule) (cron.Schedule, error) {
	if len(strings.Fields(s.Cron)) != 5 || s.TZ == "" || s.TZ == "Local" {
		return nil, ErrManifest
	}
	loc, err := time.LoadLocation(s.TZ)
	if err != nil {
		return nil, ErrManifest
	}
	v, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(s.Cron)
	if err != nil {
		return nil, ErrManifest
	}
	v.(*cron.SpecSchedule).Location = loc
	return v, nil
}
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	if strict(b, &m) != nil || (m.Contract < 1 || m.Contract > 5) || m.DeviceID == "" || m.Seq == 0 || !utc(m.IssuedAt) || m.GraceHours < 1 || m.GraceHours > 72 || m.Pins == nil {
		return m, ErrManifest
	}
	names := map[string]bool{}
	for _, p := range m.Pins {
		if !safeName(p.Pin) || names[p.Pin] || !validID(p.Algo, p.Commit) || (m.Contract != 2 && (!safePath(p.Path) || p.Container != nil)) || (m.Contract == 2 && !validContainerPin(p)) || p.DeclaredTools == nil || p.Schedules == nil {
			return m, ErrManifest
		}
		if (p.GraceHours != nil && (m.Contract < 4 || *p.GraceHours < 1 || *p.GraceHours > 72)) || (p.Setups != nil && m.Contract < 3) {
			return m, ErrManifest
		}
		setups := map[string]bool{}
		for _, setup := range p.Setups {
			if setups[setup.Name] || !validSetup(setup) {
				return m, ErrManifest
			}
			setups[setup.Name] = true
		}
		names[p.Pin] = true
		tools := map[string]bool{}
		for _, s := range p.DeclaredTools {
			if s == "" || tools[s] {
				return m, ErrManifest
			}
			tools[s] = true
		}
		sn := map[string]bool{}
		for _, s := range p.Schedules {
			if !safeName(s.Name) || sn[s.Name] || !inputOK(s.Input) {
				return m, ErrManifest
			}
			sn[s.Name] = true
			if _, err := parseSchedule(s); err != nil {
				return m, err
			}
		}
	}
	if (m.Apps != nil) != (m.Contract == appsContract) || (m.Apps != nil && !validApps(*m.Apps)) {
		return m, ErrManifest
	}
	return m, nil
}
func VerifyManifest(e SignedManifest, pub []byte, device string, minSeq uint64) (Manifest, error) {
	m, err := ParseManifest(e.Manifest)
	if err != nil {
		return m, ErrManifest
	}
	b, err := canonical(e.Manifest)
	if err != nil {
		return m, ErrManifest
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(e.Sig)
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, b, sig) || m.DeviceID != device || m.Seq <= minSeq {
		return m, ErrManifest
	}
	return m, nil
}
func OfflineAllowed(p Pin, grace int, last, now time.Time) bool {
	if p.GraceHours != nil {
		grace = *p.GraceHours
	}
	return len(p.DeclaredTools) == 0 && grace >= 1 && grace <= 72 && !last.IsZero() && !now.Before(last) && now.Sub(last) < time.Duration(grace)*time.Hour
}

// RetainUntil mirrors the core's historical manifest retention: longest effective
// pin grace plus 24 hours for late reports. The runner never expires its journal.
func (m Manifest) RetainUntil() (time.Time, error) {
	issued, err := time.Parse(time.RFC3339, m.IssuedAt)
	hours := m.GraceHours
	if len(m.Pins) > 0 {
		hours = 0
		for _, p := range m.Pins {
			grace := m.GraceHours
			if m.Contract >= 4 && p.GraceHours != nil {
				grace = *p.GraceHours
			}
			hours = max(hours, grace)
		}
	}
	return issued.Add(time.Duration(max(1, min(hours, 72)))*time.Hour + 24*time.Hour), err
}

func shortSetupText(s string, limit int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > limit {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validSetup(s Setup) bool {
	if !safeName(s.Name) || s.Label == "" || !shortSetupText(s.Label, 40) || !inputOK(s.Input) || s.Frei == nil || len(s.Frei) > 8 {
		return false
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(s.Input, &input) != nil {
		return false
	}
	seen := map[string]bool{}
	for _, f := range s.Frei {
		if !safeName(f.Name) || seen[f.Name] || input[f.Name] != nil || (f.Typ != "text" && f.MaxLaenge != 0) || (f.Typ != "auswahl" && len(f.Optionen) != 0) {
			return false
		}
		seen[f.Name] = true
		switch f.Typ {
		case "text":
			if f.MaxLaenge < 0 || f.MaxLaenge > 200 {
				return false
			}
		case "zahl", "datum":
		case "auswahl":
			if len(f.Optionen) == 0 || len(f.Optionen) > 20 {
				return false
			}
			options := map[string]bool{}
			for _, option := range f.Optionen {
				if option == "" || !shortSetupText(option, 40) || options[option] {
					return false
				}
				options[option] = true
			}
		default:
			return false
		}
	}
	return true
}
func utc(s string) bool {
	_, e := time.Parse(time.RFC3339, s)
	return e == nil && strings.HasSuffix(s, "Z")
}
func inputOK(b json.RawMessage) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 1 && len(b) <= 256<<10 && b[0] == '{' && json.Valid(b)
}
func RunKey(p Pin, s Schedule, slot time.Time) string {
	return p.Pin + "/" + s.Name + "/" + slot.UTC().Format("2006-01-02T15:04:00Z")
}

// validApps prüft Vertrag 5 streng: gültiger Name, SHA-256-Commit, fester Pfad
// apps/<name>, nach Name streng aufsteigend (damit auch eindeutig).
func validApps(apps []App) bool {
	for i, a := range apps {
		if !fenster.ValidAppName(a.Name) || !validID("sha256", a.Commit) || a.Path != "apps/"+a.Name || (i > 0 && apps[i-1].Name >= a.Name) {
			return false
		}
	}
	return true
}
