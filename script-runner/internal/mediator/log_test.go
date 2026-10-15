package mediator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
	"github.com/januaport/januaport-plugins/script-runner/internal/runlog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunLogOnlyInMetaNeverInSlog(t *testing.T) {
	const canary = "LOG-CANARY-1041-private"
	for _, end := range []string{"ok", "error", "memory", "timeout", "lost", "frame", "output", "tool_calls"} {
		t.Run(end, func(t *testing.T) {
			e := newEnv(t, nil)
			e.writePins(e.quickPin())
			done := e.oneRun("log-"+end, func(g *fakeGuard, _ frame.Job) {
				g.send(map[string]any{"type": "log", "data": canary + "\n"})
				switch end {
				case "ok":
					g.send(frame.Output{Type: frame.TypeOutput, Data: `{"ok":true}`})
					g.exited(0)
				case "error":
					g.send(frame.Exited{Type: frame.TypeExited, Code: 1, ErrorType: "ValueError"})
				case "memory":
					g.send(frame.Exited{Type: frame.TypeExited, Code: -1, Signal: 9})
				case "timeout":
					_ = g.waitEOF()
				case "lost":
					_ = g.conn.Close()
				case "frame":
					g.send(frame.Output{Type: frame.TypeOutput, Data: strings.Repeat("x", frame.MaxFromSandbox)})
					_ = g.waitEOF()
				case "output":
					g.send(frame.Output{Type: frame.TypeOutput, Data: strings.Repeat("x", contract.MaxOutputBytes)})
					g.send(frame.Output{Type: frame.TypeOutput, Data: "x"})
					_ = g.waitEOF()
				case "tool_calls":
					for i := 0; i < contract.MaxToolCalls+1; i++ {
						g.send(frame.ToolCall{Type: frame.TypeToolCall, Name: "undeclared"})
						if i < contract.MaxToolCalls {
							_, _, _ = g.read()
						}
					}
					_ = g.waitEOF()
				}
			})
			e.waitReady("quick")
			res := e.mustCall("quick", map[string]any{})
			<-done
			meta := runMeta(t, res)
			if meta["log"] != canary+"\n" {
				t.Fatalf("lost log at %s: %v", end, meta)
			}
			want := map[string]string{"ok": "ok", "error": "error", "memory": "limit", "timeout": "timeout", "lost": "limit", "frame": "limit", "output": "limit", "tool_calls": "limit"}[end]
			if meta["end"] != want {
				t.Fatalf("end=%v want %s", meta["end"], want)
			}
			detail := map[string]string{"error": contract.DetailException, "memory": contract.DetailMemory, "lost": contract.DetailSandboxLost, "frame": contract.DetailFrame, "output": contract.DetailOutput, "tool_calls": contract.DetailToolCalls}[end]
			if detail != "" && meta["detail"] != detail {
				t.Fatalf("detail=%v want %s", meta["detail"], detail)
			}
			if strings.Contains(mustJSON(res.StructuredContent), canary) {
				t.Fatal("log leaked into structuredContent")
			}
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, canary) {
					t.Fatal("log leaked into content")
				}
			}
			if strings.Contains(e.logs.String(), canary) {
				t.Fatal("log leaked into mediator slog")
			}
		})
	}
}

func TestRunLogInternalCancellation(t *testing.T) {
	r := &runner{sc: &sbConn{lost: make(chan struct{})}, a: &attachment{frames: make(chan inFrame, 1)}}
	p, _ := json.Marshal(map[string]any{"type": "log", "data": "before abort\n"})
	wall := time.NewTimer(time.Hour)
	defer wall.Stop()
	r.handle(context.Background(), inFrame{payload: p}, wall)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, _ := r.loop(ctx, wall)
	if got.End != contract.EndInternal || got.Log != "before abort\n" {
		t.Fatalf("abort: %+v", got)
	}
}

func TestRunLogAbsentBeforeStart(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	// No sandbox: state refusal precedes any job.
	res := e.mustCall("quick", map[string]any{})
	if _, ok := runMeta(t, res)["log"]; ok {
		t.Fatal("log before sandbox start")
	}
}

func TestMediatorBoundsUntrustedLogFrames(t *testing.T) {
	r := &runner{}
	wall := time.NewTimer(time.Hour)
	defer wall.Stop()
	p, _ := json.Marshal(map[string]any{"type": "log", "data": strings.Repeat("x", 4096)})
	for range 256 {
		r.handle(context.Background(), inFrame{payload: p}, wall)
	}
	got, _ := r.end(contract.EndError, contract.DetailExit)
	if len(got.Log) > 64<<10 || !strings.HasSuffix(got.Log, "\n[gekürzt]\n") {
		t.Fatal("unbounded log from untrusted guard")
	}
}

func TestMediatorPreservesGuardLogAtMultibyteCut(t *testing.T) {
	var guard runlog.Buffer
	r := &runner{}
	wall := time.NewTimer(time.Hour)
	defer wall.Stop()
	for _, s := range []string{strings.Repeat("x", 64<<10-40), strings.Repeat("😀", 40)} {
		p, _ := json.Marshal(frame.Log{Type: frame.TypeLog, Data: guard.Append(s)})
		r.handle(context.Background(), inFrame{payload: p}, wall)
	}
	got, _ := r.end(contract.EndOK, "")
	if got.Log != guard.String() {
		t.Fatal("mediator changed the guard's multibyte cut/marker")
	}
}
