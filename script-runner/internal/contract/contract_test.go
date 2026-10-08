package contract

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -update schreibt die Golden-Dateien neu. Sie sind Vertragsgegenstand
// (Vertrag §11): P3 kopiert sie unverändert ins Gateway-Repo. Eine Änderung
// hier ist eine Vertragsänderung und braucht eine neue Fassung im Ticket.
var update = flag.Bool("update", false, "Golden-Dateien neu schreiben")

const goldenDir = "../../contract/golden"

func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join(goldenDir, name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Golden-Datei fehlt: %v", err)
	}
	if !bytes.Equal(canonical(t, want), canonical(t, got)) {
		t.Fatalf("%s weicht ab:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// canonical vergleicht inhaltlich: Schlüsselreihenfolge und Einrückung sind
// kein Vertragsgegenstand, Namen, Werte und Typen schon.
func canonical(t *testing.T, b []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("kein JSON: %v", err)
	}
	out, _ := json.Marshal(v)
	return out
}

// Vertrag §4: jede Ende-Klasse als Golden-JSON.
func TestEndClassesGolden(t *testing.T) {
	tests := []struct {
		file   string
		run    Run
		output string
	}{
		{"end_ok_structured.json", Run{End: EndOK, BytesOut: 27, ToolCalls: 3, Isolation: IsolationGVisor}, `{"checked":12,"open":["A-1"]}`},
		{"end_ok_text.json", Run{End: EndOK, BytesOut: 12, Isolation: IsolationStandard}, "12 geprüft.\n"},
		{"end_error_exception.json", Run{End: EndError, Detail: DetailException, ErrorType: "ValueError", ErrorAt: "invoice-check/main.py:42", ToolCalls: 1, Isolation: IsolationGVisor}, ""},
		{"end_error_exit.json", Run{End: EndError, Detail: DetailExit, Isolation: IsolationGVisor}, ""},
		{"end_limit_output.json", Run{End: EndLimit, Detail: DetailOutput, Isolation: IsolationGVisor}, ""},
		{"end_limit_frame.json", Run{End: EndLimit, Detail: DetailFrame, Isolation: IsolationGVisor}, ""},
		{"end_limit_tool_calls.json", Run{End: EndLimit, Detail: DetailToolCalls, ToolCalls: 50, Isolation: IsolationGVisor}, ""},
		{"end_limit_memory.json", Run{End: EndLimit, Detail: DetailMemory, Isolation: IsolationGVisor}, ""},
		{"end_limit_sandbox_lost.json", Run{End: EndLimit, Detail: DetailSandboxLost, Isolation: IsolationGVisor}, ""},
		{"end_timeout.json", Run{End: EndTimeout, Isolation: IsolationGVisor}, ""},
		{"end_busy.json", Run{End: EndBusy, Isolation: IsolationGVisor}, ""},
		{"end_refused_pin.json", Run{End: EndRefused, Detail: DetailPin, Isolation: IsolationGVisor}, ""},
		{"end_refused_commit.json", Run{End: EndRefused, Detail: DetailCommit, Isolation: IsolationGVisor}, ""},
		{"end_refused_state.json", Run{End: EndRefused, Detail: DetailState, Isolation: IsolationGVisor}, ""},
		{"end_refused_input.json", Run{End: EndRefused, Detail: DetailInput, Isolation: IsolationGVisor}, ""},
		{"end_internal.json", Run{End: EndInternal, Isolation: IsolationGVisor}, ""},
	}
	seen := map[string]bool{}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			golden(t, tt.file, Result(tt.run, []byte(tt.output)))
		})
		seen[tt.run.End+"/"+tt.run.Detail] = true
	}
	// Jede Klasse und jedes detail aus §4 ist abgedeckt.
	for _, want := range []string{"ok/", "error/exit", "error/exception", "limit/output", "limit/frame",
		"limit/tool_calls", "limit/memory", "limit/sandbox_lost", "timeout/", "busy/",
		"refused/pin", "refused/commit", "refused/state", "refused/input", "internal/"} {
		if !seen[want] {
			t.Errorf("Ende-Klasse %s ohne Golden-Datei", want)
		}
	}
}

func TestResultFlags(t *testing.T) {
	for _, end := range []string{EndError, EndLimit, EndTimeout, EndBusy, EndRefused, EndInternal} {
		if r := Result(Run{End: end}, nil); !r.IsError {
			t.Errorf("%s: isError = false", end)
		}
	}
	ok := Result(Run{End: EndOK}, []byte(`[1,2]`))
	if ok.IsError || ok.StructuredContent != nil {
		t.Fatalf("ok mit Array: %+v", ok)
	}
}

