package mediator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// Vertrag §2 auf dem Draht: tools/list, wie das Gateway es sieht, entspricht
// der Golden-Datei (Commit und Manifest-Hash auf die Platzhalter gesetzt).
func TestToolsListWireMatchesGolden(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins([]pinRow{
		{Name: "invoice_check", Path: "invoice-check", Commit: e.fx.commit},
		{Name: "waiting", Path: "waiting", Commit: strings.Repeat("5", 64)},
		{Name: "broken", Path: "broken", Commit: e.fx.commit},
	})
	_ = dialGuard(t, e.sock, "i1", 2)
	// isolation kommt aus dem hello; der Fake meldet „standard“, die
	// Golden-Datei zeigt „gvisor“ — beides ist ein gültiger Wert.
	e.waitReady("invoice_check")
	res := e.listTools()
	raw, _ := json.Marshal(res)
	s := strings.ReplaceAll(string(raw), e.fx.commit, strings.Repeat("3", 40))
	s = strings.ReplaceAll(s, e.fx.blob("invoice-check/jnpt-script.yaml"), strings.Repeat("4", 40))
	s = strings.ReplaceAll(s, `"isolation":"standard"`, `"isolation":"gvisor"`)
	// Protokollfelder des SDK (resultType, serverInfo) sind nicht Teil des
	// Vertrags. Die Fixture hat nur einen Commit; die Golden-Datei zeigt für
	// „broken“ einen eigenen.
	var wire map[string]any
	_ = json.Unmarshal([]byte(s), &wire)
	delete(wire, "resultType")
	meta := wire["_meta"].(map[string]any)
	delete(meta, "io.modelcontextprotocol/serverInfo")
	meta["januaport.ai/runner"].(map[string]any)["pins"].([]any)[2].(map[string]any)["commit"] = strings.Repeat("6", 40)
	b, _ := json.Marshal(wire)
	s = string(b)
	want, err := os.ReadFile("../../contract/golden/tools_list.json")
	if err != nil {
		t.Fatal(err)
	}
	if canon(t, []byte(s)) != canon(t, want) {
		t.Fatalf("tools/list weicht von der Golden-Datei ab:\n%s\n%s", s, want)
	}
}

func canon(t *testing.T, b []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// R13: ohne das Credential 401, auch für tools/list.
func TestAuthRequiresRuntimeKey(t *testing.T) {
	e := newEnv(t, nil)
	for _, auth := range []string{"", "Bearer wrong", "Bearer " + testKey + "x", "Basic " + testKey, testKey} {
		req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Authorization %q: Status %d, want 401", auth, resp.StatusCode)
		}
	}
}

func TestAuthFailsClosedWithoutKeyFile(t *testing.T) {
	e := newEnv(t, nil)
	_ = os.Remove(filepath.Join(e.keys, "runtime-key"))
	time.Sleep(100 * time.Millisecond)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer ")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Status %d, want 401", resp.StatusCode)
	}
}

