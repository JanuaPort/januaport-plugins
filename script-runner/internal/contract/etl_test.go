package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rückfallregel (Fassung 6, E4): Fassung 3 nur bei einer streng gelesenen Kern-
// Version ab MinCoreVersion; alles andere, auch jede unlesbare Angabe, ist 2.
func TestForCoreVersion(t *testing.T) {
	tests := []struct {
		name string
		core string
		want int
	}{
		{"genau die Mindestversion", "0.100.0", 3},
		{"mit v", "v0.100.0", 3},
		{"höhere Minor", "0.101.0", 3},
		{"höhere Patch", "0.100.7", 3},
		{"neue Major", "1.0.0", 3},
		{"Vorgänger-Minor", "0.99.0", 2},
		{"Vorgänger mit v", "v0.99.9", 2},
		{"alt", "0.77.0", 2},
		{"dev-Build", "dev", 2},
		{"leer", "", 2},
		{"nur zwei Teile", "0.100", 2},
		{"vier Teile", "0.100.0.1", 2},
		{"Vorabversion", "0.100.0-rc1", 2},
		{"Build-Anhang", "0.100.0+abc", 2},
		{"Leerzeichen", " 0.100.0", 2},
		{"Zeilenumbruch", "0.100.0\n", 2},
		{"führende Null", "0.0100.0", 2},
		{"Minuszeichen", "0.-100.0", 2},
		{"Buchstaben", "0.x.0", 2},
		{"Überlauf", "0.99999999999999999999.0", 2},
		{"Steuerzeichen", "0.100.0\x00", 2},
		{"Großes V", "V0.100.0", 2},
		{"nur Zahl", "3", 2},
		{"Kontrollwert Text", "ok", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ForCoreVersion(tt.core); got != tt.want {
				t.Fatalf("ForCoreVersion(%q) = %d, want %d", tt.core, got, tt.want)
			}
		})
	}
}

// Die Mindestversion ist die nächste Kern-Minor nach dem Tag v0.99.0 und ein
// Literal: Das Gate setzt sie beim Release, die Änderung ist sichtbar.
func TestMinCoreVersionLiteral(t *testing.T) {
	if MinCoreVersion != "0.100.0" {
		t.Fatalf("MinCoreVersion = %q", MinCoreVersion)
	}
}

func TestStreamStatusSet(t *testing.T) {
	want := []string{"fertig", "abgebrochen", "geschlossen", "grenze", "vertrag", "abgewiesen"}
	got := []string{StreamFertig, StreamAbgebrochen, StreamGeschlossen, StreamGrenze, StreamVertrag, StreamAbgewiesen}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Status %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Das Feld `stream` ist ein Vertragsfeld ab Kennung 3: Form wie die
// vorgeschlagenen Beispiele des Kerns (K2). Die alten Goldens bleiben bytegleich.
func TestStreamResultGoldens(t *testing.T) {
	golden(t, "end_ok_stream_fertig.json", Result(Run{End: EndOK, BytesOut: 14, Isolation: IsolationStandard,
		Stream: &StreamReport{Status: StreamFertig, Bytes: 1048576, Zeilen: 12000}}, []byte("12000 Zeilen.\n")))
	golden(t, "end_ok_stream_abgebrochen.json", Result(Run{End: EndOK, BytesOut: 13, Isolation: IsolationStandard,
		Stream: &StreamReport{Status: StreamAbgebrochen, Bytes: 524288, Zeilen: 6000}}, []byte("6000 Zeilen.\n")))
}

func TestRunWithoutStreamHasNoStreamField(t *testing.T) {
	b, _ := json.Marshal(Run{End: EndOK, Isolation: IsolationStandard})
	if strings.Contains(string(b), "stream") {
		t.Fatalf("Feld stream ohne Strom: %s", b)
	}
	b, _ = json.Marshal(Run{End: EndOK, Isolation: IsolationStandard, Stream: &StreamReport{Status: StreamAbgewiesen}})
	if !strings.Contains(string(b), `"stream":{"status":"abgewiesen","bytes":0,"zeilen":0}`) {
		t.Fatalf("Zähler fehlen im Feld stream: %s", b)
	}
}

// Fassung 1 und 2 bleiben bytegleich: Die Altgoldens verändert diese Änderung
// nicht (`git diff` im Gate-Bericht belegt es byteweise; der Test hält die Form).
func TestOldGoldensUnchanged(t *testing.T) {
	for file, mustNotContain := range map[string]string{
		"tools_list.json":                   `"contract": 3`,
		"tools_list_wall.json":              `"contract": 3`,
		"tools_list_schedules.json":         `"contract": 3`,
		"tools_list_isolation_invalid.json": `"contract": 3`,
		"end_ok_text.json":                  `"stream"`,
		"end_ok_log.json":                   `"stream"`,
	} {
		b, err := os.ReadFile(filepath.Join(goldenDir, file))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), mustNotContain) {
			t.Errorf("%s trägt %s: Altgolden verändert", file, mustNotContain)
		}
		if !strings.Contains(string(b), `"contract": 2`) && strings.HasPrefix(file, "tools_list") {
			t.Errorf("%s hat nicht mehr contract 2", file)
		}
	}
}
