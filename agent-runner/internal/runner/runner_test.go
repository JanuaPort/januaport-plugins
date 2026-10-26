package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const secret = "CANARY-content-1099"

type fixture struct {
	Arguments Arguments         `json:"arguments"`
	Headers   map[string]string `json:"headers"`
	Tool      string            `json:"tool"`
}

func golden(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("../../contract/golden/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func requestFixture(t *testing.T) fixture {
	t.Helper()
	var f fixture
	if e := json.Unmarshal(golden(t, "run_agent_request.json"), &f); e != nil {
		t.Fatal(e)
	}
	return f
}
func equalJSON(t *testing.T, a, b []byte) {
	t.Helper()
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || !reflect.DeepEqual(x, y) {
		t.Fatalf("JSON mismatch\n%s\n%s", a, b)
	}
}

func TestCoreGoldenSHA256(t *testing.T) {
	manifest := sha256.Sum256(golden(t, "SHA256SUMS"))
	if hex.EncodeToString(manifest[:]) != "331229d8114067fd401808e7e338f6d0a13d6944927e292a6139a33cf459a5dd" {
		t.Fatal("core manifest changed")
	}
	sums := strings.Fields(string(golden(t, "SHA256SUMS")))
	if len(sums) != 20 {
		t.Fatal("seven original and three OpenAI fixtures required")
	}
	for i := 0; i < len(sums); i += 2 {
		sum := sha256.Sum256(golden(t, strings.TrimPrefix(sums[i+1], "*")))
		if hex.EncodeToString(sum[:]) != sums[i] {
			t.Fatal(sums[i+1])
		}
	}
}
func TestRunRequestGoldenRoundTrip(t *testing.T) {
	b, _ := json.Marshal(requestFixture(t))
	equalJSON(t, b, golden(t, "run_agent_request.json"))
}

type fakeCore struct {
	mu       sync.Mutex
	requests []map[string]any
	calls    int
	model    func(http.ResponseWriter, *http.Request, int)
	tool     json.RawMessage
	list     json.RawMessage
	before   func(http.ResponseWriter, *http.Request) bool
}

func (f *fakeCore) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Header.Get("Authorization") != "Bearer <lauf-token>" {
		t.Error("wrong bearer")
	}
	w.Header().Set("Content-Type", "application/json")
	if f.before != nil && f.before(w, r) {
		return
	}
	if r.URL.Path == ModelPath {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("model wire method/content type")
		}
		var b map[string]any
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			t.Error("model JSON")
		}
		f.mu.Lock()
		f.requests = append(f.requests, b)
		n := len(f.requests)
		f.mu.Unlock()
		if f.model == nil {
			t.Error("unexpected model call")
			w.WriteHeader(500)
			return
		}
		f.model(w, r, n)
		return
	}
	if r.URL.Path != MCPPath {
		t.Error("unexpected path")
		w.WriteHeader(404)
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var rpc struct {
		ID     json.RawMessage
		Method string
		Params json.RawMessage
	}
	_ = json.NewDecoder(r.Body).Decode(&rpc)
	if len(rpc.ID) == 0 {
		w.WriteHeader(202)
		return
	}
	var result any
	switch rpc.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": "2026-07-28", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fake-core", "version": "1"}}
	case "tools/list":
		if f.list != nil {
			result = f.list
		} else {
			result = json.RawMessage(`{"tools":[{"name":"erp_list_things","description":"Lists things.","inputSchema":{"type":"object"}}]}`)
		}
	case "tools/call":
		f.mu.Lock()
		f.calls++
		f.mu.Unlock()
		var p struct {
			Name      string
			Arguments map[string]any
		}
		_ = json.Unmarshal(rpc.Params, &p)
		if p.Name != "erp_list_things" || p.Arguments["q"] != secret {
			t.Error("tool call not forwarded")
		}
		result = f.tool
		if f.tool == nil {
			result = json.RawMessage(`{"content":[{"type":"text","text":"` + secret + `"}]}`)
		}
	default:
		result = map[string]any{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }
