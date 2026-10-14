// Package ticker ist der Taktgeber des Skript-Läufers (#1039 Paket S1).
// Reine Logik ohne Netz und ohne Sandbox: Er entscheidet bei jedem Abgleich,
// welcher Termin jetzt auszulösen ist, und hält den Zustand für
// `_meta["januaport.ai/runner"].schedules`. Auslösen (HTTP an das Gateway)
// macht der vermittler; den Lauf startet das Gateway über Weg A. Der
// Taktgeber kennt keinen Slot (V1).
//
// Kein Nachholen: Ein Termin wird nur im Fenster kurz nach seinem Beginn
// ausgelöst. Was älter ist (Lücke, angehaltener Prozess) oder in eine
// Sommerzeit-Lücke fällt, zählt in `missed`. Termine vor dem ersten
// Abgleich eines Zeitplans (Neustart) kennt der Taktgeber nicht.
package ticker

import (
	"log/slog"
	"sync"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/schedules"
)

// Feste Log-Codes (M6): nur Codes und Zählwerte, nie Name, Schlüssel oder Text.
const (
	CodeFired   = "schedule_fired"
	CodeRefused = "schedule_refused"
	CodeFailed  = "schedule_failed"
	CodeMissed  = "schedule_missed"
)

// fireWindow liegt unter der Slot-Toleranz des Kerns (30 s): Ein Aufruf, den
// der Kern ohnehin als nicht fällig abwiese, geht gar nicht erst hinaus.
const fireWindow = 25 * time.Second

// maxScan begrenzt das Zählen verpasster Termine nach einem großen Uhrsprung.
const maxScan = 10000

// Kind ist die Art der Antwort auf einen Auslöse-Aufruf.
type Kind int

const (
	// Ran: 200 mit Ende-Klasse.
	Ran Kind = iota + 1
	// Refused: 409 — falscher Schlüssel, unbekannt oder nicht fällig.
	Refused
	// Failed: Netzfehler, Frist, kein Schlüssel oder ein anderer Status.
	Failed
)

// Outcome ist die Antwort des Gateways. End zählt nur bei Ran; Status ist
// der HTTP-Status bei Failed (0 ohne Antwort).
type Outcome struct {
	Kind   Kind
	End    string
	Status int
}

// Due ist ein jetzt auszulösender Termin.
type Due struct {
	Name string
	Slot time.Time
}

// Ticker hält den Zustand je Zeitplan. Sicher für parallele Aufrufe.
type Ticker struct {
	log *slog.Logger

	mu      sync.Mutex
	items   map[string]*item
	order   []string
	refused uint64
	failed  uint64
}

type item struct {
	entry     schedules.Entry
	eligible  bool      // enabled und Pin bereit mit passendem Commit
	cursor    time.Time // bis hier sind Termine bewertet
	lastFired time.Time // Merker: je Termin höchstens ein Versuch
	next      time.Time
	pause     string // Grund aus einer Antwort des Kerns
	pauseOff  bool   // der Kern hat den pausierten Zeitplan schon deaktiviert
	lastRun   string
	lastEnd   string
	missed    uint64
	busy      uint64
}

// New legt einen leeren Taktgeber an.
func New(log *slog.Logger) *Ticker {
	return &Ticker{log: log, items: map[string]*item{}}
}

// Tick gleicht mit den Einträgen aus schedules.json ab und liefert die
// jetzt fälligen Termine. ready sagt, ob der Pin bereit ist und den Commit
// des Zeitplans trägt. Zeitpläne, die fehlen, werden vergessen.
func (t *Ticker) Tick(now time.Time, entries []schedules.Entry, ready func(pin, commit string) bool) []Due {
	t.mu.Lock()
	defer t.mu.Unlock()
	items := make(map[string]*item, len(entries))
	order := make([]string, 0, len(entries))
	var due []Due
	for _, e := range entries {
		it := t.items[e.Name]
		if it == nil {
			it = &item{cursor: now}
		}
		it.entry = e
		it.eligible = e.Enabled && ready(e.Pin, e.Commit)
		it.syncPause()
		slot, fire, late, gap := it.advance(now)
		if fire {
			due = append(due, Due{Name: e.Name, Slot: slot})
		}
		if late+gap > 0 {
			t.log.Info(CodeMissed, "late", late, "dst_gap", gap)
		}
		items[e.Name] = it
		order = append(order, e.Name)
	}
	t.items, t.order = items, order
	return due
}

