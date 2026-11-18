package pipe

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// zeile baut eine zustand-Nachricht mit einer Setup-Zeile, deren letzter Lauf frei einstellbar ist.
func zeile(lauf string) string {
	row := `{"pin":"demo","setup":"raum","label":"Raum","startbar":true,"braucht_eingabe":false`
	if lauf != "" {
		row += "," + lauf
	}
	row += "}"
	return `{"type":"zustand","zustand":"arbeitet","test":false,"netz":true,"beschaeftigt":false,"setups":[` + row + `]}`
}

func TestLetzterLaufSchemaTabelle(t *testing.T) {
	for _, tc := range []struct {
		name, lauf string
		ok         bool
	}{
		{"alter Läufer ohne Feld", ``, true},
		{"ok", `"zuletzt":"2026-11-17T14:05:00Z","ergebnis":"ok"`, true},
		{"Fehler mit Code", `"zuletzt":"2026-11-17T14:05:00Z","ergebnis":"fehler","code":"timeout"`, true},
		{"Fehler refused/runtime", `"zuletzt":"2026-11-17T14:05:00Z","ergebnis":"fehler","code":"refused/runtime"`, true},
		{"nicht bereit", `"ergebnis":"nicht_bereit"`, true},
		{"nie", `"ergebnis":"nie"`, true},
		{"ok ohne Zeitpunkt", `"ergebnis":"ok"`, false},
		{"Fehler ohne Code", `"zuletzt":"2026-11-17T14:05:00Z","ergebnis":"fehler"`, false},
		{"Fehler ohne Zeitpunkt", `"ergebnis":"fehler","code":"timeout"`, false},
		{"ok mit Code", `"zuletzt":"2026-11-17T14:05:00Z","ergebnis":"ok","code":"timeout"`, false},
		{"nie mit Zeitpunkt", `"zuletzt":"2026-11-17T14:05:00Z","ergebnis":"nie"`, false},
		{"nicht bereit mit Code", `"ergebnis":"nicht_bereit","code":"timeout"`, false},
		{"Zeitpunkt ohne Ergebnis", `"zuletzt":"2026-11-17T14:05:00Z"`, false},
		{"Code ohne Ergebnis", `"code":"timeout"`, false},
		{"unbekanntes Ergebnis", `"ergebnis":"gruen"`, false},
		{"Ergebnis in Großschreibung", `"ergebnis":"OK","zuletzt":"2026-11-17T14:05:00Z"`, false},
		{"Zeitpunkt nicht UTC", `"zuletzt":"2026-11-17T14:05:00+01:00","ergebnis":"ok"`, false},
		{"Zeitpunkt kein Zeitpunkt", `"zuletzt":"gestern","ergebnis":"ok"`, false},
		{"Code mit Pfad", `"zuletzt":"2026-11-17T14:05:00Z","ergebnis":"fehler","code":"C:\\Users\\x"`, false},
		{"Code mit Leerzeichen", `"zuletzt":"2026-11-17T14:05:00Z","ergebnis":"fehler","code":"zu lang"`, false},
		{"Code zu lang", `"zuletzt":"2026-11-17T14:05:00Z","ergebnis":"fehler","code":"` + strings.Repeat("a", 40) + `"`, false},
		{"Code null", `"zuletzt":"2026-11-17T14:05:00Z","ergebnis":"fehler","code":null`, false},
		{"fremdes Feld", `"ergebnis":"nie","ausgabe":"x"`, false},
		{"Eingabe als Feld", `"ergebnis":"nie","input":{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := DecodeServer([]byte(zeile(tc.lauf)))
			if (err == nil) != tc.ok {
				t.Fatalf("ok=%v, Fehler=%v", tc.ok, err)
			}
			if tc.ok && len(m.Setups) != 1 {
				t.Fatal("Zeile verloren")
			}
		})
	}
}

func TestLetzterLaufRundreise(t *testing.T) {
	for _, s := range []Setup{
		{Pin: "demo", Setup: "raum", Label: "Raum", Zuletzt: "2026-11-17T14:05:00Z", Ergebnis: "ok"},
		{Pin: "demo", Setup: "raum", Label: "Raum", Zuletzt: "2026-11-17T14:05:00Z", Ergebnis: "fehler", Code: "refused/runtime"},
		{Pin: "demo", Setup: "raum", Label: "Raum", Ergebnis: "nicht_bereit"},
		{Pin: "demo", Setup: "raum", Label: "Raum", Ergebnis: "nie"},
	} {
		b, err := json.Marshal(Message{Type: "zustand", Zustand: "arbeitet", Netz: true, Setups: []Setup{s}})
		if err != nil {
			t.Fatal(s, err)
		}
		got, err := DecodeServer(b)
		if err != nil || !reflect.DeepEqual(got.Setups, []Setup{s}) {
			t.Fatal(s, err, string(b))
		}
	}
	if _, err := json.Marshal(Message{Type: "zustand", Zustand: "arbeitet", Setups: []Setup{{Pin: "demo", Setup: "raum", Label: "Raum", Ergebnis: "ok"}}}); err == nil {
		t.Fatal("ok ohne Zeitpunkt wird nicht gesendet")
	}
}

// Die Pipe trägt je Zeile genau diese Schlüssel: Zeitpunkt und Status, nichts sonst (kein Personenbezug,
// keine Eingabe, keine Ausgabe, kein Fenstertitel).
func TestLetzterLaufNurZeitpunktUndStatus(t *testing.T) {
	var keys []string
	typ := reflect.TypeOf(Setup{})
	for i := 0; i < typ.NumField(); i++ {
		keys = append(keys, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(keys)
	want := []string{"braucht_eingabe", "code", "ergebnis", "label", "pin", "setup", "startbar", "zuletzt"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatal("Setup-Zeile trägt andere Schlüssel", keys)
	}
}
