package mediator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// ETL-Rohstrom über den vermittler (JanuaPort/januaport#1215, Paket P2,
// Vertragsfassung 3). Der Kern ist ein httptest-Stub; der Wächter ein
// fakeGuard, der die Rahmen etl_open/etl_next schickt und etl_chunk/etl_end liest.

const (
	etlCoreVersion = "0.100.0"
	// Kanarien: Sie dürfen nur auf dem Weg Kern → Skript auftauchen, in keinem
	// Log, keinem Fehler und keinem Feld des Lauf-Ergebnisses (SEC E5).
	rowCanary   = "ROWCANARY-7f3a91-private"
	errCanary   = "ERRBODYCANARY-55c2d0-private"
	tokenCanary = "jnptrun_TOKENCANARY0123456789abcdefghijklmnopqrstuv"
)

const etlManifest = `name: etl_job
description: Liest einen ETL-Strom.
entry: main.py
input_schema:
  type: object
tools:
  - kartei_find
  - etl_stream
timeout_s: 20
`

func newETLFixture(t *testing.T) *fixture {
	return newFixtureWith(t, func(f *fixture) {
		f.write("etl-job/jnpt-script.yaml", etlManifest)
		f.write("etl-job/main.py", "print('etl')\n")
	})
}

func newETLEnv(t *testing.T, version string, kern http.HandlerFunc, opts ...envOpt) *env {
	t.Helper()
	fx := newETLFixture(t)
	e := newEnvFx(t, fx, []pinRow{
		{Name: "etl_job", Path: "etl-job", Commit: fx.commit},
		{Name: "quick", Path: "quick", Commit: fx.commit},
	}, opts...)
	e.gw.setVersion(version)
	e.gw.setETL(kern)
	return e
}

