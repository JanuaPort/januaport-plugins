package panel

import (
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

var lokal = time.FixedZone("test-lokal", 60*60)

func TestLetzterLaufZeileTabelle(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   pipe.Setup
		want string
		show bool
	}{
		{"ok", pipe.Setup{Zuletzt: "2026-11-17T13:05:00Z", Ergebnis: "ok"}, "zuletzt gelaufen (seit Start): 17.11.2026 14:05 · ok", true},
		{"Fehler mit Code", pipe.Setup{Zuletzt: "2026-11-17T13:05:00Z", Ergebnis: "fehler", Code: "timeout"}, "zuletzt gelaufen (seit Start): 17.11.2026 14:05 · Fehler timeout", true},
		{"Fehler refused/runtime", pipe.Setup{Zuletzt: "2026-11-17T13:05:00Z", Ergebnis: "fehler", Code: "refused/runtime"}, "zuletzt gelaufen (seit Start): 17.11.2026 14:05 · Fehler refused/runtime", true},
		{"nicht bereit", pipe.Setup{Ergebnis: "nicht_bereit"}, "zuletzt gelaufen (seit Start): nicht bereit", true},
		{"nie", pipe.Setup{Ergebnis: "nie"}, "zuletzt gelaufen (seit Start): nie", true},
		{"Läufer ohne Angabe", pipe.Setup{}, "", false},
		{"unlesbarer Zeitpunkt", pipe.Setup{Zuletzt: "gestern", Ergebnis: "ok"}, "", false},
		{"unbekanntes Ergebnis", pipe.Setup{Zuletzt: "2026-11-17T13:05:00Z", Ergebnis: "gruen"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := LastRunText(tc.in, lokal)
			if ok != tc.show || got != tc.want {
				t.Fatalf("%q, %v", got, ok)
			}
		})
	}
}

// Das Menü trägt je Setup eine abgeschaltete Zeile und sonst nichts aus dem letzten Lauf.
func TestLetzterLaufImMenue(t *testing.T) {
	local := time.Local
	time.Local = lokal
	t.Cleanup(func() { time.Local = local })
	m := New()
	m.Connection(Connected)
	receive(t, m, pipe.Message{Type: "zustand", Zustand: "arbeitet", Netz: true, Setups: []pipe.Setup{
		{Pin: "demo", Setup: "raum", Label: "Raum", Startbar: true, Zuletzt: "2026-11-17T13:05:00Z", Ergebnis: "ok"},
		{Pin: "demo", Setup: "lager", Label: "Lager", Ergebnis: "nicht_bereit"},
		{Pin: "zwei", Setup: "neu", Label: "Neu", Ergebnis: "nie"},
	}})
	var order []string
	for _, i := range m.View().Menu {
		if strings.HasPrefix(i.ID, "setup:") || strings.HasPrefix(i.ID, "lauf:") {
			order = append(order, i.ID)
		}
	}
	want := []string{"setup:demo/raum", "lauf:demo/raum", "setup:demo/lager", "lauf:demo/lager", "setup:zwei/neu", "lauf:zwei/neu"}
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Fatal(order)
	}
	if got := item(t, m, "lauf:demo/raum"); got.Enabled || got.Text != "zuletzt gelaufen (seit Start): 17.11.2026 14:05 · ok" {
		t.Fatal(got)
	}
	if got := item(t, m, "lauf:demo/lager"); got.Enabled || !strings.HasSuffix(got.Text, "nicht bereit") {
		t.Fatal(got)
	}
	// Eine Zeile ist keine Aktion.
	if e := m.Choose("lauf:demo/raum", now); len(e.Commands) != 0 || e.OpenApp != "" || e.Quit {
		t.Fatal(e)
	}
	// Ältere Läufer ohne die Angabe: keine zusätzliche Zeile.
	old := New()
	old.Connection(Connected)
	receive(t, old, pipe.Message{Type: "zustand", Zustand: "arbeitet", Netz: true, Setups: []pipe.Setup{{Pin: "demo", Setup: "raum", Label: "Raum"}}})
	for _, i := range old.View().Menu {
		if strings.HasPrefix(i.ID, "lauf:") {
			t.Fatal("Zeile ohne Angabe", i)
		}
	}
}

// Nur Zeitpunkt und Status erreichen die Anzeige: Pin, Setup und Beschriftung stehen in der Zeile nicht.
func TestLetzterLaufZeileOhneFremdeFelder(t *testing.T) {
	s := pipe.Setup{Pin: "pinname", Setup: "setupname", Label: "Beschriftung", Startbar: true, BrauchtEingabe: true, Zuletzt: "2026-11-17T13:05:00Z", Ergebnis: "fehler", Code: "error"}
	got, _ := LastRunText(s, lokal)
	for _, bad := range []string{"pinname", "setupname", "Beschriftung"} {
		if strings.Contains(got, bad) {
			t.Fatal(bad, got)
		}
	}
}
