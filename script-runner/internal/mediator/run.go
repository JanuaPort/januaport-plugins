package mediator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
	"github.com/januaport/januaport-plugins/script-runner/internal/store"
)

// maxWall ist die Obergrenze der Wandzeit (Grenzen v1, unter dem
// 30-s-Relay des Gateways).
const maxWall = 25 * time.Second

// callTool ist tools/call (Vertrag §3, §4). Fachliches steht immer im
// Ergebnis; ein JSON-RPC-Fehler nur bei fehlenden Lauf-Headern.
func (m *Mediator) callTool(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var header http.Header
	if req.Extra != nil {
		header = req.Extra.Header
	}
	rh, err := contract.ParseRunHeaders(header)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	p, isolation, found := m.findPin(req.Params.Name)
	var r contract.Run
	var out []byte
	switch {
	case !found:
		r = contract.Run{End: contract.EndRefused, Detail: contract.DetailPin}
	case rh.PinCommit != p.pin.Commit:
		r = contract.Run{End: contract.EndRefused, Detail: contract.DetailCommit}
	case p.state != contract.StateReady || isolation == contract.IsolationInvalid:
		r = contract.Run{End: contract.EndRefused, Detail: contract.DetailState}
	case len(req.Params.Arguments) > contract.MaxInputBytes:
		r = contract.Run{End: contract.EndRefused, Detail: contract.DetailInput}
	default:
		r, out = m.run(ctx, p, req.Params.Arguments, rh.RunToken)
	}
	if r.Isolation == "" {
		r.Isolation = isolation
	}
	m.log.Info("run", "end", r.End, "detail", r.Detail, "duration_ms", time.Since(start).Milliseconds(),
		"bytes_out", r.BytesOut, "tool_calls", r.ToolCalls)
	return contract.Result(r, out), nil
}

// run führt einen Lauf auf einer frischen Instanz aus. Ende und Dauer misst
// der vermittler selbst (M2); die Angaben des Wächters zählen nur für
// ok/error innerhalb der Wandzeit.
func (m *Mediator) run(ctx context.Context, p pinState, input json.RawMessage, runToken string) (contract.Run, []byte) {
	// Die Senke rechnet vor jeder Übergabe nach (R6, M5).
	tree, err := m.store.Load(ctx, p.pin.Commit, p.pin.Path)
	if err != nil {
		m.forget(p.pin)
		return contract.Run{End: contract.EndRefused, Detail: contract.DetailState}, nil
	}
	sc, ok := m.sb.acquire(ctx.Done(), m.cfg.BusyWait)
	if !ok {
		return contract.Run{End: contract.EndBusy}, nil
	}
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage(`{}`)
	}
	a := sc.attach()
	defer sc.detach(a)
	r := &runner{
		m: m, sc: sc, a: a, pin: p, tree: tree,
		run: contract.Run{Isolation: sc.isolation},
		gw:  &gatewayClient{url: m.cfg.GatewayURL + "/mcp", token: runToken},
	}
	defer r.gw.close()
	if err := frame.Write(sc.c, frame.Job{Type: frame.TypeJob, Files: jobFiles(tree), Entry: p.manifest.Entry, Input: input}); err != nil {
		return contract.Run{End: contract.EndLimit, Detail: contract.DetailSandboxLost, Isolation: sc.isolation}, nil
	}
	wall := min(time.Duration(p.manifest.TimeoutS)*time.Second, maxWall)
	return r.loop(ctx, time.NewTimer(wall))
}

func jobFiles(t store.Tree) []frame.File {
	files := make([]frame.File, 0, len(t.Files))
	for _, f := range t.Files {
		files = append(files, frame.File{Path: f.Path, DataB64: base64.StdEncoding.EncodeToString(f.Data)})
	}
	return files
}

type runner struct {
	m    *Mediator
	sc   *sbConn
	a    *attachment
	pin  pinState
	tree store.Tree
	run  contract.Run
	out  []byte
	gw   *gatewayClient
}

func (r *runner) end(end, detail string) (contract.Run, []byte) {
	r.run.End, r.run.Detail = end, detail
	if end != contract.EndOK {
		return r.run, nil
	}
	r.run.BytesOut = len(r.out)
	return r.run, r.out
}

func (r *runner) loop(ctx context.Context, wall *time.Timer) (contract.Run, []byte) {
	defer wall.Stop()
	for {
		select {
		case f := <-r.a.frames:
			if res, out, done := r.handle(ctx, f, wall); done {
				return res, out
			}
		case <-wall.C:
			return r.end(contract.EndTimeout, "")
		case <-r.sc.lost:
			return r.end(contract.EndLimit, contract.DetailSandboxLost)
		case <-ctx.Done():
			return r.end(contract.EndInternal, "")
		}
	}
}

