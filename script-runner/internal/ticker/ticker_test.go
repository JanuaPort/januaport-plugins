package ticker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/schedules"
)

var commit = strings.Repeat("3", 40)

func entries(t *testing.T, rows ...string) []schedules.Entry {
	t.Helper()
	f, err := schedules.Parse([]byte(`{"version":1,"schedules":[` + strings.Join(rows, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	return f.Entries
}

func row(name, cron, zone string, enabled bool) string {
	return fmt.Sprintf(`{"name":%q,"pin":"invoice_check","commit":%q,"cron":%q,"timezone":%q,"enabled":%t}`, name, commit, cron, zone, enabled)
}

func readyAll(string, string) bool { return true }

func at(h, m, s int) time.Time { return time.Date(2026, 10, 12, h, m, s, 0, time.UTC) }

func newTicker() *Ticker { return New(slog.New(slog.NewTextHandler(io.Discard, nil))) }

func status(t *testing.T, tk *Ticker, name string) contract.ScheduleStatus {
	t.Helper()
	for _, s := range tk.Report() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("Zeitplan %s fehlt im Bericht", name)
	return contract.ScheduleStatus{}
}

// Normalfall: Der Termin wird im ersten Abgleich nach seinem Beginn fällig.
func TestTickFiresDueTermin(t *testing.T) {
	tk := newTicker()
	es := entries(t, row("daily", "0 8 * * *", "UTC", true))
	if due := tk.Tick(at(7, 59, 58), es, readyAll); len(due) != 0 {
		t.Fatalf("vor dem Termin fällig: %v", due)
	}
	due := tk.Tick(at(8, 0, 1), es, readyAll)
	if len(due) != 1 || due[0].Name != "daily" || !due[0].Slot.Equal(at(8, 0, 0)) {
		t.Fatalf("Due = %v", due)
	}
	s := status(t, tk, "daily")
	if s.State != contract.ScheduleReady || s.NextRun != "2026-10-13T08:00:00Z" || s.Missed != 0 {
		t.Fatalf("Status = %+v", s)
	}
}

// Invariante 2: Nach einer Lücke wird nichts nachgeholt; missed zählt.
func TestNoCatchUpAfterGap(t *testing.T) {
	tk := newTicker()
	es := entries(t, row("daily", "0 8 * * *", "UTC", true), row("often", "*/5 * * * *", "UTC", true))
	tk.Tick(at(7, 59, 30), es, readyAll)
	// Die Uhr springt (Prozess angehalten, Host im Ruhezustand): 08:00, 08:05
	// und 08:10 sind vorbei, 08:10 ist älter als das Fenster.
	if due := tk.Tick(at(8, 12, 0), es, readyAll); len(due) != 0 {
		t.Fatalf("nachgeholt: %v", due)
	}
	if s := status(t, tk, "daily"); s.Missed != 1 || s.LastRun != "" {
		t.Fatalf("daily = %+v", s)
	}
	if s := status(t, tk, "often"); s.Missed != 3 || s.NextRun != "2026-10-12T08:15:00Z" {
		t.Fatalf("often = %+v", s)
	}
	// Der nächste reguläre Termin läuft wieder.
	due := tk.Tick(at(8, 15, 1), es, readyAll)
	if len(due) != 1 || due[0].Name != "often" || !due[0].Slot.Equal(at(8, 15, 0)) {
		t.Fatalf("Due = %v", due)
	}
	if s := status(t, tk, "often"); s.Missed != 3 {
		t.Fatalf("often nach dem Termin = %+v", s)
	}
}

// Kein Nachholen nach einem Neustart: Termine vor dem ersten Abgleich zählen
// nicht und laufen nicht, auch nicht im Toleranzfenster.
func TestNoCatchUpAfterStart(t *testing.T) {
	tk := newTicker()
	es := entries(t, row("daily", "0 8 * * *", "UTC", true))
	if due := tk.Tick(at(8, 0, 5), es, readyAll); len(due) != 0 {
		t.Fatalf("beim ersten Abgleich ausgelöst: %v", due)
	}
	if due := tk.Tick(at(8, 0, 7), es, readyAll); len(due) != 0 {
		t.Fatalf("Termin vor dem Start nachgeholt: %v", due)
	}
}

// Invariante 3: je Termin höchstens ein Auslöseversuch, auch bei mehrfachem
// Abgleich im selben Slot und nach einer Änderung des Ausdrucks.
func TestAtMostOneAttemptPerTermin(t *testing.T) {
	tk := newTicker()
	es := entries(t, row("daily", "0 8 * * *", "UTC", true))
	tk.Tick(at(7, 59, 59), es, readyAll)
	fired := 0
	for _, now := range []time.Time{at(8, 0, 1), at(8, 0, 1), at(8, 0, 3), at(8, 0, 10), at(8, 0, 29)} {
		fired += len(tk.Tick(now, es, readyAll))
	}
	// Antwort 409 oder Netzfehler: keine Wiederholung im selben Slot.
	tk.Record("daily", at(8, 0, 0), Outcome{Kind: Failed})
	fired += len(tk.Tick(at(8, 0, 12), es, readyAll))
	// Ausdruck geändert, derselbe Slot ist wieder Kandidat: der Merker hält.
	changed := entries(t, row("daily", "0 8 * * 1-5", "UTC", true))
	fired += len(tk.Tick(at(8, 0, 14), changed, readyAll))
	fired += len(tk.Tick(at(8, 0, 16), changed, readyAll))
	if fired != 1 {
		t.Fatalf("%d Auslöseversuche für einen Termin", fired)
	}
}

// Eine rückwärts laufende Uhr löst einen schon versuchten Termin nicht erneut aus.
func TestClockBackwardsDoesNotRefire(t *testing.T) {
	tk := newTicker()
	es := entries(t, row("daily", "0 8 * * *", "UTC", true))
	tk.Tick(at(7, 59, 59), es, readyAll)
	fired := len(tk.Tick(at(8, 0, 1), es, readyAll))
	fired += len(tk.Tick(at(7, 59, 0), es, readyAll))
	fired += len(tk.Tick(at(8, 0, 2), es, readyAll))
	if fired != 1 {
		t.Fatalf("%d Auslöseversuche", fired)
	}
}

// Invariante 6: enabled:false, Pin nicht bereit oder anderer Commit → keine
// Auslösung, Zustand passend, kein nächster Lauf.
func TestNotEnabledOrPinNotReadyNeverFires(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		ready   func(pin, c string) bool
		state   string
	}{
		{"deaktiviert", false, readyAll, contract.ScheduleDisabled},
		{"Pin nicht bereit", true, func(string, string) bool { return false }, contract.SchedulePaused},
		{"anderer Commit", true, func(pin, c string) bool { return pin == "invoice_check" && c == strings.Repeat("4", 40) }, contract.SchedulePaused},
		{"anderer Pin", true, func(pin, _ string) bool { return pin == "other" }, contract.SchedulePaused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tk := newTicker()
			es := entries(t, row("daily", "0 8 * * *", "UTC", tt.enabled))
			fired := 0
			for _, now := range []time.Time{at(7, 59, 59), at(8, 0, 1), at(8, 0, 3)} {
				fired += len(tk.Tick(now, es, tt.ready))
			}
			if fired != 0 {
				t.Fatalf("%d Auslösungen", fired)
			}
			s := status(t, tk, "daily")
			if s.State != tt.state || s.Reason != "" || s.NextRun != "" || s.Missed != 0 {
				t.Fatalf("Status = %+v, want state %s", s, tt.state)
			}
		})
	}
}

// Invariante 7: Sommerzeit-Lücke (Europe/Berlin, 29.03.2026, 02:30) → kein
// Lauf, gezählt. Die Uhr wird simuliert, kein sleep.
func TestDSTGapNoRunCounted(t *testing.T) {
	tk := newTicker()
	es := entries(t, row("nachts", "30 2 * * *", "Europe/Berlin", true))
	fired := 0
	for now := time.Date(2026, 3, 29, 0, 50, 0, 0, time.UTC); now.Before(time.Date(2026, 3, 29, 1, 40, 0, 0, time.UTC)); now = now.Add(2 * time.Second) {
		fired += len(tk.Tick(now, es, readyAll))
	}
	if fired != 0 {
		t.Fatalf("%d Läufe in der Lücke", fired)
	}
	s := status(t, tk, "nachts")
	if s.Missed != 1 || s.NextRun != "2026-03-30T00:30:00Z" || s.LastRun != "" {
		t.Fatalf("Status = %+v", s)
	}
	// Am Folgetag läuft der Termin regulär (02:30 CEST = 00:30 UTC).
	tk.Tick(time.Date(2026, 3, 30, 0, 29, 59, 0, time.UTC), es, readyAll)
	if due := tk.Tick(time.Date(2026, 3, 30, 0, 30, 1, 0, time.UTC), es, readyAll); len(due) != 1 {
		t.Fatalf("Folgetag: Due = %v", due)
	}
}

// Eine Lücke zählt nur, wenn der Zeitplan auslösen dürfte.
func TestDSTGapNotCountedWhenDisabled(t *testing.T) {
	tk := newTicker()
	es := entries(t, row("nachts", "30 2 * * *", "Europe/Berlin", false))
	tk.Tick(time.Date(2026, 3, 29, 0, 50, 0, 0, time.UTC), es, readyAll)
	tk.Tick(time.Date(2026, 3, 29, 1, 10, 0, 0, time.UTC), es, readyAll)
	if s := status(t, tk, "nachts"); s.Missed != 0 {
		t.Fatalf("Status = %+v", s)
	}
}

// Antworten des Kerns: 200 setzt last_run/last_end, busy zählt skipped_busy,
// die Pausen spiegeln den Grund; 409 und Netzfehler setzen nichts.
func TestRecordMirrorsAnswers(t *testing.T) {
	tests := []struct {
		name    string
		outcome Outcome
		lastEnd string
		state   string
		reason  string
		busy    uint64
	}{
		{"ok", Outcome{Kind: Ran, End: "ok"}, "ok", contract.ScheduleReady, "", 0},
		{"Skriptfehler pausiert nicht", Outcome{Kind: Ran, End: "error/exception"}, "error/exception", contract.ScheduleReady, "", 0},
		{"Limit", Outcome{Kind: Ran, End: "limit/memory"}, "limit/memory", contract.ScheduleReady, "", 0},
		{"timeout", Outcome{Kind: Ran, End: "timeout"}, "timeout", contract.ScheduleReady, "", 0},
		{"internal", Outcome{Kind: Ran, End: "internal"}, "internal", contract.ScheduleReady, "", 0},
		{"busy", Outcome{Kind: Ran, End: "busy"}, "busy", contract.ScheduleReady, "", 1},
		{"Pause Token", Outcome{Kind: Ran, End: "paused/token"}, "paused/token", contract.SchedulePaused, contract.ScheduleReasonToken, 0},
		{"Pause Pin", Outcome{Kind: Ran, End: "paused/pin"}, "paused/pin", contract.SchedulePaused, contract.ScheduleReasonPin, 0},
		{"refused/commit", Outcome{Kind: Ran, End: "refused/commit"}, "refused/commit", contract.SchedulePaused, contract.ScheduleReasonPin, 0},
		{"refused/state", Outcome{Kind: Ran, End: "refused/state"}, "refused/state", contract.SchedulePaused, contract.ScheduleReasonPin, 0},
		{"Freitext", Outcome{Kind: Ran, End: "KANARIE geheim\nzeile"}, "unknown", contract.ScheduleReady, "", 0},
		{"unbekanntes detail", Outcome{Kind: Ran, End: "error/KANARIE"}, "unknown", contract.ScheduleReady, "", 0},
		{"409", Outcome{Kind: Refused}, "", contract.ScheduleReady, "", 0},
		{"Netzfehler", Outcome{Kind: Failed}, "", contract.ScheduleReady, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tk := newTicker()
			es := entries(t, row("daily", "0 8 * * *", "UTC", true))
			tk.Tick(at(7, 59, 59), es, readyAll)
			due := tk.Tick(at(8, 0, 1), es, readyAll)
			if len(due) != 1 {
				t.Fatalf("Due = %v", due)
			}
			tk.Record(due[0].Name, due[0].Slot, tt.outcome)
			tk.Tick(at(8, 0, 3), es, readyAll)
			s := status(t, tk, "daily")
			wantRun := "2026-10-12T08:00:00Z"
			if tt.lastEnd == "" {
				wantRun = ""
			}
			if s.LastEnd != tt.lastEnd || s.LastRun != wantRun || s.State != tt.state || s.Reason != tt.reason || s.SkippedBusy != tt.busy {
				t.Fatalf("Status = %+v", s)
			}
		})
	}
}