func TestHealthOnlyOwnHandlerAndNoMCP(t *testing.T) {
	e := newEnv(t, nil)
	hs := httptest.NewServer(e.m.HealthHandler())
	defer hs.Close()
	_ = dialGuard(t, e.sock, "i1", 2)
	deadline := time.Now().Add(3 * time.Second)
	status := 0
	for time.Now().Before(deadline) {
		resp, err := http.Get(hs.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		status = resp.StatusCode
		if status == http.StatusOK {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != http.StatusOK {
		t.Fatalf("/healthz = %d", status)
	}
	resp, _ := http.Post(hs.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/mcp auf dem Health-Listener = %d, want 404", resp.StatusCode)
	}
}

// R6: ohne Pin keine Ausführung.
func TestNoPinNoRun(t *testing.T) {
	e := newEnv(t, nil)
	g := dialGuard(t, e.sock, "i1", 2)
	res := e.mustCall("quick", map[string]any{})
	assertEnd(t, res, contract.EndRefused, contract.DetailPin)
	if len(e.listTools().Tools) != 0 {
		t.Fatal("Werkzeuge ohne pins.json")
	}
	_ = g.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := frame.Read(g.conn, 1<<20); err == nil {
		t.Fatal("Wächter bekam einen job ohne Pin")
	}
}

func TestRefusedCommitAndState(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(append(e.quickPin(), pinRow{Name: "waiting", Path: "waiting", Commit: strings.Repeat("5", 40)}))
	_ = dialGuard(t, e.sock, "i1", 2)
	e.waitReady("quick")

	res, err := e.call("quick", map[string]any{}, strings.Repeat("9", 40))
	if err != nil {
		t.Fatal(err)
	}
	assertEnd(t, res, contract.EndRefused, contract.DetailCommit)

	res, err = e.call("quick", map[string]any{}, "")
	if err != nil {
		t.Fatal(err)
	}
	assertEnd(t, res, contract.EndRefused, contract.DetailCommit)

	res, err = e.call("waiting", map[string]any{}, strings.Repeat("5", 40))
	if err != nil {
		t.Fatal(err)
	}
	assertEnd(t, res, contract.EndRefused, contract.DetailState)
}

func TestRefusedInputTooLarge(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	_ = dialGuard(t, e.sock, "i1", 2)
	e.waitReady("quick")
	res := e.mustCall("quick", map[string]any{"blob": strings.Repeat("x", 256<<10)})
	assertEnd(t, res, contract.EndRefused, contract.DetailInput)
}

func TestMissingRunTokenIsProtocolError(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	_ = dialGuard(t, e.sock, "i1", 2)
	e.waitReady("quick")
	h := runHeaders(e.fx.commit)
	h.Del(contract.HeaderRunToken)
	s, err := e.session(h)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "quick", Arguments: map[string]any{}}); err == nil {
		t.Fatal("kein JSON-RPC-Fehler ohne Lauf-Token")
	}
}

func TestBusyWithoutSandbox(t *testing.T) {
	e := newEnv(t, nil, withBusyWait(200*time.Millisecond))
	e.writePins(e.quickPin())
	g := dialGuard(t, e.sock, "i1", 2)
	e.waitReady("quick")
	g.conn.Close() // Instanz weg, keine neue da
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	res := e.mustCall("quick", map[string]any{})
	assertEnd(t, res, contract.EndBusy, "")
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("busy nach %v, der vermittler hat nicht gewartet", d)
	}
}

// Der Normalfall mit einem inneren Werkzeugaufruf (Weg B).
func TestRunOKWithToolCall(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	g := dialGuard(t, e.sock, "i1", 2)
	e.waitReady("invoice_check")
	done := make(chan frame.Job, 1)
	go func() {
		j := g.job()
		done <- j
		g.send(frame.ToolCall{Type: frame.TypeToolCall, ID: 1, Name: "kartei_find", Arguments: json.RawMessage(`{"q":"offen"}`)})
		typ, b, err := g.read()
		if err != nil || typ != frame.TypeToolResult {
			t.Errorf("tool_result erwartet: %q %v", typ, err)
		}
		var tr frame.ToolResult
		_ = json.Unmarshal(b, &tr)
		if tr.ID != 1 || !strings.Contains(string(tr.Result), `"found":2`) {
			t.Errorf("tool_result = %s", b)
		}
		g.send(frame.Output{Type: frame.TypeOutput, Data: `{"checked":`})
		g.send(frame.Output{Type: frame.TypeOutput, Data: `12}`})
		g.exited(0)
		g.conn.Close()
	}()
	res := e.mustCall("invoice_check", map[string]any{"limit": 5})
	m := assertEnd(t, res, contract.EndOK, "")
	if res.IsError {
		t.Fatal("isError bei ok")
	}
	if m["tool_calls"] != float64(1) || m["bytes_out"] != float64(len(`{"checked":12}`)) || m["isolation"] != contract.IsolationStandard {
		t.Fatalf("_meta = %v", m)
	}
	sc, _ := json.Marshal(res.StructuredContent)
	if string(sc) != `{"checked":12}` {
		t.Fatalf("structuredContent = %s", sc)
	}
	j := <-done
	if j.Entry != "main.py" || string(j.Input) != `{"limit":5}` {
		t.Fatalf("job = %+v", j)
	}
	files := map[string]string{}
	for _, f := range j.Files {
		d, _ := base64.StdEncoding.DecodeString(f.DataB64)
		files[f.Path] = string(d)
	}
	if files["main.py"] != "print('hi')\n" || files["lib/helper.py"] != "X = 1\n" || files["jnpt-script.yaml"] == "" || len(files) != 3 {
		t.Fatalf("job-Dateien = %v", files)
	}
	auth := e.gw.authHeaders()
	if len(auth) == 0 {
		t.Fatal("Gateway nie gerufen")
	}
	for _, a := range auth {
		if a != "Bearer "+testRunToken {
			t.Fatalf("Weg B mit Authorization %q", a)
		}
	}
}