func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.b.Bytes())
}
func setup(t *testing.T, f *fakeCore) (*Runner, string, *lockedBuffer) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	t.Cleanup(s.Close)
	key := filepath.Join(t.TempDir(), "runtime-key")
	if e := os.WriteFile(key, []byte("test-runtime-key\n"), 0600); e != nil {
		t.Fatal(e)
	}
	logs := new(lockedBuffer)
	r, e := New(Config{GatewayURL: s.URL, KeyFile: key, Log: logs})
	if e != nil {
		t.Fatal(e)
	}
	return r, key, logs
}
func invoke(t *testing.T, r *Runner, ctx context.Context, f fixture, edit func(*http.Request)) (*httptest.ResponseRecorder, json.RawMessage) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}}, "name": f.Tool, "arguments": f.Arguments}})
	req := httptest.NewRequest("POST", "http://runner/mcp", bytes.NewReader(b)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer test-runtime-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", f.Tool)
	for k, v := range f.Headers {
		req.Header.Set(k, v)
	}
	if edit != nil {
		edit(req)
	}
	w := httptest.NewRecorder()
	r.Handler().ServeHTTP(w, req)
	var out struct{ Result json.RawMessage }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	// The SDK adds transport identity, just as for script-runner. Compare the
	// complete application result after removing only that SDK-owned field.
	if len(out.Result) > 0 {
		var app map[string]any
		_ = json.Unmarshal(out.Result, &app)
		if meta, ok := app["_meta"].(map[string]any); ok {
			delete(meta, "io.modelcontextprotocol/serverInfo")
		}
		out.Result, _ = json.Marshal(app)
	}
	return w, out.Result
}
func modelReply(w http.ResponseWriter, content, stop string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"content": json.RawMessage(content), "role": "assistant", "stop_reason": stop}, "usage": map[string]any{"tokens_in": 1, "tokens_out": 1}})
}
func end(t *testing.T, b []byte, want string, steps int) {
	t.Helper()
	var r struct {
		Meta map[string]struct {
			End   string
			Steps int
		} `json:"_meta"`
	}
	if json.Unmarshal(b, &r) != nil || r.Meta["januaport.ai/run"].End != want || r.Meta["januaport.ai/run"].Steps != steps {
		t.Fatalf("want %s/%d: %s", want, steps, b)
	}
}

func TestLoopGoldenThinkingAndLog(t *testing.T) {
	thought := `{"type":"thinking","thinking":"` + secret + `","signature":"  signed==  "}`
	redacted := `{"type":"redacted_thinking","data":"opaque=="}`
	f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
		if n < 4 {
			modelReply(w, `[`+thought+`,`+redacted+`,{"type":"tool_use","id":"call_`+fmt.Sprint(n)+`","name":"erp_list_things","input":{"q":"`+secret+`"}}]`, "tool_use")
		} else {
			modelReply(w, `[{"type":"text","text":"Drei Rechnungen geprüft; eine weicht von der Bestellung ab."}]`, "end_turn")
		}
	}}
	r, _, logs := setup(t, f)
	q := requestFixture(t)
	q.Arguments.Input = secret
	q.Arguments.Instructions = secret
	_, got := invoke(t, r, context.Background(), q, nil)
	equalJSON(t, got, golden(t, "run_agent_response_ok.json"))
	if f.calls != 3 {
		t.Fatal(f.calls)
	}
	for i, b := range f.requests {
		if b["model"] != nil || b["stream"] != nil || b["tool_choice"] != nil {
			t.Fatal("forbidden top-level field")
		}
		messages := b["messages"].([]any)
		if len(messages) != 1+2*i {
			t.Fatal("history rewritten")
		}
		for j := 1; j < len(messages); j += 2 {
			blocks := messages[j].(map[string]any)["content"].([]any)
			a, _ := json.Marshal(blocks[0])
			equalJSON(t, a, []byte(thought))
			a, _ = json.Marshal(blocks[1])
			equalJSON(t, a, []byte(redacted))
			res := messages[j+1].(map[string]any)["content"].([]any)[0].(map[string]any)
			if res["type"] != "tool_result" || res["tool_use_id"] != fmt.Sprintf("call_%d", (j+1)/2) {
				t.Fatal("result correlation")
			}
		}
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "lauf-token") || strings.Contains(logs.String(), "runtime-key") {
		t.Fatal("content leaked into log")
	}
	var log map[string]any
	if json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &log) != nil {
		t.Fatal(logs.String())
	}
	if log["duration_ms"].(float64) < 0 {
		t.Fatal(log)
	}
	log["duration_ms"] = float64(0)
	b, _ := json.Marshal(log)
	want, e := os.ReadFile("../../contract/proposed/run_log_ok.json")
	if e != nil {
		t.Fatal(e)
	}
	equalJSON(t, b, want)
}

