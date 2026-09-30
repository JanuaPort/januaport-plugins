package mediator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

const (
	testKey      = "test-runtime-key-not-a-secret"
	testRunToken = "jnptrun_EXAMPLE_EXAMPLE_EXAMPLE_EXAMPLE_EXAMPLE_EXA"
	testRunID    = "0b1e4c8a-6f2d-4b7e-9a31-2c5d7e8f9a10"
)

// syncBuffer fängt das Log des vermittlers für die M6-Prüfung.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// stubGateway ist Weg B: ein MCP-Server mit zwei Werkzeugen. Er zählt die
// Aufrufe und hält den Authorization-Header fest.
type stubGateway struct {
	srv   *httptest.Server
	calls atomic.Int64
	mu    sync.Mutex
	auth  []string
	args  []string
}

func newStubGateway(t *testing.T) *stubGateway {
	t.Helper()
	g := &stubGateway{}
	server := mcp.NewServer(&mcp.Implementation{Name: "stub-gateway", Version: "0"}, nil)
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		g.calls.Add(1)
		g.mu.Lock()
		g.args = append(g.args, string(req.Params.Arguments))
		g.mu.Unlock()
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: `{"found":2}`}},
			StructuredContent: map[string]any{"found": 2},
		}, nil
	}
	schema := map[string]any{"type": "object"}
	server.AddTool(&mcp.Tool{Name: "kartei_find", InputSchema: schema}, handler)
	server.AddTool(&mcp.Tool{Name: "memory_delete", InputSchema: schema}, handler)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		g.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *stubGateway) authHeaders() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.auth...)
}

// fixture ist ein Skript-Repository mit echtem git. Der Bare-Store liegt
// dort, wo der abholer ihn hinlegt: <store>/store.git.
type fixture struct {
	t      *testing.T
	work   string
	store  string
	commit string
}

const invoiceManifest = `name: invoice_check
description: Prüft offene Rechnungen gegen die Kartei.
entry: main.py
input_schema:
  type: object
  properties:
    limit: {type: integer}
tools:
  - kartei_find
  - memory_append
timeout_s: 20
`

const quickManifest = `name: quick
description: Kurzer Testlauf.
entry: main.py
input_schema:
  type: object
tools:
  - kartei_find
timeout_s: 1
`

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git fehlt")
	}
	f := &fixture{t: t, work: t.TempDir(), store: t.TempDir()}
	f.git(f.work, "init", "-q", ".")
	f.git(f.work, "config", "user.email", "test@example.com")
	f.git(f.work, "config", "user.name", "Test")
	f.write("invoice-check/jnpt-script.yaml", invoiceManifest)
	f.write("invoice-check/main.py", "print('hi')\n")
	f.write("invoice-check/lib/helper.py", "X = 1\n")
	f.write("quick/jnpt-script.yaml", quickManifest)
	f.write("quick/main.py", "print('quick')\n")
	f.write("broken/jnpt-script.yaml", strings.Replace(quickManifest, "name: quick", "name: broken", 1))
	f.write("broken/main.py", "x\n")
	if err := os.Symlink("/etc/passwd", filepath.Join(f.work, "broken", "passwd")); err != nil {
		t.Fatal(err)
	}
	f.git(f.work, "add", "-A")
	f.git(f.work, "commit", "-q", "-m", "c")
	f.commit = f.git(f.work, "rev-parse", "HEAD")
	f.git(f.store, "init", "-q", "--bare", "store.git")
	f.git(f.work, "push", "-q", filepath.Join(f.store, "store.git"), "HEAD:refs/heads/main")
	return f
}

func (f *fixture) git(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) write(path, content string) {
	f.t.Helper()
	full := filepath.Join(f.work, path)
	_ = os.MkdirAll(filepath.Dir(full), 0o755)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) blob(path string) string {
	return f.git(f.work, "rev-parse", f.commit+":"+path)
}

type pinRow struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Commit string `json:"commit"`
}

// env ist ein laufender vermittler mit Stub-Gateway, Fixture und Log.
type env struct {
	t    *testing.T
	fx   *fixture
	gw   *stubGateway
	m    *Mediator
	srv  *httptest.Server
	sock string
	keys string
	logs *syncBuffer
}

type envOpt func(*Config)

func withBusyWait(d time.Duration) envOpt { return func(c *Config) { c.BusyWait = d } }