// M1 zweite Linie: nicht deklariertes Werkzeug erreicht das Gateway nie.
func TestUndeclaredToolNeverReachesGateway(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) {
		g.send(frame.ToolCall{Type: frame.TypeToolCall, ID: 7, Name: "memory_delete", Arguments: json.RawMessage(`{}`)})
		typ, b, _ := g.read()
		if typ != frame.TypeToolResult || !strings.Contains(string(b), `"isError":true`) {
			t.Errorf("tool_result = %s", b)
		}
		g.exited(0)
	})
	e.waitReady("quick")
	res := e.mustCall("quick", map[string]any{})
	<-done
	assertEnd(t, res, contract.EndOK, "")
	if n := e.gw.calls.Load(); n != 0 {
		t.Fatalf("Gateway %d-mal gerufen", n)
	}
}

// V1 (SEC, Muss): eine zweite Verbindung während des Laufs.
func TestV1SecondConnectionLosesRunAndNextJobGoesToFreshInstance(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	g1 := dialGuard(t, e.sock, "instance-1", 2)
	e.waitReady("quick")

	secondClosed := make(chan bool, 1)
	go func() {
		g1.job()
		// Das „Skript“ öffnet eine zweite Verbindung und gibt sich als
		// frische Sandbox aus.
		c, err := net.Dial("unix", e.sock)
		if err != nil {
			secondClosed <- true
			return
		}
		defer c.Close()
		_ = frame.Write(c, frame.Hello{Type: frame.TypeHello, Instance: "attacker", Isolation: contract.IsolationStandard, Seccomp: 2})
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = frame.Read(c, 64<<20)
		secondClosed <- err != nil && !isTimeout(err)
		// Der echte Wächter sieht das Ende und schließt erst danach.
		_ = g1.waitEOF()
		g1.conn.Close()
	}()
	res := e.mustCall("quick", map[string]any{})
	assertEnd(t, res, contract.EndLimit, contract.DetailSandboxLost)
	if !<-secondClosed {
		t.Fatal("zweite Verbindung nicht sofort geschlossen")
	}

	got := make(chan string, 1)
	go func() {
		g3 := dialGuard(t, e.sock, "instance-3", 2)
		g3.job()
		got <- g3.instance
		g3.exited(0)
		g3.conn.Close()
	}()
	res = e.mustCall("quick", map[string]any{})
	assertEnd(t, res, contract.EndOK, "")
	if inst := <-got; inst != "instance-3" {
		t.Fatalf("nächster job an %q", inst)
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

// V1: eine neue Verbindung erst nach EOF der vorigen.
func TestNewConnectionOnlyAfterEOF(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	g1 := dialGuard(t, e.sock, "instance-1", 2)
	e.waitReady("quick")
	go func() {
		g1.job()
		g1.exited(0) // Verbindung bleibt offen: Prozessbaum „noch nicht tot“
	}()
	assertEnd(t, e.mustCall("quick", map[string]any{}), contract.EndOK, "")

	early, err := net.Dial("unix", e.sock)
	if err != nil {
		t.Fatal(err)
	}
	_ = frame.Write(early, frame.Hello{Type: frame.TypeHello, Instance: "early", Isolation: contract.IsolationStandard, Seccomp: 2})
	_ = early.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := frame.Read(early, 1<<20); err == nil || isTimeout(err) {
		t.Fatalf("Verbindung vor EOF nicht geschlossen: %v", err)
	}
	early.Close()

	g1.conn.Close()
	got := make(chan string, 1)
	go func() {
		g2 := dialGuard(t, e.sock, "instance-2", 2)
		g2.job()
		got <- g2.instance
		g2.exited(0)
		g2.conn.Close()
	}()
	assertEnd(t, e.mustCall("quick", map[string]any{}), contract.EndOK, "")
	if <-got != "instance-2" {
		t.Fatal("job nicht an die neue Instanz")
	}
}

// M4 (SEC-Zeile 1): Rahmen nach dem Ende → verworfen, kein Werkzeugaufruf.
func TestFramesAfterExitedAreDiscarded(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) {
		g.exited(0)
		g.send(frame.ToolCall{Type: frame.TypeToolCall, ID: 1, Name: "kartei_find", Arguments: json.RawMessage(`{}`)})
		g.send(frame.Output{Type: frame.TypeOutput, Data: "spät"})
		time.Sleep(200 * time.Millisecond)
	})
	e.waitReady("quick")
	res := e.mustCall("quick", map[string]any{})
	<-done
	assertEnd(t, res, contract.EndOK, "")
	if text := res.Content[0].(*mcp.TextContent).Text; text != "" {
		t.Fatalf("Ausgabe nach exited übernommen: %q", text)
	}
	time.Sleep(100 * time.Millisecond)
	if n := e.gw.calls.Load(); n != 0 {
		t.Fatalf("Werkzeugaufruf nach exited: %d", n)
	}
}

