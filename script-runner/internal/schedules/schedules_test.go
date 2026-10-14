package schedules

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

var commit = strings.Repeat("3", 40)

func row(name, cron, zone string) string {
	return fmt.Sprintf(`{"name":%q,"pin":"invoice_check","commit":%q,"cron":%q,"timezone":%q,"enabled":true}`, name, commit, cron, zone)
}

func file(rows ...string) []byte {
	return []byte(`{"version":1,"schedules":[` + strings.Join(rows, ",") + `]}`)
}

// Das Golden des Kerns (cmd/jnpt/testdata/script-runner-schedules.golden.json,
// unverändert übernommen) liest der S1-Leser ohne Abstriche.
func TestParseCoreGolden(t *testing.T) {
	raw, err := os.ReadFile("../../contract/golden/script-runner-schedules.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(raw)
	if err != nil {
		t.Fatalf("Golden des Kerns abgewiesen: %v", err)
	}
	if f.Ignored != 0 || len(f.Entries) != 1 {
		t.Fatalf("File = %+v", f)
	}
	e := f.Entries[0]
	if e.Name != "daily" || e.Pin != "invoice_check" || e.Commit != commit || e.Cron != "0 8 * * 1-5" || e.Timezone != "Europe/Berlin" || !e.Enabled {
		t.Fatalf("Entry = %+v", e)
	}
	// Mo 12.10.2026, 08:00 Europe/Berlin = 06:00 UTC.
	if got := e.Next(time.Date(2026, 10, 12, 5, 0, 0, 0, time.UTC)); !got.Equal(time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("Next = %v", got)
	}
}

func TestParseEmptyListMeansNoSchedules(t *testing.T) {
	f, err := Parse([]byte(`{"version":1,"schedules":[]}`))
	if err != nil || len(f.Entries) != 0 || f.Ignored != 0 {
		t.Fatalf("File = %+v, err = %v", f, err)
	}
}

// Invariante 5: ungültig oder unbekanntes Feld → keine Zeitpläne (fail-closed).
func TestParseInvalidFailsClosed(t *testing.T) {
	good := row("daily", "0 8 * * 1-5", "Europe/Berlin")
	tests := []struct {
		name string
		data string
	}{
		{"kein JSON", `{"version":1,`},
		{"leer", ``},
		{"null", `null`},
		{"Liste fehlt", `{"version":1}`},
		{"Liste null", `{"version":1,"schedules":null}`},
		{"Version fehlt", `{"schedules":[` + good + `]}`},
		{"Version 2", `{"version":2,"schedules":[` + good + `]}`},
		{"Version als Text", `{"version":"1","schedules":[` + good + `]}`},
		{"unbekanntes Feld oben", `{"version":1,"schedules":[` + good + `],"token_id":"x"}`},
		{"unbekanntes Feld im Eintrag", `{"version":1,"schedules":[` + strings.Replace(good, `"enabled":true`, `"enabled":true,"input":{}`, 1) + `]}`},
		{"Daten nach dem Objekt", `{"version":1,"schedules":[` + good + `]} {}`},
		{"enabled fehlt", `{"version":1,"schedules":[` + strings.Replace(good, `,"enabled":true`, ``, 1) + `]}`},
		{"Name ungültig", string(file(row("../bad", "0 8 * * *", "UTC")))},
		{"Name leer", string(file(row("", "0 8 * * *", "UTC")))},
		{"Pin ungültig", string(file(strings.Replace(good, `"invoice_check"`, `"Invoice"`, 1)))},
		{"Commit ungültig", string(file(strings.Replace(good, commit, "abc", 1)))},
		{"Name doppelt", string(file(good, good))},
		{"sechs Felder", string(file(row("a", "0 0 8 * * *", "UTC")))},
		{"vier Felder", string(file(row("a", "0 8 * *", "UTC")))},
		{"Cron kaputt", string(file(row("a", "61 8 * * *", "UTC")))},
		{"Deskriptor statt Feldern", string(file(row("a", "@daily", "UTC")))},
		{"Zeitzone leer", string(file(row("a", "0 8 * * *", "")))},
		{"Zeitzone Local", string(file(row("a", "0 8 * * *", "Local")))},
		{"Zeitzone unbekannt", string(file(row("a", "0 8 * * *", "Mars/Olympus")))},
		{"Abstand unter fünf Minuten", string(file(row("a", "*/2 * * * *", "UTC")))},
		{"Abstand über die volle Stunde", string(file(row("a", "0,58 8,9 * * *", "UTC")))},
		{"nie fällig", string(file(row("a", "0 8 30 2 *", "UTC")))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Parse([]byte(tt.data))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if len(f.Entries) != 0 {
				t.Fatalf("ungültige Datei liefert %d Zeitpläne", len(f.Entries))
			}
		})
	}
}

