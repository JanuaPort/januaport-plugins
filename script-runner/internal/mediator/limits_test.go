package mediator

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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

// The timer records the actual duration passed by run after a real Git tree,
// HTTP/MCP request and sandbox job; no wall-clock wait for long runs.
func TestRunLimitsWallSelectionAtAdapter(t *testing.T) {
	for _, tt := range []struct {
		name, timeout, header string
		maxWall, want         int
	}{
		{"header_without_manifest", "", "wall_s=300;output_bytes=1048576;tool_calls=50", 0, 300},
		{"manifest_wins", "120", "wall_s=300;output_bytes=1048576;tool_calls=50", 0, 120},
		{"header_wins", "600", "wall_s=300;output_bytes=1048576;tool_calls=50", 0, 300},
		{"legacy_without_manifest", "", "", 0, 25},
		{"legacy_long_manifest", "300", "", 0, 25},
		{"legacy_short_manifest", "1", "", 0, 1},
		{"technical_ceiling", "86400", "wall_s=86401;output_bytes=1048576;tool_calls=50", 0, 86400},
		{"configured_ceiling", "600", "wall_s=600;output_bytes=1048576;tool_calls=50", 300, 300},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fx := newFixtureWith(t, func(f *fixture) {
				line := ""
				if tt.timeout != "" {
					line = "timeout_s: " + tt.timeout
				}
				f.write("quick/jnpt-script.yaml", strings.Replace(quickManifest, "timeout_s: 1", line, 1))
			})
			durations := make(chan time.Duration, 1)
			e := newEnvFx(t, fx, nil, func(c *Config) {
				c.MaxWallS = tt.maxWall
				c.NewWallTimer = func(d time.Duration) *time.Timer {
					durations <- d
					return time.NewTimer(0)
				}
			})
			e.writePins(e.quickPin())
			done := e.oneRun("wall_selection", func(g *fakeGuard, _ frame.Job) { _ = g.waitEOF() })
			e.waitReady("quick")
			var headers []string
			if tt.header != "" {
				headers = []string{tt.header}
			}
			assertEnd(t, e.callLimits(t, "quick", headers...), contract.EndTimeout, "")
			select {
			case got := <-durations:
				if got != time.Duration(tt.want)*time.Second {
					t.Fatalf("wall=%v want=%ds", got, tt.want)
				}
			default:
				t.Fatal("run did not create a wall timer")
			}
			<-done
		})
	}
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

func TestRunLimitsInvalidManifestTimeoutAtAdapter(t *testing.T) {
	for _, value := range []string{"0", "-1", "86401", "null", "1.5", "true", "\"300\""} {
		t.Run(value, func(t *testing.T) {
			fx := newFixtureWith(t, func(f *fixture) {
				line := "timeout_s: " + value
				if value == "missing" {
					line = ""
				}
				f.write("quick/jnpt-script.yaml", strings.Replace(quickManifest, "timeout_s: 1", line, 1))
			})
			e := newEnvFx(t, fx, []pinRow{{Name: "quick", Path: "quick", Commit: fx.commit}})
			g := dialGuard(t, e.sock, "manifest", 2)
			jobs := make(chan bool, 1)
			go func() {
				_, err := frame.Read(g.conn, 8<<20)
				if err == nil {
					g.exited(0)
				}
				jobs <- err == nil
			}()
			state, reason := e.waitPinSettled("quick")
			if state != contract.StateInvalid || reason != contract.ReasonManifestInvalid {
				t.Fatalf("%s/%s", state, reason)
			}
			res := e.callLimits(t, "quick", "wall_s=600;output_bytes=2097152;tool_calls=60")
			assertEnd(t, res, contract.EndRefused, contract.DetailState)
			if got := res.Content[0].(*mcp.TextContent).Text; got != "Das Skript ist im Skript-Läufer noch nicht bereit." {
				t.Fatal(got)
			}
			_ = g.conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
			if <-jobs {
				t.Fatal("invalid manifest started a job")
			}
		})
	}
}