// advance bewertet die Termine in (cursor, now]. Ausgelöst wird höchstens
// der jüngste, und nur im Fenster; ältere zählen als verpasst. Läuft die Uhr
// rückwärts, bleibt der Cursor stehen.
func (it *item) advance(now time.Time) (slot time.Time, fire bool, late, gap int) {
	if now.Before(it.cursor) {
		return time.Time{}, false, 0, 0
	}
	occ := it.entry.Next(it.cursor)
	for i := 0; !occ.After(now) && i < maxScan; i++ {
		next := it.entry.Next(occ)
		if it.eligible && occ.After(it.lastFired) {
			if now.Sub(occ) < fireWindow && next.After(now) {
				slot, fire = occ, true
			} else {
				late++
			}
		}
		occ = next
	}
	if it.eligible {
		gap = it.entry.GapMisses(it.cursor, now)
	}
	if !occ.After(now) {
		occ = it.entry.Next(now)
	}
	it.cursor, it.next = now, occ
	it.missed += uint64(late + gap)
	if fire {
		it.lastFired = slot
	}
	return slot, fire, late, gap
}

// syncPause hebt eine gespiegelte Pause erst auf, wenn der Kern den
// Zeitplan deaktiviert und danach wieder aktiviert hat (Bestätigung).
func (it *item) syncPause() {
	switch {
	case it.pause == "":
	case !it.entry.Enabled:
		it.pauseOff = true
	case it.pauseOff:
		it.pause, it.pauseOff = "", false
	}
}

// Record übernimmt die Antwort auf einen Auslöse-Aufruf. Wiederholt wird
// nie; 409 und Fehler werden nur gezählt und geloggt.
func (t *Ticker) Record(name string, slot time.Time, o Outcome) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch o.Kind {
	case Refused:
		t.refused++
		t.log.Warn(CodeRefused, "refused", t.refused)
		return
	case Ran:
	default:
		t.failed++
		t.log.Warn(CodeFailed, "status", o.Status, "failed", t.failed)
		return
	}
	end := fixedEnd(o.End)
	t.log.Info(CodeFired, "end", end)
	it := t.items[name]
	if it == nil {
		return
	}
	it.lastRun, it.lastEnd = slot.UTC().Format(time.RFC3339), end
	if end == contract.EndBusy {
		it.busy++
	}
	it.pause, it.pauseOff = pauseReason(end), false
}

// Report ist der Zustand in Dateireihenfolge, höchstens so viele Einträge
// wie schedules.json (≤ contract.MaxSchedules).
func (t *Ticker) Report() []contract.ScheduleStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]contract.ScheduleStatus, 0, len(t.order))
	for _, name := range t.order {
		out = append(out, t.items[name].status())
	}
	return out
}

func (it *item) status() contract.ScheduleStatus {
	s := contract.ScheduleStatus{Name: it.entry.Name, LastRun: it.lastRun, LastEnd: it.lastEnd, Missed: it.missed, SkippedBusy: it.busy}
	switch {
	case it.pause != "":
		s.State, s.Reason = contract.SchedulePaused, it.pause
	case !it.entry.Enabled:
		s.State = contract.ScheduleDisabled
	case !it.eligible:
		s.State = contract.SchedulePaused
	default:
		s.State, s.NextRun = contract.ScheduleReady, it.next.UTC().Format(time.RFC3339)
	}
	return s
}

// knownEnds ist die feste Menge, die der Kern als Ende eines geplanten
// Laufs meldet (Vertrag §4 plus die Pausen des Kerns). Alles andere wird
// „unknown“ — nie Text aus der Antwort.
var knownEnds = map[string]bool{
	"ok": true, "error/exit": true, "error/exception": true,
	"limit/output": true, "limit/frame": true, "limit/tool_calls": true, "limit/memory": true, "limit/sandbox_lost": true,
	"timeout": true, "busy": true, "internal": true,
	"refused/pin": true, "refused/commit": true, "refused/state": true, "refused/input": true,
	"paused/token": true, "paused/pin": true,
}

func fixedEnd(end string) string {
	if knownEnds[end] {
		return end
	}
	return "unknown"
}

// pauseReason: Der Kern pausiert selbst; refused/pin|commit|state pausiert
// er mit `pin` (Fire in internal/scriptschedule).
func pauseReason(end string) string {
	switch end {
	case "paused/token":
		return contract.ScheduleReasonToken
	case "paused/pin", "refused/pin", "refused/commit", "refused/state":
		return contract.ScheduleReasonPin
	}
	return ""
}