func runnerContract(t *testing.T, e *env) int {
	t.Helper()
	b, err := json.Marshal(e.listTools())
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Meta map[string]struct {
			Contract int `json:"contract"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Meta[contract.MetaRunner].Contract
}

func (e *env) waitContract(want int) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runnerContract(e.t, e) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("tools/list meldet nicht contract %d (zuletzt %d)", want, runnerContract(e.t, e))
}

// --- Wächter-Seite -----------------------------------------------------------

type etlReply struct {
	typ  string
	raw  []byte
	data []byte // dekodiertes Stück
	end  frame.ETLEnd
}

func (g *fakeGuard) etlOpen(id int64, integration, query string, params any) {
	g.t.Helper()
	var raw json.RawMessage
	if params != nil {
		raw, _ = json.Marshal(params)
	}
	g.send(frame.ETLOpen{Type: frame.TypeETLOpen, ID: id, Integration: integration, QueryID: query, Params: raw})
}

func (g *fakeGuard) etlRead() etlReply {
	g.t.Helper()
	typ, raw, err := g.read()
	if err != nil {
		g.t.Errorf("etl-Antwort erwartet: %v", err)
		return etlReply{}
	}
	r := etlReply{typ: typ, raw: raw}
	switch typ {
	case frame.TypeETLChunk:
		var c frame.ETLChunk
		_ = json.Unmarshal(raw, &c)
		r.data, _ = base64.StdEncoding.DecodeString(c.DataB64)
	case frame.TypeETLEnd:
		_ = json.Unmarshal(raw, &r.end)
	}
	return r
}

func (g *fakeGuard) etlPull(id int64) etlReply {
	g.t.Helper()
	g.send(frame.ETLNext{Type: frame.TypeETLNext, ID: id})
	return g.etlRead()
}

func (g *fakeGuard) etlClose(id int64) etlReply {
	g.t.Helper()
	g.send(frame.ETLNext{Type: frame.TypeETLNext, ID: id, Close: true})
	return g.etlRead()
}

// etlDrain zieht, bis etl_end kommt, und liefert Rohdaten, Stückzahl und
// größtes Stück.
func (g *fakeGuard) etlDrain(id int64) (data []byte, chunks, maxChunk int, end frame.ETLEnd) {
	g.t.Helper()
	for {
		r := g.etlPull(id)
		switch r.typ {
		case frame.TypeETLChunk:
			chunks++
			maxChunk = max(maxChunk, len(r.data))
			data = append(data, r.data...)
		case frame.TypeETLEnd:
			return data, chunks, maxChunk, r.end
		default:
			g.t.Errorf("unerwarteter Rahmen %q", r.typ)
			return
		}
	}
}

// runScript führt einen Lauf von etl_job: Wächter-Goroutine mit script, danach
// exited(0) (außer script meldet selbst), dann der Aufruf wie vom Gateway.
func (e *env) runScript(script func(g *fakeGuard), limits ...string) (*mcp.CallToolResult, func()) {
	e.t.Helper()
	done := e.oneRun("etl", func(g *fakeGuard, _ frame.Job) {
		script(g)
		g.exited(0)
		_ = g.waitEOF()
	})
	e.waitReady("etl_job")
	e.waitContract(3)
	return e.callToken("etl_job", testRunToken, limits...), func() { <-done }
}

func (e *env) callToken(name, token string, limits ...string) *mcp.CallToolResult {
	e.t.Helper()
	h := runHeaders(e.fx.commit)
	h.Set(contract.HeaderRunToken, token)
	for _, l := range limits {
		h.Add(contract.HeaderRunLimits, l)
	}
	s, err := e.session(h)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
	if err != nil {
		e.t.Fatalf("CallTool: %v", err)
	}
	return res
}

func streamField(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	m := runMeta(t, res)
	s, ok := m["stream"].(map[string]any)
	if !ok {
		t.Fatalf("Feld stream fehlt: %v", m)
	}
	return s
}

func assertStream(t *testing.T, res *mcp.CallToolResult, status string, bytes, zeilen int64) {
	t.Helper()
	s := streamField(t, res)
	if s["status"] != status || s["bytes"] != float64(bytes) || s["zeilen"] != float64(zeilen) {
		t.Fatalf("stream = %v, want {%s %d %d}", s, status, bytes, zeilen)
	}
}

// --- Kern-Seite ----------------------------------------------------------------

func ndjsonRows(n int, grund string) []byte {
	var b bytes.Buffer
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `{"t":"row","d":{"nr":%d,"name":"zeile %d"}}`+"\n", i, i)
	}
	if grund != "" {
		fmt.Fprintf(&b, `{"t":"end","grund":%q,"zeilen":%d,"bytes":%d}`+"\n", grund, n, b.Len())
	}
	return b.Bytes()
}

func serveBody(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write(body)
	}
}

// openKern zählt Anfragen und meldet, wann der Kern merkt, dass die Anfrage
// geschlossen wurde (Kontext des Handlers endet).
type openKern struct {
	hits      atomic.Int64
	closed    chan struct{}
	started   chan struct{}
	once      sync.Once
	closeOnce sync.Once
	auth      atomic.Value
	path      atomic.Value
	query     atomic.Value
	body      atomic.Value
	written   atomic.Int64
}

func newOpenKern() *openKern {
	return &openKern{closed: make(chan struct{}), started: make(chan struct{})}
}

// handler streamt Zeilen, bis der Client schließt. perBlock Byte je Block.
func (k *openKern) handler(total int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		k.hits.Add(1)
		k.auth.Store(r.Header.Get("Authorization"))
		k.path.Store(r.URL.Path)
		k.query.Store(r.URL.RawQuery)
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		k.body.Store(string(b))
		k.once.Do(func() { close(k.started) })
		defer k.closeOnce.Do(func() { close(k.closed) })
		w.Header().Set("Content-Type", "application/x-ndjson")
		rc := http.NewResponseController(w)
		line := []byte(`{"t":"row","d":{"nr":1,"pad":"` + strings.Repeat("x", 100) + `"}}` + "\n")
		block := bytes.Repeat(line, 600) // ≈ 66 KiB
		for k.written.Load() < total {
			if _, err := w.Write(block); err != nil {
				return
			}
			k.written.Add(int64(len(block)))
			_ = rc.Flush()
			select {
			case <-r.Context().Done():
				return
			default:
			}
		}
		_, _ = w.Write([]byte(`{"t":"end","grund":"fertig","zeilen":0,"bytes":0}` + "\n"))
		<-r.Context().Done()
	}
}

func (k *openKern) waitClosed(t *testing.T, what string) {
	t.Helper()
	select {
	case <-k.closed:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s: der Kern bemerkt das Schließen der Anfrage nicht", what)
	}
}

// =============================================================================
// Pfad, Pull-Modell, Zähler
// =============================================================================

func TestETLStreamFertigHappyPath(t *testing.T) {
	body := ndjsonRows(12000, "fertig")
	e := newETLEnv(t, etlCoreVersion, serveBody(body))
	var got []byte
	var chunks, maxChunk int
	var end frame.ETLEnd
	res, wait := e.runScript(func(g *fakeGuard) {
		g.etlOpen(1, "erp_db", "offene_posten", map[string]any{"stichtag": "2026-11-18"})
		got, chunks, maxChunk, end = g.etlDrain(1)
		g.send(frame.Output{Type: frame.TypeOutput, Data: "ok\n"})
	})
	wait()
	if !bytes.Equal(got, body) {
		t.Fatalf("Strom verändert: %d statt %d Byte", len(got), len(body))
	}
	if end.Status != frame.ETLStatusEOF || end.ID != 1 {
		t.Fatalf("etl_end = %+v", end)
	}
	if maxChunk > frame.MaxETLChunkRaw {
		t.Fatalf("Stück %d > %d", maxChunk, frame.MaxETLChunkRaw)
	}
	if chunks < 2 {
		t.Fatalf("nur %d Stück; der Test braucht mehrere", chunks)
	}
	meta := assertEnd(t, res, contract.EndOK, "")
	assertStream(t, res, contract.StreamFertig, int64(len(body)), 12000)
	// Strom-Bytes zählen weder in bytes_out noch als Werkzeugaufruf.
	if meta["bytes_out"] != float64(len("ok\n")) || meta["tool_calls"] != float64(0) {
		t.Fatalf("bytes_out/tool_calls zählen den Strom: %v", meta)
	}
}

func TestETLRequestShape(t *testing.T) {
	k := newOpenKern()
	e := newETLEnv(t, etlCoreVersion, k.handler(1<<20))
	res, wait := e.runScript(func(g *fakeGuard) {
		g.etlOpen(1, "erp_db", "offene_posten", map[string]any{"stichtag": "2026-11-18", "n": 3})
		g.etlPull(1)
		g.etlClose(1)
	})
	wait()
	_ = res
	if k.path.Load() != "/api/etl/stream" || k.query.Load() != "" {
		t.Fatalf("Pfad/Query: %v ? %v", k.path.Load(), k.query.Load())
	}
	if k.auth.Load() != "Bearer "+testRunToken {
		t.Fatalf("Authorization = %v", k.auth.Load())
	}
	var sent struct {
		Integration string         `json:"integration"`
		QueryID     string         `json:"query_id"`
		Params      map[string]any `json:"params"`
	}
	if err := json.Unmarshal([]byte(k.body.Load().(string)), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Integration != "erp_db" || sent.QueryID != "offene_posten" || sent.Params["stichtag"] != "2026-11-18" {
		t.Fatalf("Anfragekörper: %+v", sent)
	}
	if strings.Contains(k.body.Load().(string), "jnptrun_") {
		t.Fatal("Token im Anfragekörper")
	}
}

// Pull: Ohne etl_next liest der vermittler nichts, nicht einmal die Anfrage
// geht hinaus; mit etl_next puffert er höchstens ein Stück.
func TestETLNoReadWithoutNext(t *testing.T) {
	k := newOpenKern()
	e := newETLEnv(t, etlCoreVersion, k.handler(64<<20))
	var hitsAfterOpen int64
	var written int64
	res, wait := e.runScript(func(g *fakeGuard) {
		g.etlOpen(1, "erp_db", "q", nil)
		time.Sleep(300 * time.Millisecond)
		hitsAfterOpen = k.hits.Load()
		r := g.etlPull(1)
		if r.typ != frame.TypeETLChunk || len(r.data) != frame.MaxETLChunkRaw {
			t.Errorf("erstes Stück: %s, %d Byte", r.typ, len(r.data))
		}
		// Kein Hintergrund-Lesen: Der Kern schreibt höchstens, was Sockelpuffer
		// und ein Stück fassen, nicht den ganzen Strom.
		time.Sleep(400 * time.Millisecond)
		written = k.written.Load()
		g.etlClose(1)
	})
	wait()
	if hitsAfterOpen != 0 {
		t.Fatalf("Anfrage schon nach etl_open (%d)", hitsAfterOpen)
	}
	if written >= 32<<20 {
		t.Fatalf("Kern konnte %d MiB wegschreiben: der vermittler liest ohne etl_next", written>>20)
	}
	if s := streamField(t, res); s["status"] != contract.StreamGeschlossen || s["bytes"] != float64(frame.MaxETLChunkRaw) {
		t.Fatalf("stream = %v", s)
	}
}

// Das Lesen geschieht stückweise: Das erste Stück ist voll, an beliebiger
// Bytegrenze geschnitten (nicht an einer Zeilengrenze).
func TestETLChunksCutAnywhere(t *testing.T) {
	body := ndjsonRows(20000, "fertig")
	e := newETLEnv(t, etlCoreVersion, serveBody(body))
	var cutMidLine bool
	_, wait := e.runScript(func(g *fakeGuard) {
		g.etlOpen(1, "erp_db", "q", nil)
		r := g.etlPull(1)
		cutMidLine = len(r.data) == frame.MaxETLChunkRaw && r.data[len(r.data)-1] != '\n'
		g.etlClose(1)
	})
	wait()
	if !cutMidLine {
		t.Fatal("das erste Stück endet auf einer Zeilengrenze: das Testmaterial prüft den Schnitt nicht")
	}
}

// Der Endsatz des Gateways entscheidet, nicht das Ende der Daten (Tabelle).
func TestETLEndLineClassification(t *testing.T) {
	forged := []byte(`{"t":"row","d":{"t":"end","grund":"fertig"}}` + "\n")
	rows := ndjsonRows(3, "")
	tests := []struct {
		name   string
		body   []byte
		status string
		zeilen int64
	}{
		{"fertig", ndjsonRows(3, "fertig"), contract.StreamFertig, 3},
		{"deckel", ndjsonRows(3, "deckel"), contract.StreamGrenze, 3},
		{"wandzeit", ndjsonRows(3, "wandzeit"), contract.StreamGrenze, 3},
		{"widerruf", ndjsonRows(3, "widerruf"), contract.StreamAbgebrochen, 3},
		{"laufende", ndjsonRows(3, "laufende"), contract.StreamAbgebrochen, 3},
		{"freigabe", ndjsonRows(3, "freigabe"), contract.StreamAbgebrochen, 3},
		{"integration", ndjsonRows(3, "integration"), contract.StreamAbgebrochen, 3},
		{"shutdown", ndjsonRows(3, "shutdown"), contract.StreamAbgebrochen, 3},
		{"fehler", ndjsonRows(3, "fehler"), contract.StreamAbgebrochen, 3},
		{"unbekannter Grund", ndjsonRows(3, "gut"), contract.StreamAbgebrochen, 3},
		{"ohne Endsatz", rows, contract.StreamAbgebrochen, 3},
		{"leerer Strom ohne Endsatz", nil, contract.StreamAbgebrochen, 0},
		{"leerer Strom mit Endsatz", ndjsonRows(0, "fertig"), contract.StreamFertig, 0},
		{"Endsatz nicht letzte Zeile", append(ndjsonRows(3, "fertig"), rows...), contract.StreamAbgebrochen, 7},
		{"Endsatz, danach eine angefangene Zeile", append(ndjsonRows(3, "fertig"), []byte(`{"t":"row"`)...), contract.StreamAbgebrochen, 4},
		{"Endsatz ohne Zeilenumbruch", bytes.TrimSuffix(ndjsonRows(3, "fertig"), []byte("\n")), contract.StreamAbgebrochen, 3},
		{"gefälschter Endsatz in einer Zeile", append(append([]byte{}, rows...), forged...), contract.StreamAbgebrochen, 4},
		{"Endsatz mit falschem Typ", append(append([]byte{}, rows...), []byte(`{"t":"endx","grund":"fertig"}`+"\n")...), contract.StreamAbgebrochen, 4},
		{"Endsatz ohne Grund", append(append([]byte{}, rows...), []byte(`{"t":"end"}`+"\n")...), contract.StreamAbgebrochen, 4},
		{"Endsatz mit Zahl als Grund", append(append([]byte{}, rows...), []byte(`{"t":"end","grund":1}`+"\n")...), contract.StreamAbgebrochen, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newETLEnv(t, etlCoreVersion, serveBody(tt.body))
			res, wait := e.runScript(func(g *fakeGuard) {
				g.etlOpen(1, "erp_db", "q", nil)
				g.etlDrain(1)
			})
			wait()
			assertEnd(t, res, contract.EndOK, "")
			s := streamField(t, res)
			if s["status"] != tt.status || s["bytes"] != float64(len(tt.body)) || s["zeilen"] != float64(tt.zeilen) {
				t.Fatalf("stream = %v, want {%s %d %d}", s, tt.status, len(tt.body), tt.zeilen)
			}
		})
	}
}

// Ein Strom, den der Kern mitten in der Zeile abreißt, ist abgebrochen.
func TestETLConnectionCutMidStream(t *testing.T) {
	e := newETLEnv(t, etlCoreVersion, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write(ndjsonRows(100, ""))
		_, _ = w.Write([]byte(`{"t":"row","d":{"nr":`))
		panic(http.ErrAbortHandler)
	})
	var end frame.ETLEnd
	res, wait := e.runScript(func(g *fakeGuard) {
		g.etlOpen(1, "erp_db", "q", nil)
		_, _, _, end = g.etlDrain(1)
	})
	wait()
	if end.Status != frame.ETLStatusAbgebrochen {
		t.Fatalf("etl_end = %+v, want abgebrochen", end)
	}
	assertEnd(t, res, contract.EndOK, "")
	if s := streamField(t, res); s["status"] != contract.StreamAbgebrochen {
		t.Fatalf("stream = %v", s)
	}
}

// =============================================================================
// Abbruch: Wandzeit, Sandbox, Kontext, Skriptende, close schließen die Anfrage
// =============================================================================

func TestETLCloseClosesRequest(t *testing.T) {
	k := newOpenKern()
	e := newETLEnv(t, etlCoreVersion, k.handler(1<<40))
	var reply etlReply
	res, wait := e.runScript(func(g *fakeGuard) {
		g.etlOpen(1, "erp_db", "q", nil)
		g.etlPull(1)
		reply = g.etlClose(1)
	})
	wait()
	if reply.typ != frame.TypeETLEnd || reply.end.Status != frame.ETLStatusGeschlossen {
		t.Fatalf("Antwort auf close: %s %+v", reply.typ, reply.end)
	}
	k.waitClosed(t, "close")
	assertEnd(t, res, contract.EndOK, "")
	if s := streamField(t, res); s["status"] != contract.StreamGeschlossen {
		t.Fatalf("stream = %v", s)
	}
}

func TestETLScriptEndClosesRequest(t *testing.T) {
	k := newOpenKern()
	e := newETLEnv(t, etlCoreVersion, k.handler(1<<40))
	res, wait := e.runScript(func(g *fakeGuard) {
		g.etlOpen(1, "erp_db", "q", nil)
		g.etlPull(1)
		// Skriptende ohne close
	})
	wait()
	k.waitClosed(t, "Skriptende")
	assertEnd(t, res, contract.EndOK, "")
	if s := streamField(t, res); s["status"] != contract.StreamGeschlossen {
		t.Fatalf("stream = %v", s)
	}
}

// Wer den ganzen Strom samt Endsatz gelesen hat und dann endet, hat fertig
// gelesen: auch ohne weiteres etl_next bis zum EOF.
func TestETLScriptEndAfterEndLineIsFertig(t *testing.T) {
	body := ndjsonRows(10, "fertig")
	e := newETLEnv(t, etlCoreVersion, serveBody(body))
	res, wait := e.runScript(func(g *fakeGuard) {
		g.etlOpen(1, "erp_db", "q", nil)
		r := g.etlPull(1)
		if !bytes.Equal(r.data, body) {
			t.Errorf("Stück %d Byte", len(r.data))
		}
	})
	wait()
	assertStream(t, res, contract.StreamFertig, int64(len(body)), 10)
}

func TestETLWallTimeClosesRequest(t *testing.T) {
	k := newOpenKern()
	e := newETLEnv(t, etlCoreVersion, k.handler(1<<40), func(c *Config) {
		c.NewWallTimer = func(time.Duration) *time.Timer { return time.NewTimer(400 * time.Millisecond) }
	})
	done := e.oneRun("etl", func(g *fakeGuard, _ frame.Job) {
		g.etlOpen(1, "erp_db", "q", nil)
		g.etlPull(1)
		_ = g.waitEOF()
	})
	e.waitReady("etl_job")
	e.waitContract(3)
	res := e.mustCall("etl_job", map[string]any{})
	<-done
	assertEnd(t, res, contract.EndTimeout, "")
	k.waitClosed(t, "Wandzeit")
	if s := streamField(t, res); s["status"] != contract.StreamAbgebrochen {
		t.Fatalf("stream = %v", s)
	}
}

// Eine Wandzeit, die mitten im Warten auf den Kern zuschlägt (Kern liefert
// keinen Byte): Der Lauf endet trotzdem pünktlich.
func TestETLWallTimeWhileWaitingForKern(t *testing.T) {
	released := make(chan struct{})
	t.Cleanup(func() { close(released) })
	closed := make(chan struct{})
	e := newETLEnv(t, etlCoreVersion, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		http.NewResponseController(w).Flush()
		select {
		case <-r.Context().Done():
			close(closed)
		case <-released:
		}
	}, func(c *Config) {
		c.NewWallTimer = func(time.Duration) *time.Timer { return time.NewTimer(400 * time.Millisecond) }
	})
	done := e.oneRun("etl", func(g *fakeGuard, _ frame.Job) {
		g.etlOpen(1, "erp_db", "q", nil)
		g.send(frame.ETLNext{Type: frame.TypeETLNext, ID: 1})
		_ = g.waitEOF()
	})
	e.waitReady("etl_job")
	e.waitContract(3)
	start := time.Now()
	res := e.mustCall("etl_job", map[string]any{})
	<-done
	assertEnd(t, res, contract.EndTimeout, "")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Lauf endet erst nach %v", d)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Anfrage nicht geschlossen")
	}
}

func TestETLSandboxLostClosesRequest(t *testing.T) {
	k := newOpenKern()
	e := newETLEnv(t, etlCoreVersion, k.handler(1<<40))
	done := e.oneRun("etl", func(g *fakeGuard, _ frame.Job) {
		g.etlOpen(1, "erp_db", "q", nil)
		g.etlPull(1)
		g.send(frame.ETLNext{Type: frame.TypeETLNext, ID: 1})
		_ = g.conn.Close() // Sandbox verloren, während ein Stück ansteht
	})
	e.waitReady("etl_job")
	e.waitContract(3)
	res := e.mustCall("etl_job", map[string]any{})
	<-done
	assertEnd(t, res, contract.EndLimit, contract.DetailSandboxLost)
	k.waitClosed(t, "Sandbox verloren")
	if s := streamField(t, res); s["status"] != contract.StreamAbgebrochen {
		t.Fatalf("stream = %v", s)
	}
}

func TestETLRunContextClosesRequest(t *testing.T) {
	k := newOpenKern()
	e := newETLEnv(t, etlCoreVersion, k.handler(1<<40))
	started := make(chan struct{})
	done := e.oneRun("etl", func(g *fakeGuard, _ frame.Job) {
		g.etlOpen(1, "erp_db", "q", nil)
		g.etlPull(1)
		close(started)
		_ = g.waitEOF()
	})
	e.waitReady("etl_job")
	e.waitContract(3)
	s, err := e.session(runHeaders(e.fx.commit))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = s.CallTool(ctx, &mcp.CallToolParams{Name: "etl_job", Arguments: map[string]any{}}) }()
	<-started
	cancel()
	k.waitClosed(t, "Kontext des Laufs")
	<-done
}

// Ein Skript, das etl_next schickt und die Antwort nie liest, darf den Lauf
// nicht über die Wandzeit hinaus halten (Schreibfrist am Sockel).
func TestETLScriptThatNeverReadsCannotPastWall(t *testing.T) {
	k := newOpenKern()
	e := newETLEnv(t, etlCoreVersion, k.handler(1<<40), func(c *Config) {
		c.NewWallTimer = func(time.Duration) *time.Timer { return time.NewTimer(500 * time.Millisecond) }
	})
	done := e.oneRun("etl", func(g *fakeGuard, _ frame.Job) {
		g.etlOpen(1, "erp_db", "q", nil)
		for i := 0; i < 8; i++ {
			g.send(frame.ETLNext{Type: frame.TypeETLNext, ID: 1}) // liest keine Antwort
			time.Sleep(20 * time.Millisecond)
		}
		_ = g.waitEOF()
	})
	e.waitReady("etl_job")
	e.waitContract(3)
	start := time.Now()
	res := e.mustCall("etl_job", map[string]any{})
	<-done
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Lauf %v über die Wandzeit gehalten", d)
	}
	if m := runMeta(t, res); m["end"] == contract.EndOK {
		t.Fatalf("Lauf ok trotz Protokollbruch: %v", m)
	}
}

// =============================================================================
// Manifest, ein Strom je Lauf, Grenzen, Fassung
// =============================================================================

func TestETLRefusedWithoutManifestDeclaration(t *testing.T) {
	k := newOpenKern()
	e := newETLEnv(t, etlCoreVersion, k.handler(1<<20))
	done := e.oneRun("etl", func(g *fakeGuard, _ frame.Job) {
		g.etlOpen(1, "erp_db", "q", nil)
		r := g.etlPull(1)
		if r.typ != frame.TypeETLEnd || r.end.Status != frame.ETLStatusAbgewiesen {
			t.Errorf("Antwort: %s %+v", r.typ, r.end)
		}
		g.exited(0)
		_ = g.waitEOF()
	})
	e.waitReady("quick") // Manifest von quick deklariert etl_stream nicht
	e.waitContract(3)
	res := e.mustCall("quick", map[string]any{})
	<-done
	if k.hits.Load() != 0 {
		t.Fatal("Anfrage an den Kern ohne etl_stream im Manifest")
	}
	assertEnd(t, res, contract.EndOK, "")
	if s := streamField(t, res); s["status"] != contract.StreamAbgewiesen {
		t.Fatalf("stream = %v", s)
	}
}

func TestETLSecondStreamRefused(t *testing.T) {
	for _, tt := range []struct {
		name  string
		first func(g *fakeGuard)
	}{
		{"erster noch offen", func(g *fakeGuard) { g.etlPull(1) }},
		{"erster schon fertig", func(g *fakeGuard) { g.etlDrain(1) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int64
			body := ndjsonRows(5, "fertig")
			e := newETLEnv(t, etlCoreVersion, func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				serveBody(body)(w, r)
			})
			var second etlReply
			var firstOK bool
			res, wait := e.runScript(func(g *fakeGuard) {
				g.etlOpen(1, "erp_db", "q", nil)
				tt.first(g)
				g.etlOpen(2, "erp_db", "q2", nil)
				second = g.etlPull(2)
				if tt.name == "erster noch offen" {
					// Der erste Strom läuft weiter.
					r := g.etlPull(1)
					firstOK = r.typ == frame.TypeETLEnd && r.end.Status == frame.ETLStatusEOF
				}
			})
			wait()
			if second.typ != frame.TypeETLEnd || second.end.Status != frame.ETLStatusAbgewiesen || second.end.ID != 2 {
				t.Fatalf("zweiter Strom: %s %+v", second.typ, second.end)
			}
			if hits.Load() != 1 {
				t.Fatalf("Anfragen an den Kern: %d, want 1", hits.Load())
			}
			if tt.name == "erster noch offen" && !firstOK {
				t.Fatal("der erste Strom wurde vom zweiten Versuch gestört")
			}
			// Ein abgewiesener Versuch macht den Lauf nie „fertig“.
			assertEnd(t, res, contract.EndOK, "")
			if s := streamField(t, res); s["status"] != contract.StreamAbgewiesen {
				t.Fatalf("stream = %v, want abgewiesen", s)
			}
		})
	}
}

func TestETLNextWithoutOpenOrWrongID(t *testing.T) {
	e := newETLEnv(t, etlCoreVersion, serveBody(ndjsonRows(1, "fertig")))
	var a, b etlReply
	res, wait := e.runScript(func(g *fakeGuard) {
		a = g.etlPull(5) // nie geöffnet
		g.etlOpen(1, "erp_db", "q", nil)
		b = g.etlPull(9) // falsche ID
	})
	wait()
	for _, r := range []etlReply{a, b} {
		if r.typ != frame.TypeETLEnd || r.end.Status != frame.ETLStatusVertrag {
			t.Fatalf("Antwort: %s %+v", r.typ, r.end)
		}
	}
	assertEnd(t, res, contract.EndOK, "")
	if s := streamField(t, res); s["status"] == contract.StreamFertig {
		t.Fatalf("stream = %v", s)
	}
}

func TestETLOpenValidation(t *testing.T) {
	big := map[string]any{"x": strings.Repeat("a", frame.MaxETLOpen)}
	tests := []struct {
		name   string
		open   func(g *fakeGuard)
		status string
	}{
		{"über 64 KiB", func(g *fakeGuard) { g.etlOpen(1, "erp_db", "q", big) }, frame.ETLStatusGrenze},
		{"ohne Integration", func(g *fakeGuard) { g.etlOpen(1, "", "q", nil) }, frame.ETLStatusVertrag},
		{"ohne query_id", func(g *fakeGuard) { g.etlOpen(1, "erp_db", "", nil) }, frame.ETLStatusVertrag},
		{"Parameter keine Objekt-Form", func(g *fakeGuard) {
			g.send(map[string]any{"type": "etl_open", "id": 1, "integration": "erp_db", "query_id": "q", "params": []int{1}})
		}, frame.ETLStatusVertrag},
		{"Integration zu lang", func(g *fakeGuard) { g.etlOpen(1, strings.Repeat("i", 300), "q", nil) }, frame.ETLStatusVertrag},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := newOpenKern()
			e := newETLEnv(t, etlCoreVersion, k.handler(1<<20))
			var r etlReply
			res, wait := e.runScript(func(g *fakeGuard) {
				tt.open(g)
				r = g.etlPull(1)
			})
			wait()
			if r.typ != frame.TypeETLEnd || r.end.Status != tt.status {
				t.Fatalf("Antwort: %s %+v, want %s", r.typ, r.end, tt.status)
			}
			if k.hits.Load() != 0 {
				t.Fatal("Anfrage trotz ungültigem etl_open")
			}
			assertEnd(t, res, contract.EndOK, "")
			if s := streamField(t, res); s["status"] != tt.status {
				t.Fatalf("stream = %v, want %s", s, tt.status)
			}
		})
	}
}

// etl_open ist kein Werkzeugaufruf; auch Strom-Bytes zählen nicht gegen die
// Ausgabegrenze.
func TestETLNotCountedAsToolCallNorOutput(t *testing.T) {
	body := ndjsonRows(3000, "fertig")
	e := newETLEnv(t, etlCoreVersion, serveBody(body))
	res, wait := e.runScript(func(g *fakeGuard) {
		g.etlOpen(1, "erp_db", "q", nil)
		g.etlDrain(1)
		g.send(frame.ToolCall{Type: frame.TypeToolCall, ID: 1, Name: "kartei_find", Arguments: json.RawMessage(`{}`)})
		g.read()
		g.send(frame.Output{Type: frame.TypeOutput, Data: "ok"})
	}, "wall_s=25;output_bytes=10;tool_calls=1")
	wait()
	meta := assertEnd(t, res, contract.EndOK, "")
	if meta["tool_calls"] != float64(1) || meta["bytes_out"] != float64(2) {
		t.Fatalf("meta = %v", meta)
	}
	assertStream(t, res, contract.StreamFertig, int64(len(body)), 3000)
}

// etl_stream ist ein Strom-Recht, nie ein Werkzeug: Ein Aufruf als Werkzeug
// geht nicht an das Gateway (zweite Linie zu E2 im Kern).
func TestETLStreamScopeIsNeverAToolCall(t *testing.T) {
	e := newETLEnv(t, etlCoreVersion, serveBody(nil))
	var result string
	_, wait := e.runScript(func(g *fakeGuard) {
		g.send(frame.ToolCall{Type: frame.TypeToolCall, ID: 1, Name: "etl_stream", Arguments: json.RawMessage(`{}`)})
		_, raw, _ := g.read()
		result = string(raw)
	})
	wait()
	if n := len(e.gw.authHeaders()); n != 0 {
		t.Fatalf("etl_stream als Werkzeug ans Gateway gegangen (%d Anfragen)", n)
	}
	if !strings.Contains(result, "isError") {
		t.Fatalf("Antwort: %s", result)
	}
}

// =============================================================================
// Rückfall auf Fassung 2 (E4)
// =============================================================================

func TestContractFollowsCoreVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		health  http.HandlerFunc
		want    int
	}{
		{"neu", "0.100.0", nil, 3},
		{"neu mit v", "v0.100.1", nil, 3},
		{"alt", "0.99.0", nil, 2},
		{"dev", "dev", nil, 2},
		{"Vorabversion", "0.100.0-rc1", nil, 2},
		{"Kern ohne healthz", "", nil, 2},
		{"kein JSON", "", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<html>0.100.0</html>") }, 2},
		{"Version ist eine Zahl", "", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"status":"ok","version":100}`)
		}, 2},
		{"Version fehlt", "", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"status":"ok"}`) }, 2},
		{"Fehlerstatus mit neuer Version", "", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"version":"0.100.0"}`)
		}, 2},
		{"riesige Antwort", "", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"pad":"`+strings.Repeat("a", 1<<20)+`","version":"0.100.0"}`)
		}, 2},
		{"Umleitung wird nicht gefolgt", "", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/anderswo", http.StatusTemporaryRedirect)
		}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newETLEnv(t, tt.version, nil)
			if tt.health != nil {
				e.gw.setHealth(tt.health)
			}
			time.Sleep(150 * time.Millisecond) // mehrere Probe-Takte
			if got := runnerContract(t, e); got != tt.want {
				t.Fatalf("contract = %d, want %d", got, tt.want)
			}
			if tt.want == 3 {
				e.waitContract(3)
			}
		})
	}
}

func TestContractAdaptsWhenCoreIsUpdated(t *testing.T) {
	e := newETLEnv(t, "0.99.0", nil)
	e.waitContract(2)
	e.gw.setVersion("0.100.0")
	e.waitContract(3)
	e.gw.setVersion("kaputt")
	e.waitContract(2)
}

// Unter Fassung 2 gibt es keinen Strom: kein Kern-Aufruf, Antwort „vertrag“.
func TestETLUnavailableUnderContract2(t *testing.T) {
	k := newOpenKern()
	e := newETLEnv(t, "0.99.0", k.handler(1<<20))
	var r etlReply
	done := e.oneRun("etl", func(g *fakeGuard, _ frame.Job) {
		g.etlOpen(1, "erp_db", "q", nil)
		r = g.etlPull(1)
		g.exited(0)
		_ = g.waitEOF()
	})
	e.waitReady("etl_job")
	e.waitContract(2)
	res := e.mustCall("etl_job", map[string]any{})
	<-done
	if r.typ != frame.TypeETLEnd || r.end.Status != frame.ETLStatusVertrag {
		t.Fatalf("Antwort: %s %+v", r.typ, r.end)
	}
	if k.hits.Load() != 0 {
		t.Fatal("Anfrage unter Fassung 2")
	}
	if s := streamField(t, res); s["status"] != contract.StreamVertrag {
		t.Fatalf("stream = %v", s)
	}
}

// Ein Lauf ohne Strom trägt kein Feld stream: das Altverhalten bleibt.
func TestRunWithoutStreamHasNoStreamFieldAtAdapter(t *testing.T) {
	e := newETLEnv(t, etlCoreVersion, serveBody(nil))
	res, wait := e.runScript(func(g *fakeGuard) {})
	wait()
	if _, ok := runMeta(t, res)["stream"]; ok {
		t.Fatalf("Feld stream ohne Strom: %v", runMeta(t, res))
	}
}

// =============================================================================
// Abweisungen des Kerns, feste Klassen (E5)
// =============================================================================

func TestETLRejectionsFixedClasses(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		retryHeader string
		ctype       string
		wantStatus  string
		wantCode    int
		wantRetry   int
	}{
		{"409 Deckel", 409, "", "application/json", frame.ETLStatusAbgewiesen, 409, 0},
		{"429 belegt mit Wartezeit", 429, "12", "application/json", frame.ETLStatusAbgewiesen, 429, 12},
		{"429 ohne Wartezeit", 429, "", "application/json", frame.ETLStatusAbgewiesen, 429, 0},
		{"429 Wartezeit kein Zahlenwert", 429, "Wed, 21 Oct 2026 07:28:00 GMT", "application/json", frame.ETLStatusAbgewiesen, 429, 0},
		{"429 Wartezeit negativ", 429, "-5", "application/json", frame.ETLStatusAbgewiesen, 429, 0},
		{"429 Wartezeit riesig", 429, "99999999999", "application/json", frame.ETLStatusAbgewiesen, 429, 0},
		{"503 Audit", 503, "30", "application/json", frame.ETLStatusAbgewiesen, 503, 30},
		{"401", 401, "", "application/json", frame.ETLStatusAbgewiesen, 401, 0},
		{"403", 403, "", "application/json", frame.ETLStatusAbgewiesen, 403, 0},
		{"404", 404, "", "application/json", frame.ETLStatusAbgewiesen, 404, 0},
		{"400", 400, "", "application/json", frame.ETLStatusAbgewiesen, 400, 0},
		{"500 ist kein Vertrag", 500, "", "text/plain", frame.ETLStatusVertrag, 0, 0},
		{"502", 502, "", "text/html", frame.ETLStatusVertrag, 0, 0},
		{"200 mit falschem Inhaltstyp", 200, "", "text/html", frame.ETLStatusVertrag, 0, 0},
		{"204", 204, "", "", frame.ETLStatusVertrag, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newETLEnv(t, etlCoreVersion, func(w http.ResponseWriter, _ *http.Request) {
				if tt.retryHeader != "" {
					w.Header().Set("Retry-After", tt.retryHeader)
				}
				if tt.ctype != "" {
					w.Header().Set("Content-Type", tt.ctype)
				}
				w.WriteHeader(tt.status)
				if tt.status != 204 {
					_, _ = io.WriteString(w, `{"error":{"code":"deckel","message":"`+errCanary+`"}}`)
				}
			})
			var r etlReply
			res, wait := e.runScript(func(g *fakeGuard) {
				g.etlOpen(1, "erp_db", "q", nil)
				r = g.etlPull(1)
			})
			wait()
			if r.typ != frame.TypeETLEnd || r.end.Status != tt.wantStatus || r.end.Code != tt.wantCode || r.end.RetryAfterS != tt.wantRetry {
				t.Fatalf("etl_end = %s %+v", r.typ, r.end)
			}
			// Nur Code und Zahl, nie Text des Kerns.
			if strings.Contains(string(r.raw), errCanary) {
				t.Fatalf("Fehlertext des Kerns im Rahmen: %s", r.raw)
			}
			b, _ := json.Marshal(res)
			if strings.Contains(string(b), errCanary) || strings.Contains(e.logs.String(), errCanary) {
				t.Fatal("Fehlertext des Kerns im Ergebnis oder im Log")
			}
			assertEnd(t, res, contract.EndOK, "")
			if s := streamField(t, res); s["status"] != tt.wantStatus || s["bytes"] != float64(0) || s["zeilen"] != float64(0) {
				t.Fatalf("stream = %v", s)
			}
		})
	}
}

// Redirect: Der vermittler folgt nie. Ein zweiter Host bekommt keinen Request,
// weder mit dem Authorization-Header noch ohne.
func TestETLRedirectNeverFollowed(t *testing.T) {
	var second atomic.Int64
	var secondAuth atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		second.Add(1)
		secondAuth.Store(r.Header.Get("Authorization"))
		serveBody(ndjsonRows(1, "fertig"))(w, r)
	}))
	t.Cleanup(other.Close)
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			second.Store(0)
			e := newETLEnv(t, etlCoreVersion, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, other.URL+"/api/etl/stream", code)
			})
			var r etlReply
			res, wait := e.runScript(func(g *fakeGuard) {
				g.etlOpen(1, "erp_db", "q", nil)
				r = g.etlPull(1)
			})
			wait()
			if second.Load() != 0 {
				t.Fatalf("zweiter Host bekam %d Request(s), Authorization=%v", second.Load(), secondAuth.Load())
			}
			if r.typ != frame.TypeETLEnd || r.end.Status != frame.ETLStatusVertrag {
				t.Fatalf("etl_end = %s %+v", r.typ, r.end)
			}
			if s := streamField(t, res); s["status"] != contract.StreamVertrag {
				t.Fatalf("stream = %v", s)
			}
		})
	}
}

// Der Kern ist beim Öffnen des Stroms unerreichbar: feste Klasse, kein
// Fehlertext (die Adresse steckt im Fehler des http-Clients). Die Fassung gilt
// ab Laufbeginn, ein späterer Wechsel der Kern-Version ändert den Lauf nicht.
func TestETLKernUnreachable(t *testing.T) {
	e := newETLEnv(t, etlCoreVersion, serveBody(nil))
	var r etlReply
	addr := strings.TrimPrefix(e.gw.srv.URL, "http://")
	res, wait := e.runScript(func(g *fakeGuard) {
		e.gw.srv.Close()
		g.etlOpen(1, "erp_db", "q", nil)
		r = g.etlPull(1)
	})
	wait()
	if r.typ != frame.TypeETLEnd || r.end.Status != frame.ETLStatusAbgebrochen {
		t.Fatalf("etl_end = %s %+v", r.typ, r.end)
	}
	if strings.Contains(string(r.raw), addr) {
		t.Fatalf("Adresse im Rahmen: %s", r.raw)
	}
	if b, _ := json.Marshal(res); strings.Contains(string(b), addr) || strings.Contains(e.logs.String(), addr) {
		t.Fatal("Adresse im Ergebnis oder Log")
	}
	if s := streamField(t, res); s["status"] != contract.StreamAbgebrochen {
		t.Fatalf("stream = %v", s)
	}
}

// =============================================================================
// Kanarien (E5): Token und Zeilen
// =============================================================================

func TestETLCanaries(t *testing.T) {
	body := ndjsonRows(50, "")
	body = append(body, []byte(`{"t":"row","d":{"geheim":"`+rowCanary+`"}}`+"\n")...)
	body = append(body, []byte(`{"t":"end","grund":"fertig","zeilen":51,"bytes":1}`+"\n")...)
	var gotAuth, gotURL atomic.Value
	e := newETLEnv(t, etlCoreVersion, func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		gotURL.Store(r.URL.String())
		serveBody(body)(w, r)
	})
	var stream []byte
	done := e.oneRun("etl", func(g *fakeGuard, _ frame.Job) {
		g.etlOpen(1, "erp_db", "q", map[string]any{"a": "b"})
		stream, _, _, _ = g.etlDrain(1)
		g.send(map[string]any{"type": "log", "data": "gelesen\n"}) // eigenes Log des Skripts ist erlaubt
		g.exited(0)
		_ = g.waitEOF()
	})
	e.waitReady("etl_job")
	e.waitContract(3)
	res := e.callToken("etl_job", tokenCanary)
	<-done
	if gotAuth.Load() != "Bearer "+tokenCanary {
		t.Fatalf("Authorization = %v", gotAuth.Load())
	}
	if strings.Contains(gotURL.Load().(string), "jnptrun") || strings.Contains(gotURL.Load().(string), "?") {
		t.Fatalf("Token oder Query in der URL: %v", gotURL.Load())
	}
	if !bytes.Contains(stream, []byte(rowCanary)) {
		t.Fatal("die Zeilen-Kanarie erreicht das Skript nicht: Testmaterial prüft nichts")
	}
	b, _ := json.Marshal(res)
	for _, canary := range []string{rowCanary, tokenCanary, "TOKENCANARY"} {
		if strings.Contains(string(b), canary) {
			t.Fatalf("%q im Lauf-Ergebnis: %s", canary, b)
		}
		if strings.Contains(e.logs.String(), canary) {
			t.Fatalf("%q im Log des vermittlers:\n%s", canary, e.logs.String())
		}
	}
	// Das Dienst-Log zählt nur: Status und Zähler, keine Inhalte.
	if !strings.Contains(e.logs.String(), "stream=fertig") {
		t.Fatalf("Log ohne Strom-Zähler:\n%s", e.logs.String())
	}
	assertStream(t, res, contract.StreamFertig, int64(len(body)), 51)
}