// Der Fehler trägt nie Inhalt der Datei (fester Log-Code beim Aufrufer).
func TestParseErrorCarriesNoContent(t *testing.T) {
	_, err := Parse(file(row("a", "0 8 * * *", "KANARIE/4711")))
	if err == nil || strings.Contains(err.Error(), "KANARIE") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseAcceptsSpacingOfFiveMinutes(t *testing.T) {
	for _, cron := range []string{"*/5 * * * *", "0,55 8,9 * * *", "30 2 * * *", "0 8 * * mon-fri", "0 8 1 jan *"} {
		if _, err := Parse(file(row("a", cron, "Europe/Berlin"))); err != nil {
			t.Errorf("%q abgewiesen: %v", cron, err)
		}
	}
}

// Höchstens 20 Zeitpläne; weitere werden ignoriert und gezählt.
func TestParseKeepsTwentyAndCountsRest(t *testing.T) {
	rows := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		rows = append(rows, row(fmt.Sprintf("s%02d", i), "0 8 * * *", "UTC"))
	}
	f, err := Parse(file(rows...))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != Max || f.Ignored != 5 || Max != 20 {
		t.Fatalf("%d Einträge, %d ignoriert", len(f.Entries), f.Ignored)
	}
	if f.Entries[0].Name != "s00" || f.Entries[19].Name != "s19" {
		t.Fatalf("Reihenfolge: %s … %s", f.Entries[0].Name, f.Entries[19].Name)
	}
}

// Invariante 7 (Kalender): Die Lücke am letzten Sonntag im März
// (29.03.2026, 02:00 → 03:00 Europe/Berlin) hat kein 02:30. Next überspringt
// den Termin, GapMisses zählt ihn.
func TestDSTGapEuropeBerlin(t *testing.T) {
	gapStart := time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC) // 02:00 CET = 03:00 CEST
	tests := []struct {
		cron string
		want int
	}{
		{"30 2 * * *", 1},
		{"*/15 2 * * *", 4},
		{"0 2 * * *", 1},
		{"0 3 * * *", 0},
		{"30 2 * * 1", 0}, // der 29.03.2026 ist ein Sonntag
		{"30 2 29 3 *", 1},
		{"30 1 * * *", 0},
	}
	for _, tt := range tests {
		t.Run(tt.cron, func(t *testing.T) {
			f, err := Parse(file(row("a", tt.cron, "Europe/Berlin")))
			if err != nil {
				t.Fatal(err)
			}
			e := f.Entries[0]
			if got := e.GapMisses(gapStart.Add(-time.Hour), gapStart.Add(time.Hour)); got != tt.want {
				t.Fatalf("GapMisses = %d, want %d", got, tt.want)
			}
			// Außerhalb des Intervalls zählt nichts, auch wenn es knapp ist.
			if got := e.GapMisses(gapStart, gapStart.Add(time.Hour)); got != 0 {
				t.Fatalf("GapMisses ab dem Wechsel = %d, want 0", got)
			}
			if got := e.GapMisses(gapStart.Add(-time.Hour), gapStart.Add(-time.Second)); got != 0 {
				t.Fatalf("GapMisses vor dem Wechsel = %d, want 0", got)
			}
		})
	}
	f, _ := Parse(file(row("a", "30 2 * * *", "Europe/Berlin")))
	if got := f.Entries[0].Next(gapStart.Add(-time.Minute)); !got.Equal(time.Date(2026, 3, 30, 0, 30, 0, 0, time.UTC)) {
		t.Fatalf("Next über die Lücke = %v, want 2026-03-30T00:30:00Z", got)
	}
}

// Die Rückstellung im Oktober ist keine Lücke.
func TestFallBackIsNoGap(t *testing.T) {
	f, err := Parse(file(row("a", "30 2 * * *", "Europe/Berlin")))
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC)
	if got := f.Entries[0].GapMisses(from, from.Add(6*time.Hour)); got != 0 {
		t.Fatalf("GapMisses im Oktober = %d", got)
	}
}