// handle verarbeitet einen Rahmen; done heißt: der Lauf ist zu Ende.
func (r *runner) handle(ctx context.Context, f inFrame, wall *time.Timer) (contract.Run, []byte, bool) {
	if f.err != nil {
		if errors.Is(f.err, frame.ErrTooLarge) {
			res, out := r.end(contract.EndLimit, contract.DetailFrame)
			return res, out, true
		}
		res, out := r.end(contract.EndLimit, contract.DetailSandboxLost)
		return res, out, true
	}
	typ, err := frame.TypeOf(f.payload)
	if err != nil {
		return contract.Run{}, nil, false
	}
	switch typ {
	case frame.TypeOutput:
		var o frame.Output
		if json.Unmarshal(f.payload, &o) != nil {
			return contract.Run{}, nil, false
		}
		r.out = append(r.out, o.Data...)
		if len(r.out) > contract.MaxOutputBytes {
			res, out := r.end(contract.EndLimit, contract.DetailOutput)
			return res, out, true
		}
	case frame.TypeToolCall:
		if r.run.ToolCalls >= contract.MaxToolCalls {
			res, out := r.end(contract.EndLimit, contract.DetailToolCalls)
			return res, out, true
		}
		r.run.ToolCalls++
		if res, out, done := r.toolCall(ctx, f.payload, wall); done {
			return res, out, true
		}
	case frame.TypeExited:
		var ex frame.Exited
		if json.Unmarshal(f.payload, &ex) != nil {
			res, out := r.end(contract.EndLimit, contract.DetailSandboxLost)
			return res, out, true
		}
		res, out := r.exited(ex)
		return res, out, true
	}
	return contract.Run{}, nil, false
}

// toolCall prüft die Manifest-Liste (M1, zweite Linie) und ruft sonst das
// Gateway mit dem Lauf-Token (Weg B). Die Wandzeit läuft weiter.
func (r *runner) toolCall(ctx context.Context, payload []byte, wall *time.Timer) (contract.Run, []byte, bool) {
	var tc frame.ToolCall
	if json.Unmarshal(payload, &tc) != nil {
		return contract.Run{}, nil, false
	}
	var result json.RawMessage
	if !slices.Contains(r.pin.manifest.Tools, tc.Name) {
		result = errorResult("Werkzeug nicht im Manifest deklariert.")
	} else {
		callCtx, cancel := context.WithCancel(ctx)
		done := make(chan json.RawMessage, 1)
		go func() { done <- r.gw.call(callCtx, tc.Name, tc.Arguments) }()
		select {
		case result = <-done:
			cancel()
		case <-wall.C:
			cancel()
			res, out := r.end(contract.EndTimeout, "")
			return res, out, true
		case <-r.sc.lost:
			cancel()
			res, out := r.end(contract.EndLimit, contract.DetailSandboxLost)
			return res, out, true
		}
	}
	if err := frame.Write(r.sc.c, frame.ToolResult{Type: frame.TypeToolResult, ID: tc.ID, Result: result}); err != nil {
		res, out := r.end(contract.EndLimit, contract.DetailSandboxLost)
		return res, out, true
	}
	return contract.Run{}, nil, false
}

// exited ordnet das Ende des Skripts einer Klasse zu. Ein SIGKILL, den der
// vermittler nicht ausgelöst hat, kommt vom OOM-Killer.
func (r *runner) exited(ex frame.Exited) (contract.Run, []byte) {
	switch {
	case ex.Signal == 9:
		return r.end(contract.EndLimit, contract.DetailMemory)
	case ex.Signal == 0 && ex.Code == 0:
		return r.end(contract.EndOK, "")
	case ex.ErrorType != "":
		r.run.ErrorType = contract.SanitizeErrorType(ex.ErrorType)
		r.run.ErrorAt = r.errorAt(ex.ErrorAt)
		return r.end(contract.EndError, contract.DetailException)
	}
	return r.end(contract.EndError, contract.DetailExit)
}

// errorAt lässt nur `<datei im Pin>:<zeile>` durch und setzt den Pin-Pfad
// davor; alles andere fällt weg (Vertrag §4, M6).
func (r *runner) errorAt(at string) string {
	i := strings.LastIndexByte(at, ':')
	if i <= 0 {
		return ""
	}
	rel, line := at[:i], at[i+1:]
	if n, err := strconv.Atoi(line); err != nil || n < 1 || len(line) > 9 || r.tree.File(rel) == nil {
		return ""
	}
	return r.pin.pin.Path + "/" + rel + ":" + line
}

func errorResult(text string) json.RawMessage {
	b, _ := json.Marshal(&mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}})
	return b
}

// gatewayClient ist Weg B: eine MCP-Sitzung je Lauf mit dem Lauf-Token.
type gatewayClient struct {
	url   string
	token string
	sess  *mcp.ClientSession
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func (g *gatewayClient) call(ctx context.Context, name string, args json.RawMessage) json.RawMessage {
	if g.sess == nil {
		client := mcp.NewClient(&mcp.Implementation{Name: "januaport-script-runner", Version: "1"}, nil)
		sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   g.url,
			HTTPClient: &http.Client{Transport: bearer{token: g.token}},
		}, nil)
		if err != nil {
			return errorResult("Werkzeugaufruf gescheitert.")
		}
		g.sess = sess
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	res, err := g.sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return errorResult("Werkzeugaufruf gescheitert.")
	}
	b, err := json.Marshal(res)
	if err != nil {
		return errorResult("Werkzeugaufruf gescheitert.")
	}
	return b
}

func (g *gatewayClient) close() {
	if g.sess != nil {
		_ = g.sess.Close()
	}
}