// M6: Nur Ausgabe bei ok. Bei jedem anderen Ende sieht die KI einen festen
// Satz, auch wenn Bytes gelesen wurden.
func TestResultNeverLeaksOutputOnError(t *testing.T) {
	for _, end := range []string{EndError, EndLimit, EndTimeout} {
		r := Result(Run{End: end}, []byte("KANARIE-4711"))
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "KANARIE") {
			t.Errorf("%s: Ausgabe im Ergebnis: %s", end, b)
		}
	}
}

func TestSanitizeErrorType(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ValueError", "ValueError"},
		{"json.decoder.JSONDecodeError", "json.decoder.JSONDecodeError"},
		{"_Private", "_Private"},
		{"", "Exception"},
		{"1Bad", "Exception"},
		{"Value Error", "Exception"},
		{"KANARIE: geheime Daten", "Exception"},
		{strings.Repeat("A", 101), "A" + strings.Repeat("A", 100)},
		{strings.Repeat("A", 102), "Exception"},
	}
	for _, tt := range tests {
		if got := SanitizeErrorType(tt.in); got != tt.want {
			t.Errorf("SanitizeErrorType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestToolsListGolden(t *testing.T) {
	commit := strings.Repeat("3", 40)
	listed := []ListedPin{
		{
			Name: "invoice_check", Commit: commit, State: StateReady,
			Script: &ScriptMeta{
				Path: "invoice-check", DeclaredTools: []string{"kartei_find", "memory_append"},
				TimeoutS: 20, ManifestSHA: strings.Repeat("4", 40),
			},
			Description: "Prüft offene Rechnungen gegen die Kartei.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"limit": map[string]any{"type": "integer"}}},
		},
		{Name: "waiting", Commit: strings.Repeat("5", 64), State: StatePending},
		{Name: "broken", Commit: strings.Repeat("6", 40), State: StateInvalid, Reason: ReasonSymlink},
	}
	golden(t, "tools_list.json", ToolsList(listed, IsolationGVisor))
}

func TestToolsListInvalidIsolationListsNoTool(t *testing.T) {
	listed := []ListedPin{{Name: "a", Commit: strings.Repeat("3", 40), State: StateReady,
		Script:      &ScriptMeta{Path: "a", TimeoutS: 1, ManifestSHA: strings.Repeat("4", 40)},
		InputSchema: map[string]any{"type": "object"}}}
	res := ToolsList(listed, IsolationInvalid)
	if len(res.Tools) != 0 {
		t.Fatalf("Werkzeuge trotz isolation invalid: %d", len(res.Tools))
	}
	golden(t, "tools_list_isolation_invalid.json", res)
}

// Vertrag §3: die Header, die das Gateway setzt. Die Golden-Datei ist das
// Muster; der Prüfer muss sie annehmen.
func TestCallHeadersGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(goldenDir, "call_headers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hs map[string]string
	if err := json.Unmarshal(raw, &hs); err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	for k, v := range hs {
		h.Set(k, v)
	}
	got, err := ParseRunHeaders(h)
	if err != nil {
		t.Fatalf("Golden-Header abgewiesen: %v", err)
	}
	if got.PinCommit != hs[HeaderPinCommit] || got.RunToken != hs[HeaderRunToken] || got.RunID != hs[HeaderRunID] {
		t.Fatalf("ParseRunHeaders = %+v", got)
	}
}

func TestParseRunHeadersRejects(t *testing.T) {
	good := func() http.Header {
		h := http.Header{}
		h.Set(HeaderRunToken, "jnptrun_"+strings.Repeat("A", 43))
		h.Set(HeaderRunID, "0b1e4c8a-6f2d-4b7e-9a31-2c5d7e8f9a10")
		h.Set(HeaderPinCommit, strings.Repeat("3", 40))
		return h
	}
	tests := []struct {
		name  string
		mod   func(http.Header)
		isErr bool
	}{
		{"gut", func(http.Header) {}, false},
		{"Token fehlt", func(h http.Header) { h.Del(HeaderRunToken) }, true},
		{"Token falsches Präfix", func(h http.Header) { h.Set(HeaderRunToken, "jnpt_"+strings.Repeat("A", 43)) }, true},
		{"Token zu kurz", func(h http.Header) { h.Set(HeaderRunToken, "jnptrun_"+strings.Repeat("A", 42)) }, true},
		{"Token mit Zeichen außerhalb base64url", func(h http.Header) { h.Set(HeaderRunToken, "jnptrun_"+strings.Repeat("A", 42)+"+") }, true},
		{"Run-Id keine UUIDv4", func(h http.Header) { h.Set(HeaderRunID, "run-1") }, true},
		{"Pin-Commit fehlt ist kein Protokollfehler", func(h http.Header) { h.Del(HeaderPinCommit) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := good()
			tt.mod(h)
			_, err := ParseRunHeaders(h)
			if (err != nil) != tt.isErr {
				t.Fatalf("err = %v, want Fehler=%v", err, tt.isErr)
			}
		})
	}
}