func TestGatewayErrorsFixed(t *testing.T) {
	for _, tc := range []struct{ file, end string }{{"model_error_limit.json", "limit"}, {"model_error_provider.json", "error"}, {"model_error_rejected.json", "error"}} {
		t.Run(tc.file, func(t *testing.T) {
			var g struct {
				Status int
				Body   json.RawMessage
			}
			_ = json.Unmarshal(golden(t, tc.file), &g)
			var body map[string]any
			_ = json.Unmarshal(g.Body, &body)
			body["error"].(map[string]any)["message"] = secret
			f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
				w.WriteHeader(g.Status)
				_ = json.NewEncoder(w).Encode(body)
			}}
			r, _, log := setup(t, f)
			_, out := invoke(t, r, context.Background(), requestFixture(t), nil)
			end(t, out, tc.end, 1)
			if strings.Contains(string(out)+log.String(), secret) {
				t.Fatal("provider text leaked")
			}
			want, e := os.ReadFile("../../contract/proposed/run_agent_response_" + strings.TrimSuffix(strings.TrimPrefix(tc.file, "model_error_"), ".json") + ".json")
			if e != nil {
				t.Fatal(e)
			}
			equalJSON(t, out, want)
		})
	}
}
func TestLimitsAtAdapter(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		steps, calls, wantSteps, wantCalls int
	}{{"steps", 2, 50, 2, 2}, {"tool_calls", 20, 1, 2, 1}, {"zero_tools", 20, 0, 1, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
				modelReply(w, `[{"type":"tool_use","id":"x","name":"erp_list_things","input":{"q":"`+secret+`"}}]`, "tool_use")
			}}
			r, _, _ := setup(t, f)
			q := requestFixture(t)
			q.Arguments.Limits.Steps = tc.steps
			q.Arguments.Limits.ToolCalls = tc.calls
			_, b := invoke(t, r, context.Background(), q, nil)
			end(t, b, "limit", tc.wantSteps)
			if f.calls != tc.wantCalls {
				t.Fatal(f.calls)
			}
		})
	}
}
func TestCancellationAndWallAtAdapter(t *testing.T) {
	for _, wall := range []bool{false, true} {
		t.Run(fmt.Sprint(wall), func(t *testing.T) {
			entered := make(chan struct{})
			f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) { close(entered); <-r.Context().Done() }}
			r, _, logs := setup(t, f)
			q := requestFixture(t)
			q.Arguments.Limits.WallS = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan json.RawMessage, 1)
			go func() { _, b := invoke(t, r, ctx, q, nil); done <- b }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("not started")
			}
			want := "limit"
			if !wall {
				cancel()
				want = "cancelled"
			}
			select {
			case b := <-done:
				if wall || len(b) > 0 {
					end(t, b, want, 1)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("context lost")
			}
			until := time.Now().Add(time.Second)
			for logs.String() == "" && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
			if !strings.Contains(logs.String(), `"end":"`+want+`"`) {
				t.Fatal("missing terminal log")
			}
		})
	}
}

func TestAuthHeadersAndPathsAtAdapter(t *testing.T) {
	f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
		modelReply(w, `[{"type":"text","text":"ok"}]`, "end_turn")
	}}
	r, key, _ := setup(t, f)
	for _, edit := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, func(r *http.Request) { r.Header.Add("Authorization", "Bearer test-runtime-key") }} {
		w, _ := invoke(t, r, context.Background(), requestFixture(t), edit)
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	for _, mutate := range []func(*fixture){func(q *fixture) { delete(q.Headers, "X-Jnpt-Run-Token") }, func(q *fixture) { q.Headers["X-Jnpt-Run-Id"] = "wrong" }, func(q *fixture) { q.Tool = "other" }, func(q *fixture) { q.Arguments.Gateway.ModelPath = "https://example.invalid/model" }, func(q *fixture) { q.Arguments.Gateway.MCPPath = "//example.invalid/mcp" }, func(q *fixture) { q.Arguments.Contract = 2 }, func(q *fixture) { q.Arguments.Limits.Steps = 0 }, func(q *fixture) { q.Arguments.Limits.WallS = 0 }} {
		q := requestFixture(t)
		mutate(&q)
		w, _ := invoke(t, r, context.Background(), q, nil)
		if !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatal(w.Body.String())
		}
	}
	if len(f.requests) != 0 {
		t.Fatal("rejected request ran")
	}
	if e := os.WriteFile(key, []byte("rotated"), 0600); e != nil {
		t.Fatal(e)
	}
	w, _ := invoke(t, r, context.Background(), requestFixture(t), nil)
	if w.Code != 401 {
		t.Fatal("old key accepted")
	}
	if e := os.Remove(key); e != nil {
		t.Fatal(e)
	}
	w, _ = invoke(t, r, context.Background(), requestFixture(t), nil)
	if w.Code != 401 {
		t.Fatal("missing key accepted")
	}
}

