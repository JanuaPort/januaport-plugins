package mediator

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

func (e *env) callLimits(t *testing.T, name string, values ...string) *mcp.CallToolResult {
	t.Helper()
	h := runHeaders(e.fx.commit)
	for _, value := range values {
		h.Add("X-Jnpt-Run-Limits", value)
	}
	s, err := e.session(h)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRunLimitsOutputAtAdapter(t *testing.T) {
	for _, tt := range []struct {
		name, header string
		bytes        int
		end, detail  string
	}{
		{"small", "wall_s=25;output_bytes=10;tool_calls=50", 11, contract.EndLimit, contract.DetailOutput},
		{"exact", "wall_s=25;output_bytes=10;tool_calls=50", 10, contract.EndOK, ""},
		{"larger_than_v1", "wall_s=25;output_bytes=2097152;tool_calls=50", 1048577, contract.EndOK, ""},
		{"v1_without_header", "", 1048577, contract.EndLimit, contract.DetailOutput},
		{"clamped_ceiling", "wall_s=25;output_bytes=16777217;tool_calls=50", 16777217, contract.EndLimit, contract.DetailOutput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.writePins(e.quickPin())
			done := e.oneRun("limits", func(g *fakeGuard, _ frame.Job) {
				for left := tt.bytes; left > 0; {
					n := min(left, 64<<10)
					g.send(frame.Output{Type: frame.TypeOutput, Data: strings.Repeat("x", n)})
					left -= n
				}
				g.exited(0)
				_ = g.waitEOF()
			})
			e.waitReady("invoice_check")
			var headers []string
			if tt.header != "" {
				headers = []string{tt.header}
			}
			res := e.callLimits(t, "invoice_check", headers...)
			meta := assertEnd(t, res, tt.end, tt.detail)
			if tt.end == contract.EndOK && meta["bytes_out"] != float64(tt.bytes) {
				t.Fatal(meta)
			}
			if tt.detail == contract.DetailOutput && res.Content[0].(*mcp.TextContent).Text != "Die Ausgabe des Skripts überschreitet die Grenze dieses Laufs." {
				t.Fatal(res.Content)
			}
			<-done
		})
	}
}

func TestRunLimitsToolCallsAtAdapter(t *testing.T) {
	for _, tt := range []struct {
		name, header       string
		attempts, accepted int
		end, detail        string
	}{
		{"second_rejected", "wall_s=25;output_bytes=1048576;tool_calls=1", 2, 1, contract.EndLimit, contract.DetailToolCalls},
		{"v1_sixty_attempts_without_header", "", 60, 50, contract.EndLimit, contract.DetailToolCalls},
		{"sixty_with_header", "wall_s=25;output_bytes=1048576;tool_calls=60", 60, 60, contract.EndOK, ""},
		{"sixty_first_rejected", "wall_s=25;output_bytes=1048576;tool_calls=60", 61, 60, contract.EndLimit, contract.DetailToolCalls},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.writePins(e.quickPin())
			done := e.oneRun("limits", func(g *fakeGuard, _ frame.Job) {
				for i := 1; i <= tt.attempts; i++ {
					g.send(frame.ToolCall{Type: frame.TypeToolCall, ID: int64(i), Name: "kartei_find", Arguments: json.RawMessage(`{}`)})
					if _, _, err := g.read(); err != nil {
						return
					}
				}
				g.exited(0)
			})
			e.waitReady("invoice_check")
			var headers []string
			if tt.header != "" {
				headers = []string{tt.header}
			}
			res := e.callLimits(t, "invoice_check", headers...)
			meta := assertEnd(t, res, tt.end, tt.detail)
			<-done
			if n := e.gw.calls.Load(); n != int64(tt.accepted) || meta["tool_calls"] != float64(tt.accepted) {
				t.Fatalf("gateway=%d meta=%v", n, meta)
			}
			if tt.detail == contract.DetailToolCalls && res.Content[0].(*mcp.TextContent).Text != "Das Skript hat die Grenze der Werkzeugaufrufe dieses Laufs überschritten." {
				t.Fatal(res.Content)
			}
		})
	}
}