// M4 (SEC-Zeile 1): Rahmen nach der Wandzeit → verworfen, kein Aufruf.
func TestFramesAfterTimeoutAreDiscarded(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) {
		g.send(frame.Output{Type: frame.TypeOutput, Data: "ok"})
		time.Sleep(1500 * time.Millisecond) // quick: timeout_s 1
		g.send(frame.ToolCall{Type: frame.TypeToolCall, ID: 1, Name: "kartei_find", Arguments: json.RawMessage(`{}`)})
		g.exited(0)
		_ = g.waitEOF()
	})
	e.waitReady("quick")
	start := time.Now()
	res := e.mustCall("quick", map[string]any{})
	elapsed := time.Since(start)
	assertEnd(t, res, contract.EndTimeout, "")
	if elapsed < 900*time.Millisecond || elapsed > 1400*time.Millisecond {
		t.Fatalf("timeout nach %v, want ≈ 1 s (eigene Uhr)", elapsed)
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), `"text":"ok"`) {
		t.Fatalf("Ausgabe trotz timeout: %s", b)
	}
	<-done
	if n := e.gw.calls.Load(); n != 0 {
		t.Fatalf("Werkzeugaufruf nach der Wandzeit: %d", n)
	}
}

// M4 (SEC-Zeile 1): übergroßer Rahmen → limit/frame, der vermittler lebt.
func TestOversizedFrameLimitAndMediatorSurvives(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) {
		big := frame.Output{Type: frame.TypeOutput, Data: strings.Repeat("x", frame.MaxFromSandbox)}
		g.send(big)
		_ = g.waitEOF()
	})
	e.waitReady("quick")
	assertEnd(t, e.mustCall("quick", map[string]any{}), contract.EndLimit, contract.DetailFrame)
	<-done

	done = e.oneRun("i2", func(g *fakeGuard, _ frame.Job) { g.exited(0) })
	assertEnd(t, e.mustCall("quick", map[string]any{}), contract.EndOK, "")
	<-done
}

func TestOutputLimit(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) {
		chunk := strings.Repeat("y", 512<<10)
		for i := 0; i < 3; i++ {
			g.send(frame.Output{Type: frame.TypeOutput, Data: chunk})
		}
		g.exited(0)
		_ = g.waitEOF()
	})
	e.waitReady("quick")
	assertEnd(t, e.mustCall("quick", map[string]any{}), contract.EndLimit, contract.DetailOutput)
	<-done
}