func TestUnsafeResponseAndUnknownToolAtAdapter(t *testing.T) {
	for _, content := range []string{`[{"type":"server_tool_use","name":"web_search"}]`, `[{"type":"tool_use","name":"not_listed","id":"x","input":{}}]`, `[{"type":"image","source":{"type":"url","url":"https://example.invalid/SECRET"}}]`} {
		f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) { modelReply(w, content, "tool_use") }}
		r, _, _ := setup(t, f)
		_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
		end(t, b, "error", 1)
		if f.calls != 0 {
			t.Fatal("unlisted tool called")
		}
	}
}

func TestRunnerAdvertisementAndHealth(t *testing.T) {
	r, key, _ := setup(t, &fakeCore{})
	h := r.Handler()
	for _, p := range []string{"/healthz", "/other"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		want := 200
		if p != "/healthz" {
			want = 404
		}
		if w.Code != want {
			t.Fatal(w.Code)
		}
	}
	w := httptest.NewRecorder()
	q := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`))
	q.Header.Set("Authorization", "Bearer test-runtime-key")
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Accept", "application/json, text/event-stream")
	q.Header.Set("MCP-Protocol-Version", "2026-07-28")
	q.Header.Set("Mcp-Method", "tools/list")
	h.ServeHTTP(w, q)
	var rpc struct {
		Result struct {
			Tools []struct{ Name string }
			Meta  map[string]struct {
				MaxWallS  int      `json:"max_wall_s"`
				Contract  int      `json:"contract"`
				Protocols []string `json:"protocols"`
			} `json:"_meta"`
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rpc)
	if len(rpc.Result.Tools) != 1 || rpc.Result.Tools[0].Name != "run_agent" || rpc.Result.Meta["januaport.ai/runner"].MaxWallS != 300 || rpc.Result.Meta["januaport.ai/runner"].Contract != 1 || !reflect.DeepEqual(rpc.Result.Meta["januaport.ai/runner"].Protocols, []string{"anthropic", "openai"}) {
		t.Fatal(w.Body.String())
	}
	_ = os.Remove(key)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 503 || w.Body.String() != "not ready\n" {
		t.Fatal(w.Body.String())
	}
}

// Live shape since 10/2026 (#1099): every tool_use carries `caller`, the response
// top level carries container/stop_details. A direct caller runs and stays in history.
func TestLoopToolUseCaller(t *testing.T) {
	use := `{"type":"tool_use","id":"call_1","name":"erp_list_things","input":{"q":"` + secret + `"},"caller":{"type":"direct"}}`
	f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"content": json.RawMessage(`[` + use + `]`), "role": "assistant", "stop_reason": "tool_use", "container": nil, "stop_details": nil}, "usage": map[string]any{"tokens_in": 1, "tokens_out": 1}})
		} else {
			modelReply(w, `[{"type":"text","text":"fertig"}]`, "end_turn")
		}
	}}
	r, _, _ := setup(t, f)
	_, got := invoke(t, r, context.Background(), requestFixture(t), nil)
	if f.calls != 1 {
		t.Fatalf("tool calls %d: %s", f.calls, got)
	}
	last := f.requests[len(f.requests)-1]["messages"].([]any)
	a, _ := json.Marshal(last[1].(map[string]any)["content"].([]any)[0])
	equalJSON(t, a, []byte(use))
}

func TestLoopProgrammaticCallerRejected(t *testing.T) {
	f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
		modelReply(w, `[{"type":"tool_use","id":"call_1","name":"erp_list_things","input":{},"caller":{"type":"code_execution_20250825","tool_id":"srvtoolu_1"}}]`, "tool_use")
	}}
	r, _, _ := setup(t, f)
	_, got := invoke(t, r, context.Background(), requestFixture(t), nil)
	if f.calls != 0 || !strings.Contains(string(got), `"gateway"`) {
		t.Fatalf("calls %d: %s", f.calls, got)
	}
}
