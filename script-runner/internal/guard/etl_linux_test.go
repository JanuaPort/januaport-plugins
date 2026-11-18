//go:build linux

package guard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// Läuft nur unter Linux (Build-Tag), lokal unter Windows also nicht: dort in
// einem Linux-Container ausführen (`go test ./internal/guard`).

func typesOf(t *testing.T, wire []byte) []string {
	t.Helper()
	var out []string
	r := bytes.NewReader(wire)
	for {
		p, err := frame.Read(r, MaxJobFrame)
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		typ, _ := frame.TypeOf(p)
		out = append(out, typ)
	}
}

// Vom Skript zum vermittler gehen etl_open und etl_next durch, sonst nichts
// Neues: ein Skript darf keine etl_chunk und kein etl_end an den vermittler
// fälschen (Richtung ist Teil des Vertrags).
func TestETLOpenNextForwardedToMediatorOnly(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	r := &runState{conn: a}
	var seen [][]byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			p, err := frame.Read(b, frame.MaxFromSandbox)
			if err != nil {
				return
			}
			seen = append(seen, p)
		}
	}()
	var bootstrap bytes.Buffer
	_ = frame.Write(&bootstrap, frame.ETLOpen{Type: frame.TypeETLOpen, ID: 1, Integration: "erp", QueryID: "q", Params: json.RawMessage(`{"a":1}`)})
	_ = frame.Write(&bootstrap, frame.ETLNext{Type: frame.TypeETLNext, ID: 1})
	_ = frame.Write(&bootstrap, frame.ETLChunk{Type: frame.TypeETLChunk, ID: 1, DataB64: "AAAA"})
	_ = frame.Write(&bootstrap, frame.ETLEnd{Type: frame.TypeETLEnd, ID: 1, Status: frame.ETLStatusEOF})
	_ = frame.Write(&bootstrap, frame.ToolResult{Type: frame.TypeToolResult, ID: 1, Result: json.RawMessage(`{}`)})
	_ = frame.Write(&bootstrap, frame.ETLNext{Type: frame.TypeETLNext, ID: 1, Close: true})
	r.pumpBootstrap(&bootstrap)
	_ = a.Close()
	<-done
	var got []string
	for _, p := range seen {
		typ, _ := frame.TypeOf(p)
		got = append(got, typ)
	}
	want := []string{frame.TypeETLOpen, frame.TypeETLNext, frame.TypeETLNext}
	if len(got) != len(want) {
		t.Fatalf("durchgereicht: %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("durchgereicht: %v, want %v", got, want)
		}
	}
	// Der Rahmen wird unverändert durchgereicht (kein Neuschreiben der Nutzlast).
	var open frame.ETLOpen
	_ = json.Unmarshal(seen[0], &open)
	if open.Integration != "erp" || string(open.Params) != `{"a":1}` {
		t.Fatalf("etl_open verändert: %s", seen[0])
	}
}

// Vom vermittler zum Skript gehen etl_chunk und etl_end durch (und weiter
// tool_result), kein job, kein hello und keine Rückwärts-Typen. Das volle Stück
// (256 KiB roh) passt durch den Leser des Wächters.
func TestETLChunkEndForwardedToBootstrapOnly(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	r := &runState{conn: b}
	raw := bytes.Repeat([]byte{0xfe}, frame.MaxETLChunkRaw)
	full := frame.ETLChunk{Type: frame.TypeETLChunk, ID: 1, DataB64: base64.StdEncoding.EncodeToString(raw)}
	go func() {
		_ = frame.Write(a, frame.ETLOpen{Type: frame.TypeETLOpen, ID: 1})
		_ = frame.Write(a, frame.ETLNext{Type: frame.TypeETLNext, ID: 1})
		_ = frame.Write(a, full)
		_ = frame.Write(a, frame.ETLEnd{Type: frame.TypeETLEnd, ID: 1, Status: frame.ETLStatusEOF})
		_ = frame.Write(a, frame.ToolResult{Type: frame.TypeToolResult, ID: 2, Result: json.RawMessage(`{}`)})
		_ = frame.Write(a, frame.Hello{Type: frame.TypeHello})
		_ = a.Close()
	}()
	var out bytes.Buffer
	r.pumpMediator(&out)
	got := typesOf(t, out.Bytes())
	want := []string{frame.TypeETLChunk, frame.TypeETLEnd, frame.TypeToolResult}
	if len(got) != len(want) {
		t.Fatalf("durchgereicht: %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("durchgereicht: %v, want %v", got, want)
		}
	}
	p, err := frame.Read(bytes.NewReader(out.Bytes()), MaxJobFrame)
	if err != nil {
		t.Fatal(err)
	}
	var c frame.ETLChunk
	_ = json.Unmarshal(p, &c)
	back, _ := base64.StdEncoding.DecodeString(c.DataB64)
	if !bytes.Equal(back, raw) {
		t.Fatal("volles Stück beim Durchreichen verändert")
	}
}