func TestToolCallCap(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) {
		for i := 1; i <= 51; i++ {
			g.send(frame.ToolCall{Type: frame.TypeToolCall, ID: int64(i), Name: "kartei_find", Arguments: json.RawMessage(`{}`)})
			if _, _, err := g.read(); err != nil {
				return
			}
		}
		g.exited(0)
	})
	e.waitReady("quick")
	m := assertEnd(t, e.mustCall("quick", map[string]any{}), contract.EndLimit, contract.DetailToolCalls)
	<-done
	if n := e.gw.calls.Load(); n != 50 {
		t.Fatalf("Gateway %d-mal gerufen, want 50", n)
	}
	if m["tool_calls"] != float64(50) {
		t.Fatalf("tool_calls = %v", m["tool_calls"])
	}
}

func TestSandboxLostWithoutExited(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) {
		g.send(frame.Output{Type: frame.TypeOutput, Data: "halb"})
	})
	e.waitReady("quick")
	assertEnd(t, e.mustCall("quick", map[string]any{}), contract.EndLimit, contract.DetailSandboxLost)
	<-done
}

func TestExitClasses(t *testing.T) {
	tests := []struct {
		name   string
		exited frame.Exited
		end    string
		detail string
		at     string
		typ    string
	}{
		{"Exit 3", frame.Exited{Code: 3}, contract.EndError, contract.DetailExit, "", ""},
		{"Ausnahme", frame.Exited{Code: 1, ErrorType: "ValueError", ErrorAt: "main.py:3"}, contract.EndError, contract.DetailException, "quick/main.py:3", "ValueError"},
		{"Ausnahme außerhalb des Pins", frame.Exited{Code: 1, ErrorType: "KeyError", ErrorAt: "../../usr/lib/python3.12/json/__init__.py:1"}, contract.EndError, contract.DetailException, "", "KeyError"},
		{"Ausnahme unbekannte Datei", frame.Exited{Code: 1, ErrorType: "KeyError", ErrorAt: "nope.py:1"}, contract.EndError, contract.DetailException, "", "KeyError"},
		{"Zeile keine Zahl", frame.Exited{Code: 1, ErrorType: "KeyError", ErrorAt: "main.py:x"}, contract.EndError, contract.DetailException, "", "KeyError"},
		{"SIGKILL ohne Zutun = OOM", frame.Exited{Code: -1, Signal: 9}, contract.EndLimit, contract.DetailMemory, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.writePins(e.quickPin())
			ex := tt.exited
			ex.Type = frame.TypeExited
			done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) { g.send(ex) })
			e.waitReady("quick")
			m := assertEnd(t, e.mustCall("quick", map[string]any{}), tt.end, tt.detail)
			<-done
			gotAt, _ := m["error_at"].(string)
			gotType, _ := m["error_type"].(string)
			if gotAt != tt.at || gotType != tt.typ {
				t.Fatalf("error_type/error_at = %q/%q, want %q/%q", gotType, gotAt, tt.typ, tt.at)
			}
		})
	}
}

// M6: Eine Kanarie im Fehler erscheint weder in der Antwort noch im Log.
func TestM6CanaryNeverInResultOrLog(t *testing.T) {
	const canary = "KANARIE-7f3a"
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) {
		g.send(frame.Output{Type: frame.TypeOutput, Data: "Traceback " + canary})
		g.send(frame.Exited{Type: frame.TypeExited, Code: 1, ErrorType: "ValueError: " + canary, ErrorAt: canary + ":1"})
	})
	e.waitReady("quick")
	res := e.mustCall("quick", map[string]any{"secret": canary})
	<-done
	m := assertEnd(t, res, contract.EndError, contract.DetailException)
	if m["error_type"] != "Exception" {
		t.Fatalf("error_type = %v", m["error_type"])
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), canary) {
		t.Fatalf("Kanarie in der Antwort: %s", b)
	}
	if strings.Contains(e.logs.String(), canary) {
		t.Fatalf("Kanarie im Log:\n%s", e.logs.String())
	}
	if !strings.Contains(e.logs.String(), "end=error") {
		t.Fatalf("Log ohne Zählzeile:\n%s", e.logs.String())
	}
}