func newEnv(t *testing.T, pins []pinRow, opts ...envOpt) *env {
	t.Helper()
	fx := newFixture(t)
	gw := newStubGateway(t)
	sockDir, err := os.MkdirTemp("", "sr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	e := &env{t: t, fx: fx, gw: gw, sock: filepath.Join(sockDir, "sandbox.sock"), keys: t.TempDir(), logs: &syncBuffer{}}
	e.writeKey(testKey)
	if pins != nil {
		e.writePins(pins)
	}
	cfg := Config{
		SocketPath:   e.sock,
		KeyDir:       e.keys,
		StoreDir:     fx.store,
		GatewayURL:   gw.srv.URL,
		Logger:       slog.New(slog.NewTextHandler(e.logs, nil)),
		BusyWait:     3 * time.Second,
		PollInterval: 20 * time.Millisecond,
	}
	for _, o := range opts {
		o(&cfg)
	}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = m.Serve(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	e.m = m
	e.srv = httptest.NewServer(m.Handler())
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) writeKey(k string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.keys, "runtime-key"), []byte(k+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) writePins(rows []pinRow) {
	e.t.Helper()
	b, _ := json.Marshal(map[string]any{"version": 1, "repo_url": "ssh://git@git.example.com/org/scripts.git", "pins": rows})
	tmp := filepath.Join(e.keys, "pins.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(e.keys, "pins.json")); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) quickPin() []pinRow {
	return []pinRow{
		{Name: "quick", Path: "quick", Commit: e.fx.commit},
		{Name: "invoice_check", Path: "invoice-check", Commit: e.fx.commit},
	}
}

// session öffnet eine MCP-Sitzung wie das Gateway: Bearer runtime-key plus
// die Lauf-Header aus §3.
type headerRT struct {
	h    http.Header
	base http.RoundTripper
}

func (rt headerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range rt.h {
		r.Header[k] = v
	}
	return rt.base.RoundTrip(r)
}

func (e *env) session(h http.Header) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "test-gateway", Version: "0"}, nil)
	tr := &mcp.StreamableClientTransport{
		Endpoint:   e.srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: headerRT{h: h, base: http.DefaultTransport}},
	}
	return client.Connect(context.Background(), tr, nil)
}

func runHeaders(commit string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+testKey)
	h.Set(contract.HeaderRunToken, testRunToken)
	h.Set(contract.HeaderRunID, testRunID)
	h.Set(contract.HeaderPinCommit, commit)
	return h
}

func (e *env) listTools() *mcp.ListToolsResult {
	e.t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+testKey)
	s, err := e.session(h)
	if err != nil {
		e.t.Fatal(err)
	}
	defer s.Close()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

// waitReady wartet, bis der Pin in tools/list als Werkzeug erscheint.
func (e *env) waitReady(name string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, tool := range e.listTools().Tools {
			if tool.Name == name {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("Pin %s wird nicht ready", name)
}

func (e *env) call(name string, args any, commit string) (*mcp.CallToolResult, error) {
	s, err := e.session(runHeaders(commit))
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
}

func (e *env) mustCall(name string, args any) *mcp.CallToolResult {
	e.t.Helper()
	res, err := e.call(name, args, e.fx.commit)
	if err != nil {
		e.t.Fatalf("CallTool: %v", err)
	}
	return res
}

// runMeta liest _meta["januaport.ai/run"] aus einem Ergebnis.
func runMeta(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	raw, ok := res.Meta[contract.MetaRun]
	if !ok {
		t.Fatalf("_meta ohne %s: %+v", contract.MetaRun, res.Meta)
	}
	b, _ := json.Marshal(raw)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func assertEnd(t *testing.T, res *mcp.CallToolResult, end, detail string) map[string]any {
	t.Helper()
	m := runMeta(t, res)
	gotDetail, _ := m["detail"].(string)
	if m["end"] != end || gotDetail != detail {
		t.Fatalf("Ende = %v/%v, want %s/%s", m["end"], m["detail"], end, detail)
	}
	return m
}

// fakeGuard spricht das Rahmenprotokoll (§7) wie der Wächter.
type fakeGuard struct {
	t        *testing.T
	conn     net.Conn
	instance string
}

func dialGuard(t *testing.T, sock, instance string, seccomp int) *fakeGuard {
	t.Helper()
	var conn net.Conn
	var err error
	for i := 0; i < 250; i++ {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Socket nicht erreichbar: %v", err)
	}
	g := &fakeGuard{t: t, conn: conn, instance: instance}
	t.Cleanup(func() { _ = conn.Close() })
	g.send(frame.Hello{Type: frame.TypeHello, Instance: instance, Isolation: contract.IsolationStandard, Seccomp: seccomp})
	return g
}

func (g *fakeGuard) send(v any) {
	if err := frame.Write(g.conn, v); err != nil {
		g.t.Logf("fakeGuard send: %v", err)
	}
}

func (g *fakeGuard) read() (string, []byte, error) {
	_ = g.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	b, err := frame.Read(g.conn, 64<<20)
	if err != nil {
		return "", nil, err
	}
	typ, err := frame.TypeOf(b)
	return typ, b, err
}

func (g *fakeGuard) job() frame.Job {
	g.t.Helper()
	typ, b, err := g.read()
	if err != nil || typ != frame.TypeJob {
		g.t.Errorf("job erwartet, bekam %q, %v", typ, err)
		return frame.Job{}
	}
	var j frame.Job
	_ = json.Unmarshal(b, &j)
	return j
}

func (g *fakeGuard) exited(code int) {
	g.send(frame.Exited{Type: frame.TypeExited, Code: code})
}

// waitEOF liest, bis der vermittler die Verbindung schließt.
func (g *fakeGuard) waitEOF() error {
	for {
		if _, _, err := g.read(); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return fmt.Errorf("kein EOF: %w", err)
			}
			return nil
		}
	}
}

// oneRun ist der übliche Wächter: hello, job, dann fn, danach close.
func (e *env) oneRun(instance string, fn func(g *fakeGuard, j frame.Job)) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		g := dialGuard(e.t, e.sock, instance, 2)
		j := g.job()
		fn(g, j)
		_ = g.conn.Close()
	}()
	return done
}
