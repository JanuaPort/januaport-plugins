package pipe

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func rawFrame(s string) []byte {
	b := make([]byte, 4+len(s))
	binary.BigEndian.PutUint32(b, uint32(len(s)))
	copy(b[4:], s)
	return b
}

func TestPipeBefehleGeschlossen(t *testing.T) {
	for _, s := range []string{
		`{"type":"fremd"}`, `{"type":"screenshot"}`, `{"type":"ergebnis","wert":42}`,
		`{"type":"status","pin":"demo"}`, `{"type":"status","input":{}}`,
		`{"type":"status","mode":"always"}`, `{"type":"status","token":"synthetic"}`,
		`{"type":"status","path":"demo"}`, `{"type":"status","Type":"status"}`,
		`{"type":"status","type":"anhalten"}`, `{"type":"status"} {}`,
		`{"type":"pause"}`, `{"type":"pause","minuten":null}`, `{"type":"pause","minuten":1}`,
		`{"type":"pause","minuten":-1}`, `{"type":"pause","minuten":15.0}`,
		`{"type":"setup_start","pin":"demo","setup":"raum","frei":{}}`,
		`{"type":"setup_start","pin":"../demo","setup":"raum"}`,
		`{"type":"setup_start","pin":"demo"}`, `{"type":"antwort","id":"x","wahl":4}`,
		`{"type":"antwort","id":"x","wahl":-1}`, `{"type":"antwort","id":"x"}`,
		`{"type":"zustand","zustand":"arbeitet","test":false,"netz":true,"beschaeftigt":false,"setups":[]}`,
		"null", "[]", strings.Repeat(" ", 4097),
		`{"type":"status"}` + strings.Repeat(" ", 4080),
	} {
		if _, err := DecodeClient([]byte(s)); err == nil {
			t.Errorf("Befehl angenommen: %s", s)
		}
	}
	for _, n := range []int{0, 4097, 1 << 30} {
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], uint32(n))
		if _, err := ReadFrame(bytes.NewReader(h[:])); err == nil {
			t.Fatalf("Rahmen %d", n)
		}
	}
	if _, err := ReadFrame(bytes.NewReader(rawFrame(strings.Repeat("x", 4097)))); err == nil {
		t.Fatal("vollständiger übergroßer Rahmen")
	}
	// A valid 4096-byte payload is accepted; no off-by-one or read-ahead.
	s := `{"type":"status"}`
	s += strings.Repeat(" ", 4096-len(s))
	got, err := ReadFrame(bytes.NewReader(rawFrame(s)))
	if err != nil || len(got) != 4096 {
		t.Fatal(err, len(got))
	}
	if _, err = DecodeClient(got); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadFrame(bytes.NewReader(rawFrame(`{}`)[:5])); err == nil {
		t.Fatal("abgeschnittener Rahmen")
	}
}

func TestPipeGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Direction string          `json:"direction"`
		Message   json.RawMessage `json:"message"`
	}
	if err = json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 17 {
		t.Fatalf("Golden-Nachrichten: %d", len(rows))
	}
	for _, row := range rows {
		decode := DecodeClient
		if row.Direction == "server" {
			decode = DecodeServer
		}
		m, err := decode(row.Message)
		if err != nil {
			t.Fatalf("%s: %v", row.Message, err)
		}
		var w bytes.Buffer
		if err = WriteFrame(&w, m); err != nil {
			t.Fatal(err)
		}
		raw, err := ReadFrame(&w)
		if err != nil {
			t.Fatal(err)
		}
		var want, got any
		json.Unmarshal(row.Message, &want)
		json.Unmarshal(raw, &got)
		a, _ := json.Marshal(want)
		b, _ := json.Marshal(got)
		if !bytes.Equal(a, b) {
			t.Fatalf("Golden: %s != %s", a, b)
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }
func TestPipeSchreibgrenzen(t *testing.T) {
	if err := WriteFrame(shortWriter{}, Message{Type: "status"}); err != io.ErrShortWrite {
		t.Fatal(err)
	}
	if err := WriteFrame(io.Discard, Message{Type: "frage", Titel: "Demo", ID: "x", Text: strings.Repeat("x", 501), Optionen: []string{"a", "b"}, FristS: 5}); err == nil {
		t.Fatal("zu lange Frage")
	}
}

func TestPipeServerSchema(t *testing.T) {
	for _, s := range []string{
		`{"type":"fehler","code":"secret"}`, `{"type":"frage_weg","id":""}`,
		`{"type":"frage","id":"x","titel":"Demo","text":"ok","optionen":["a"],"frist_s":5}`,
		`{"type":"frage","id":"x","titel":"Demo","text":"ok","optionen":["a","b"],"frist_s":4}`,
		`{"type":"frage","id":"x","titel":"Demo","text":"ok","optionen":["a","b"],"frist_s":3601}`,
		`{"type":"zustand","zustand":"frei","test":false,"netz":true,"beschaeftigt":false,"setups":[]}`,
		`{"type":"zustand","zustand":"arbeitet","test":false,"netz":true,"beschaeftigt":false,"setups":[{"pin":"demo","setup":"x","label":"x","startbar":true,"input":{}}]}`,
		`{"type":"status"}`,
	} {
		if _, err := DecodeServer([]byte(s)); err == nil {
			t.Errorf("Server-Schema: %s", s)
		}
	}
	for _, m := range []Message{
		{Type: "frage", Titel: "Demo", ID: "x", Text: strings.Repeat("ü", 501), Optionen: []string{"a", "b"}, FristS: 5},
		{Type: "frage", Titel: "Demo", ID: "x", Text: "ok", Optionen: []string{"a", strings.Repeat("ü", 41)}, FristS: 5},
		{Type: "frage", Titel: "Demo", ID: "x", Text: "ok", Optionen: []string{"a", "b", "c", "d", "e"}, FristS: 5},
	} {
		if WriteFrame(io.Discard, m) == nil {
			t.Fatal("Fragegrenze")
		}
	}
}