// S2 fail-closed: meldet der Wächter seccomp ≠ 2, bedient der vermittler nicht.
func TestSeccompNot2FailsClosed(t *testing.T) {
	e := newEnv(t, nil, withBusyWait(300*time.Millisecond))
	e.writePins(e.quickPin())
	g := dialGuard(t, e.sock, "i1", 0)
	deadline := time.Now().Add(3 * time.Second)
	var res *mcp.ListToolsResult
	for time.Now().Before(deadline) {
		res = e.listTools()
		if strings.Contains(mustJSON(res.Meta), `"isolation":"invalid"`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(mustJSON(res.Meta), `"isolation":"invalid"`) {
		t.Fatalf("isolation nicht invalid: %s", mustJSON(res.Meta))
	}
	if len(res.Tools) != 0 {
		t.Fatalf("%d Werkzeuge trotz seccomp 0", len(res.Tools))
	}
	call := e.mustCall("quick", map[string]any{})
	if m := runMeta(t, call); m["end"] == contract.EndOK {
		t.Fatalf("Lauf trotz seccomp 0: %v", m)
	}
	_ = g.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := frame.Read(g.conn, 1<<20); err == nil {
		t.Fatal("job an eine Sandbox mit seccomp 0")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Weg D: der vermittler meldet dem abholer repo_url und die gewünschten
// Commits; ein Fehlschlag des abholers wird zu invalid/fetch_failed.
func TestWantedAndFetchFailed(t *testing.T) {
	e := newEnv(t, nil)
	missing := strings.Repeat("7", 40)
	e.writePins([]pinRow{{Name: "quick", Path: "quick", Commit: e.fx.commit}, {Name: "later", Path: "later", Commit: missing}})
	_ = dialGuard(t, e.sock, "i1", 2)
	e.waitReady("quick")
	for _, sha := range []string{e.fx.commit, missing} {
		if _, err := os.Stat(filepath.Join(e.fx.store, "wanted", sha)); err != nil {
			t.Errorf("wanted/%s fehlt: %v", sha, err)
		}
	}
	url, err := os.ReadFile(filepath.Join(e.fx.store, "repo_url"))
	if err != nil || strings.TrimSpace(string(url)) != "ssh://git@git.example.com/org/scripts.git" {
		t.Fatalf("repo_url = %q, %v", url, err)
	}
	if !strings.Contains(mustJSON(e.listTools().Meta), `"state":"pending"`) {
		t.Fatal("fehlender Commit nicht pending")
	}
	_ = os.MkdirAll(filepath.Join(e.fx.store, "failed"), 0o755)
	_ = os.WriteFile(filepath.Join(e.fx.store, "failed", missing), nil, 0o644)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(mustJSON(e.listTools().Meta), `"reason":"fetch_failed"`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(mustJSON(e.listTools().Meta), `"reason":"fetch_failed"`) {
		t.Fatal("fetch_failed nicht gemeldet")
	}
	// Ein entfernter Pin verschwindet aus wanted/.
	e.writePins([]pinRow{{Name: "quick", Path: "quick", Commit: e.fx.commit}})
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(e.fx.store, "wanted", missing)); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("wanted/ nicht aufgeräumt")
}

// M5 im Lauf: der Symlink-Pin ist invalid und wird nie ausgeführt.
func TestSymlinkPinInvalidAndRefused(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(append(e.quickPin(), pinRow{Name: "broken", Path: "broken", Commit: e.fx.commit}))
	_ = dialGuard(t, e.sock, "i1", 2)
	e.waitReady("quick")
	if !strings.Contains(mustJSON(e.listTools().Meta), `"reason":"symlink"`) {
		t.Fatalf("broken nicht als symlink gemeldet: %s", mustJSON(e.listTools().Meta))
	}
	assertEnd(t, e.mustCall("broken", map[string]any{}), contract.EndRefused, contract.DetailState)
}