func TestRunLimitsWallAtAdapter(t *testing.T) {
	for _, tt := range []struct {
		name           string
		manifest, wall int
	}{{"manifest", 1, 600}, {"header", 3, 1}} {
		t.Run(tt.name, func(t *testing.T) {
			fx := newFixtureWith(t, func(f *fixture) {
				f.write("quick/jnpt-script.yaml", strings.Replace(quickManifest, "timeout_s: 1", fmt.Sprintf("timeout_s: %d", tt.manifest), 1))
			})
			e := newEnvFx(t, fx, nil)
			e.writePins(e.quickPin())
			done := e.oneRun("wall", func(g *fakeGuard, _ frame.Job) { _ = g.waitEOF() })
			e.waitReady("quick")
			start := time.Now()
			assertEnd(t, e.callLimits(t, "quick", fmt.Sprintf("wall_s=%d;output_bytes=1048576;tool_calls=50", tt.wall)), contract.EndTimeout, "")
			elapsed := time.Since(start)
			if elapsed < 900*time.Millisecond || elapsed > 2*time.Second {
				t.Fatalf("wall=%v, want about 1s", elapsed)
			}
			<-done
		})
	}
}

// L6 must remain an independent runtime barrier even if a future manifest
// parser admits longer runs. Freeze state after New (no pollLoop), inject a
// 600-s manifest, then exercise the real HTTP/MCP and sandbox adapters.
func TestRunLimitsL6CapAtAdapter(t *testing.T) {
	fx := newFixture(t)
	gw := newStubGateway(t)
	sock := filepath.Join(t.TempDir(), "sb.sock")
	e := &env{t: t, fx: fx, gw: gw, sock: sock, keys: t.TempDir()}
	e.writeKey(testKey)
	e.writePins(e.quickPin())
	m, err := New(Config{SocketPath: sock, KeyDir: e.keys, StoreDir: fx.store, GatewayURL: gw.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.pins {
		if m.pins[i].pin.Name == "quick" {
			m.pins[i].manifest.TimeoutS = 600
		}
	}
	e.m = m
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan struct{})
	go func() { _ = m.sb.accept(ln); close(serveDone) }()
	t.Cleanup(func() { _ = ln.Close(); <-serveDone })
	e.srv = httptest.NewServer(m.Handler())
	t.Cleanup(e.srv.Close)
	done := e.oneRun("l6", func(g *fakeGuard, _ frame.Job) {
		_ = g.conn.SetReadDeadline(time.Now().Add(28 * time.Second))
		_, _ = frame.Read(g.conn, 64<<20)
	})
	e.waitReady("quick")
	s, err := e.session(runLimitsHeaders(fx.commit, "wall_s=600;output_bytes=1048576;tool_calls=50"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
	defer cancel()
	start := time.Now()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "quick", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("L6 did not end run: %v", err)
	}
	assertEnd(t, res, contract.EndTimeout, "")
	if elapsed := time.Since(start); elapsed < 24*time.Second || elapsed > 27*time.Second {
		t.Fatalf("L6 wall=%v", elapsed)
	}
	<-done
}

func runLimitsHeaders(commit, value string) http.Header {
	h := runHeaders(commit)
	h.Set("X-Jnpt-Run-Limits", value)
	return h
}

func TestMalformedRunLimitsAtAdapterDoNotStartOrOccupySlot(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	g := dialGuard(t, e.sock, "still_fresh", 2)
	e.waitReady("quick")
	const good = "wall_s=25;output_bytes=1048576;tool_calls=50"
	values := [][]string{
		{""}, {"KANARIE-geheimer-Headerwert"}, {"output_bytes=10;wall_s=1;tool_calls=1"}, {"wall_s=1;output_bytes=10"},
		{good + ";tool_calls=50"}, {"wall_s=1;wall_s=1;tool_calls=1"}, {strings.Replace(good, ";", "; ", 1)},
		{good, good}, {good + ", " + good},
	}
	for _, key := range []string{"wall_s=25", "output_bytes=1048576", "tool_calls=50"} {
		for _, v := range []string{"0", "-1", "NaN", "+1", "18446744073709551616"} {
			values = append(values, []string{strings.Replace(good, key, strings.Split(key, "=")[0]+"="+v, 1)})
		}
	}
	for i, v := range values {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			h := runHeaders(e.fx.commit)
			for _, value := range v {
				h.Add("X-Jnpt-Run-Limits", value)
			}
			s, err := e.session(h)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "quick", Arguments: map[string]any{}})
			if err == nil || res != nil || !strings.Contains(err.Error(), "contract: Lauf-Grenzen sind falsch geformt") {
				t.Fatalf("result=%v err=%v", res, err)
			}
			if strings.Contains(err.Error(), "KANARIE") || strings.Contains(e.logs.String(), "KANARIE") {
				t.Fatal("header value leaked")
			}
			_ = g.conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
			if _, err := frame.Read(g.conn, 8<<20); err == nil {
				t.Fatal("malformed header started a job")
			} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
				t.Fatalf("fresh sandbox lost: %v", err)
			}
		})
	}
	done := make(chan struct{})
	go func() { defer close(done); g.job(); g.exited(0) }()
	start := time.Now()
	assertEnd(t, e.callLimits(t, "quick", good), contract.EndOK, "")
	if time.Since(start) > time.Second {
		t.Fatal("next valid run did not start immediately")
	}
	<-done
	if e.gw.calls.Load() != 0 {
		t.Fatal("unexpected tool call")
	}
}