// Die Pause aus einer Antwort bleibt sichtbar, solange der Kern den Zeitplan
// mit enabled:false führt; erst eine erneute Aktivierung hebt sie auf.
func TestPauseMirroredUntilReEnabled(t *testing.T) {
	tk := newTicker()
	on := entries(t, row("daily", "0 8 * * *", "UTC", true))
	off := entries(t, row("daily", "0 8 * * *", "UTC", false))
	tk.Tick(at(7, 59, 59), on, readyAll)
	due := tk.Tick(at(8, 0, 1), on, readyAll)
	tk.Record(due[0].Name, due[0].Slot, Outcome{Kind: Ran, End: "paused/token"})
	tk.Tick(at(8, 0, 3), off, readyAll)
	if s := status(t, tk, "daily"); s.State != contract.SchedulePaused || s.Reason != contract.ScheduleReasonToken || s.NextRun != "" {
		t.Fatalf("pausiert = %+v", s)
	}
	tk.Tick(at(9, 0, 0), on, readyAll)
	if s := status(t, tk, "daily"); s.State != contract.ScheduleReady || s.Reason != "" || s.NextRun == "" {
		t.Fatalf("bestätigt = %+v", s)
	}
	// Eine spätere bewusste Deaktivierung ist keine Pause.
	tk.Tick(at(9, 0, 2), off, readyAll)
	if s := status(t, tk, "daily"); s.State != contract.ScheduleDisabled || s.Reason != "" {
		t.Fatalf("deaktiviert = %+v", s)
	}
}

