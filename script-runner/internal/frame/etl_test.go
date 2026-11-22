package frame

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const goldenDir = "../../contract/golden"

// goldenFrame prüft, dass der Rahmen genau die Golden-Datei ergibt (Fassung 3,
// #1215 P2). Die Reihenfolge der Schlüssel ist Teil des Vertrags: BOX und der
// Kern lesen dieselben Dateien.
func goldenFrame(t *testing.T, file string, v any) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join(goldenDir, file))
	if err != nil {
		t.Fatalf("Golden-Datei fehlt: %v", err)
	}
	var wantCompact bytes.Buffer
	if err := json.Compact(&wantCompact, want); err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != wantCompact.String() {
		t.Fatalf("%s weicht ab:\n got  %s\n want %s", file, got, wantCompact.String())
	}
}

func TestETLFramesGolden(t *testing.T) {
	goldenFrame(t, "frame_etl_open.json", ETLOpen{Type: TypeETLOpen, ID: 1, Integration: "erp_db", QueryID: "offene_posten",
		Params: json.RawMessage(`{"stichtag":"2026-11-18"}`)})
	goldenFrame(t, "frame_etl_next.json", ETLNext{Type: TypeETLNext, ID: 1})
	goldenFrame(t, "frame_etl_chunk.json", ETLChunk{Type: TypeETLChunk, ID: 1, DataB64: base64.StdEncoding.EncodeToString([]byte("{\"t\":\"row\",\"d\":{\"nr\":1}}\n"))})
	goldenFrame(t, "frame_etl_end.json", ETLEnd{Type: TypeETLEnd, ID: 1, Status: ETLStatusAbgewiesen, Code: 429, RetryAfterS: 12})
}

func TestETLNextCloseAndEndOptionalFields(t *testing.T) {
	b, _ := json.Marshal(ETLNext{Type: TypeETLNext, ID: 7, Close: true})
	if string(b) != `{"type":"etl_next","id":7,"close":true}` {
		t.Fatalf("close-Rahmen: %s", b)
	}
	b, _ = json.Marshal(ETLEnd{Type: TypeETLEnd, ID: 7, Status: ETLStatusEOF})
	if string(b) != `{"type":"etl_end","id":7,"status":"eof"}` {
		t.Fatalf("end ohne Code und Wartezeit: %s", b)
	}
}

func TestETLTypesAreDistinctFromExistingTypes(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range []string{TypeHello, TypeJob, TypeToolCall, TypeToolResult, TypeOutput, TypeLog, TypeExited, TypeError,
		TypeETLOpen, TypeETLNext, TypeETLChunk, TypeETLEnd} {
		if s == "" || seen[s] {
			t.Fatalf("Rahmentyp leer oder doppelt: %q", s)
		}
		seen[s] = true
	}
	if TypeETLOpen != "etl_open" || TypeETLNext != "etl_next" || TypeETLChunk != "etl_chunk" || TypeETLEnd != "etl_end" {
		t.Fatal("Rahmennamen geändert")
	}
}

func TestETLStatusSet(t *testing.T) {
	want := []string{"eof", "abgewiesen", "abgebrochen", "grenze", "vertrag", "geschlossen"}
	got := []string{ETLStatusEOF, ETLStatusAbgewiesen, ETLStatusAbgebrochen, ETLStatusGrenze, ETLStatusVertrag, ETLStatusGeschlossen}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Status %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Pflicht-Prüfpunkt Rahmengröße (#1215): Ein volles Stück (256 KiB roh) muss
// samt Base64 (≈ 342 KiB) und JSON durch BEIDE Leser passen, die der Rahmen auf
// seinem Weg trifft. Die Messung steht im Testlog.
func TestETLFullChunkFitsEveryFrameLimit(t *testing.T) {
	if MaxETLChunkRaw != 256<<10 || MaxETLOpen != 64<<10 {
		t.Fatalf("Stückgrößen geändert: chunk=%d open=%d", MaxETLChunkRaw, MaxETLOpen)
	}
	raw := bytes.Repeat([]byte{0xff}, MaxETLChunkRaw) // Worst Case: kein Zeichen schrumpft
	var wire bytes.Buffer
	if err := Write(&wire, ETLChunk{Type: TypeETLChunk, ID: 1<<53 - 1, DataB64: base64.StdEncoding.EncodeToString(raw)}); err != nil {
		t.Fatal(err)
	}
	payload := wire.Len() - 4
	t.Logf("volles Stück: roh %d, Rahmen-Nutzlast %d Byte (%.1f KiB); MaxFromSandbox %d, Ladegrenze des Wächters 8 MiB",
		len(raw), payload, float64(payload)/1024, MaxFromSandbox)
	if payload > MaxETLChunkFrame {
		t.Fatalf("volles Stück %d Byte übersteigt MaxETLChunkFrame %d", payload, MaxETLChunkFrame)
	}
	if MaxETLChunkFrame > MaxFromSandbox {
		t.Fatalf("MaxETLChunkFrame %d übersteigt die kleinste Leserschranke %d", MaxETLChunkFrame, MaxFromSandbox)
	}
	got, err := Read(&wire, MaxFromSandbox)
	if err != nil {
		t.Fatalf("volles Stück passt nicht durch den Leser: %v", err)
	}
	var c ETLChunk
	if err := json.Unmarshal(got, &c); err != nil {
		t.Fatal(err)
	}
	back, err := base64.StdEncoding.DecodeString(c.DataB64)
	if err != nil || !bytes.Equal(back, raw) {
		t.Fatalf("Stück nach dem Durchlauf verändert: %v", err)
	}
}

// Rahmengrenze: Ein Stück über MaxETLChunkRaw ist nicht erlaubt; die
// Leserschranke setzt die Größe beim Lesen durch, nicht nur beim Schreiben.
func TestETLOversizedFrameRejectedAtRead(t *testing.T) {
	var wire bytes.Buffer
	big := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, MaxFromSandbox))
	if err := Write(&wire, ETLChunk{Type: TypeETLChunk, ID: 1, DataB64: big}); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(&wire, MaxFromSandbox); err != ErrTooLarge {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}