func TestRunLimitsDoNotRaiseInputCeilingAtAdapter(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	g := dialGuard(t, e.sock, "input", 2)
	e.waitReady("quick")
	// Consume a forbidden job too, so a removed input check fails by its
	// result instead of blocking the mediator on a full Unix-socket buffer.
	jobStarted := make(chan bool, 1)
	go func() {
		_, err := frame.Read(g.conn, 8<<20)
		if err == nil {
			g.exited(0)
		}
		jobStarted <- err == nil
	}()
	s, err := e.session(runLimitsHeaders(e.fx.commit, "wall_s=86400;output_bytes=16777216;tool_calls=10000"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "quick", Arguments: map[string]any{"blob": strings.Repeat("x", 262144)}})
	if err != nil {
		t.Fatal(err)
	}
	assertEnd(t, res, contract.EndRefused, contract.DetailInput)
	_ = g.conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	if <-jobStarted {
		t.Fatal("oversized input started a job")
	}
}

func TestRunLimitsManifestTimeoutStillRequiredAtAdapter(t *testing.T) {
	for _, value := range []string{"missing", "0", "26"} {
		t.Run(value, func(t *testing.T) {
			fx := newFixtureWith(t, func(f *fixture) {
				line := "timeout_s: " + value
				if value == "missing" {
					line = ""
				}
				f.write("quick/jnpt-script.yaml", strings.Replace(quickManifest, "timeout_s: 1", line, 1))
			})
			e := newEnvFx(t, fx, []pinRow{{Name: "quick", Path: "quick", Commit: fx.commit}})
			_ = dialGuard(t, e.sock, "manifest", 2)
			state, reason := e.waitPinSettled("quick")
			if state != contract.StateInvalid || reason != contract.ReasonManifestInvalid {
				t.Fatalf("%s/%s", state, reason)
			}
			assertEnd(t, e.callLimits(t, "quick", "wall_s=600;output_bytes=2097152;tool_calls=60"), contract.EndRefused, contract.DetailState)
		})
	}
}