// Ein Zeitplan, der aus der Datei verschwindet, verschwindet aus dem Bericht;
// ein spätes Ergebnis für ihn wird verworfen.
func TestRemovedScheduleLeavesReport(t *testing.T) {
	tk := newTicker()
	es := entries(t, row("daily", "0 8 * * *", "UTC", true), row("other", "0 9 * * *", "UTC", true))
	tk.Tick(at(7, 59, 59), es, readyAll)
	due := tk.Tick(at(8, 0, 1), es, readyAll)
	tk.Tick(at(8, 0, 3), es[1:], readyAll)
	tk.Record(due[0].Name, due[0].Slot, Outcome{Kind: Ran, End: "ok"})
	rep := tk.Report()
	if len(rep) != 1 || rep[0].Name != "other" {
		t.Fatalf("Bericht = %+v", rep)
	}
}

// Invariante 8 (Taktgeber-Seite): höchstens 20 Einträge und genau die
// Vertragsfelder, keine Personenachse, kein Freitext.
func TestReportAtMostTwentyAndOnlyContractFields(t *testing.T) {
	rows := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		rows = append(rows, row(fmt.Sprintf("s%02d", i), "0 8 * * *", "UTC", true))
	}
	f, err := schedules.Parse([]byte(`{"version":1,"schedules":[` + strings.Join(rows, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	tk := newTicker()
	tk.Tick(at(7, 0, 0), f.Entries, readyAll)
	rep := tk.Report()
	if len(rep) != 20 {
		t.Fatalf("%d Einträge im Bericht", len(rep))
	}
	raw, _ := json.Marshal(rep)
	var objs []map[string]any
	if err := json.Unmarshal(raw, &objs); err != nil {
		t.Fatal(err)
	}
	want := []string{"last_end", "last_run", "missed", "name", "next_run", "reason", "skipped_busy", "state"}
	for _, o := range objs {
		keys := make([]string, 0, len(o))
		for k := range o {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != strings.Join(want, ",") {
			t.Fatalf("Felder = %v, want %v", keys, want)
		}
	}
	if rep[0].Name != "s00" || rep[19].Name != "s19" {
		t.Fatalf("Reihenfolge nicht wie in der Datei: %s … %s", rep[0].Name, rep[19].Name)
	}
}

// Verpasste Termine landen im Log nur als fester Code mit Zählwert.
func TestMissedLogsFixedCodeOnly(t *testing.T) {
	var buf bytes.Buffer
	tk := New(slog.New(slog.NewTextHandler(&buf, nil)))
	es := entries(t, row("geheimname", "0 8 * * *", "UTC", true))
	tk.Tick(at(7, 59, 0), es, readyAll)
	tk.Tick(at(8, 5, 0), es, readyAll)
	log := buf.String()
	if !strings.Contains(log, CodeMissed) || strings.Contains(log, "geheimname") {
		t.Fatalf("Log = %q", log)
	}
}
