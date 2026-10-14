package mediator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
	"github.com/januaport/januaport-plugins/script-runner/internal/ticker"
)

// #1039 S1: der Taktgeber im vermittler. Die Uhr ist injiziert; gewartet
// wird nur auf das asynchrone Ergebnis, nie auf eine echte Uhrzeit.

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func clockAt(h, m, s int) time.Time { return time.Date(2026, 10, 12, h, m, s, 0, time.UTC) }

type coreRequest struct {
	method, path, auth string
	bodyLen            int
	contentLength      int64
}

// fakeCore ist die Auslöse-Route des Gateways.
type fakeCore struct {
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []coreRequest
	handle func(w http.ResponseWriter, r *http.Request)
}

func newFakeCore(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *fakeCore {
	t.Helper()
	c := &fakeCore{handle: handle}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.reqs = append(c.reqs, coreRequest{r.Method, r.URL.Path, r.Header.Get("Authorization"), len(body), r.ContentLength})
		c.mu.Unlock()
		c.handle(w, r)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *fakeCore) requests() []coreRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]coreRequest(nil), c.reqs...)
}

func answer(end string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, "{\"end\":%q}\n", end)
	}
}

func withGateway(url string) envOpt             { return func(c *Config) { c.GatewayURL = url } }
func withClock(clk *fakeClock) envOpt           { return func(c *Config) { c.Now = clk.Now } }
func withTriggerTimeout(d time.Duration) envOpt { return func(c *Config) { c.TriggerTimeout = d } }

func scheduleRow(name, pin, commit, cron, zone string, enabled bool) string {
	return fmt.Sprintf(`{"name":%q,"pin":%q,"commit":%q,"cron":%q,"timezone":%q,"enabled":%t}`, name, pin, commit, cron, zone, enabled)
}

func (e *env) writeSchedulesRaw(data string) {
	e.t.Helper()
	tmp := filepath.Join(e.keys, "schedules.json.tmp")
	if err := os.WriteFile(tmp, []byte(data), 0o600); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(e.keys, "schedules.json")); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) writeSchedules(rows ...string) {
	e.writeSchedulesRaw(`{"version":1,"schedules":[` + strings.Join(rows, ",") + `]}`)
}

// schedules liest _meta["januaport.ai/runner"].schedules wie das Gateway.
func (e *env) schedules() ([]contract.ScheduleStatus, bool) {
	e.t.Helper()
	raw, _ := json.Marshal(e.listTools().Meta[contract.MetaRunner])
	var runner map[string]json.RawMessage
	_ = json.Unmarshal(raw, &runner)
	list, ok := runner["schedules"]
	if !ok {
		return nil, false
	}
	var out []contract.ScheduleStatus
	if err := json.Unmarshal(list, &out); err != nil {
		e.t.Fatal(err)
	}
	return out, true
}

func (e *env) schedule(name string) (contract.ScheduleStatus, bool) {
	list, _ := e.schedules()
	for _, s := range list {
		if s.Name == name {
			return s, true
		}
	}
	return contract.ScheduleStatus{}, false
}

func waitUntil(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Zeitüberschreitung: %s", what)
}

// scheduled startet einen vermittler mit bereitem Pin „quick“, Fake-Wächter
// und dem Zeitplan „q“ (täglich 08:00 UTC), den der Taktgeber um 07:59:50
// zum ersten Mal sieht.
func scheduled(t *testing.T, core *fakeCore, opts ...envOpt) (*env, *fakeClock, *fakeGuard) {
	t.Helper()
	clk := &fakeClock{t: clockAt(7, 59, 50)}
	e := newEnv(t, nil, append([]envOpt{withGateway(core.srv.URL), withClock(clk)}, opts...)...)
	e.writePins(e.quickPin())
	g := dialGuard(t, e.sock, "a1", 2)
	e.waitReady("quick")
	e.writeSchedules(scheduleRow("q", "quick", e.fx.commit, "0 8 * * *", "UTC", true))
	waitUntil(t, 5*time.Second, "Zeitplan q bereit", func() bool {
		s, ok := e.schedule("q")
		return ok && s.State == contract.ScheduleReady && s.NextRun == "2026-10-12T08:00:00Z"
	})
	return e, clk, g
}

// Invariante 1 (V1): Der Taktgeber hält keinen Slot. Der Kern startet den
// Lauf über Weg A, während der Auslöse-Aufruf noch offen ist; hielte der
// Taktgeber den Slot, bekäme Weg A nach BusyWait nur `busy`.
func TestTickerHoldsNoSlotWhileGatewayRunsViaPathA(t *testing.T) {
	var e *env
	ready := make(chan struct{})
	core := newFakeCore(t, func(w http.ResponseWriter, r *http.Request) {
		<-ready
		res, err := e.call("quick", map[string]any{}, e.fx.commit)
		end := "internal"
		if err == nil {
			var run contract.Run
			raw, _ := json.Marshal(res.Meta[contract.MetaRun])
			if json.Unmarshal(raw, &run) == nil && run.End != "" {
				end = run.End
			}
		}
		answer(end)(w, r)
	})
	var clk *fakeClock
	var g *fakeGuard
	e, clk, g = scheduled(t, core, withBusyWait(500*time.Millisecond))
	close(ready)
	jobs := make(chan frame.Job, 1)
	go func() {
		jobs <- g.job()
		g.exited(0)
		_ = g.conn.Close()
	}()
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 10*time.Second, "Ergebnis des geplanten Laufs", func() bool {
		s, _ := e.schedule("q")
		return s.LastEnd != ""
	})
	if s, _ := e.schedule("q"); s.LastEnd != "ok" || s.LastRun != "2026-10-12T08:00:00Z" || s.SkippedBusy != 0 {
		t.Fatalf("Status = %+v (busy heißt: der Taktgeber hielt den Slot)", s)
	}
	if j := <-jobs; j.Entry != "main.py" {
		t.Fatalf("job = %+v", j)
	}
	if n := len(core.requests()); n != 1 {
		t.Fatalf("%d Auslöse-Aufrufe", n)
	}
}

// Invariante 1: Ein Auslöse-Aufruf bei belegtem Slot blockiert nicht. Weg A
// antwortet `busy`, der Taktgeber zählt skipped_busy — ohne selbst auf den
// Slot zu warten (BusyWait 10 s, das Ergebnis kommt deutlich früher).
func TestTriggerWithBusySlotDoesNotBlock(t *testing.T) {
	core := newFakeCore(t, answer("busy"))
	e, clk, g := scheduled(t, core, withBusyWait(10*time.Second))
	got := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = g.job()
		close(got)
		<-release
		g.exited(0)
		_ = g.conn.Close()
	}()
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = e.call("quick", map[string]any{}, e.fx.commit)
	}()
	<-got
	start := time.Now()
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 3*time.Second, "skipped_busy", func() bool {
		s, _ := e.schedule("q")
		return s.SkippedBusy == 1
	})
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Auslösung hat %v gewartet", d)
	}
	close(release)
	<-callDone
}

// Invariante 1, wahrscheinlichste Verletzung: Der Taktgeber schickt den Job
// selbst über Weg C an die Sandbox. Der Kern hier ruft Weg A NICHT zurück —
// also darf beim Wächter nichts ankommen.
func TestTickerNeverSendsJobToSandbox(t *testing.T) {
	core := newFakeCore(t, answer("ok"))
	e, clk, g := scheduled(t, core)
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 5*time.Second, "Ergebnis", func() bool {
		s, _ := e.schedule("q")
		return s.LastEnd == "ok"
	})
	_ = g.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if b, err := frame.Read(g.conn, 64<<20); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Wächter bekam ohne Weg A einen Rahmen: %q, %v", b, err)
	}
	if n := len(core.requests()); n != 1 {
		t.Fatalf("%d Auslöse-Aufrufe", n)
	}
}

// Invariante 4: POST ohne Body, Bearer = runtime-key (je Aufruf frisch
// gelesen), und der Schlüssel erscheint nie im Log.
func TestTriggerRequestNoBodyBearerRuntimeKeyNeverLogged(t *testing.T) {
	core := newFakeCore(t, answer("ok"))
	e, clk, _ := scheduled(t, core)
	const rotated = "rotated-runtime-key-not-a-secret"
	e.writeKey(rotated)
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 5*time.Second, "Auslöse-Aufruf", func() bool { return len(core.requests()) == 1 })
	r := core.requests()[0]
	if r.method != http.MethodPost || r.path != "/api/plugin/script-runner/schedules/q/run" {
		t.Fatalf("Request = %s %s", r.method, r.path)
	}
	if r.auth != "Bearer "+rotated {
		t.Fatalf("Authorization = %q", r.auth)
	}
	if r.bodyLen != 0 || r.contentLength != 0 {
		t.Fatalf("Body %d Bytes, Content-Length %d", r.bodyLen, r.contentLength)
	}
	// tools/list ginge jetzt nur noch mit dem neuen Schlüssel: auf den Code warten.
	waitUntil(t, 5*time.Second, "fester Code im Log", func() bool { return strings.Contains(e.logs.String(), ticker.CodeFired) })
	logs := e.logs.String()
	for _, secret := range []string{testKey, rotated} {
		if strings.Contains(logs, secret) {
			t.Fatalf("runtime-key im Log: %s", logs)
		}
	}
}

// Ohne runtime-key wird nichts gesendet (kein leerer Bearer).
func TestTriggerWithoutKeySendsNothing(t *testing.T) {
	core := newFakeCore(t, answer("ok"))
	e, clk, _ := scheduled(t, core)
	_ = os.Remove(filepath.Join(e.keys, "runtime-key"))
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 5*time.Second, "Fehler-Code", func() bool { return strings.Contains(e.logs.String(), ticker.CodeFailed) })
	if n := len(core.requests()); n != 0 {
		t.Fatalf("%d Aufrufe ohne Schlüssel", n)
	}
}

// Invariante 5: ungültige schedules.json oder unbekanntes Feld → keine
// Zeitpläne, fester Log-Code ohne Inhalt.
func TestInvalidSchedulesFileFailsClosedWithFixedCode(t *testing.T) {
	core := newFakeCore(t, answer("ok"))
	e, clk, _ := scheduled(t, core)
	e.writeSchedulesRaw(`{"version":1,"schedules":[` + scheduleRow("q", "quick", e.fx.commit, "0 8 * * *", "UTC", true) + `],"token_id":"KANARIE-4711"}`)
	waitUntil(t, 5*time.Second, "Zeitpläne entfernt", func() bool {
		_, ok := e.schedules()
		return !ok
	})
	clk.Set(clockAt(8, 0, 1))
	time.Sleep(200 * time.Millisecond)
	if n := len(core.requests()); n != 0 {
		t.Fatalf("%d Auslösungen aus ungültiger Datei", n)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, codeSchedulesInvalid) || strings.Contains(logs, "KANARIE") {
		t.Fatalf("Log = %s", logs)
	}
	if c := strings.Count(logs, codeSchedulesInvalid); c != 1 {
		t.Fatalf("Code %d-mal geloggt, want einmal je Wechsel", c)
	}
}

// Fehlt die Datei, gibt es keine Zeitpläne und keinen Schlüssel `schedules`.
func TestMissingSchedulesFileMeansNone(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	_ = dialGuard(t, e.sock, "a1", 2)
	e.waitReady("quick")
	if list, ok := e.schedules(); ok {
		t.Fatalf("schedules ohne Datei: %+v", list)
	}
}

// Höchstens 20 Zeitpläne; weitere werden ignoriert und mit Zählwert geloggt.
func TestOverTwentySchedulesIgnoredAndLogged(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	_ = dialGuard(t, e.sock, "a1", 2)
	e.waitReady("quick")
	rows := make([]string, 0, 23)
	for i := 0; i < 23; i++ {
		rows = append(rows, scheduleRow(fmt.Sprintf("s%02d", i), "quick", e.fx.commit, "0 8 * * *", "UTC", true))
	}
	e.writeSchedules(rows...)
	waitUntil(t, 5*time.Second, "Zeitpläne gemeldet", func() bool {
		list, _ := e.schedules()
		return len(list) > 0
	})
	if list, _ := e.schedules(); len(list) != 20 {
		t.Fatalf("%d Zeitpläne gemeldet", len(list))
	}
	logs := e.logs.String()
	if !strings.Contains(logs, codeSchedulesOverLimit) || !strings.Contains(logs, "ignored=3") {
		t.Fatalf("Log = %s", logs)
	}
}

// Invariante 6 auf dem Draht: anderer Commit oder enabled:false → keine
// Auslösung, Zustand passend.
func TestCommitMismatchOrDisabledNeverTriggers(t *testing.T) {
	core := newFakeCore(t, answer("ok"))
	e, clk, _ := scheduled(t, core)
	e.writeSchedules(
		scheduleRow("q", "quick", strings.Repeat("4", 40), "0 8 * * *", "UTC", true),
		scheduleRow("off", "quick", e.fx.commit, "0 8 * * *", "UTC", false),
		scheduleRow("gone", "nopin", e.fx.commit, "0 8 * * *", "UTC", true),
	)
	waitUntil(t, 5*time.Second, "Zustände", func() bool {
		q, _ := e.schedule("q")
		off, _ := e.schedule("off")
		gone, _ := e.schedule("gone")
		return q.State == contract.SchedulePaused && off.State == contract.ScheduleDisabled && gone.State == contract.SchedulePaused
	})
	clk.Set(clockAt(8, 0, 1))
	time.Sleep(200 * time.Millisecond)
	if n := len(core.requests()); n != 0 {
		t.Fatalf("%d Auslösungen", n)
	}
}

// 409 (falscher Schlüssel, unbekannt, nicht fällig — für den Kern dasselbe):
// nur zählen und loggen, keine Wiederholung, kein last_run.
func TestConflictCountedNotRetried(t *testing.T) {
	core := newFakeCore(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Zeitplan nicht verfügbar", http.StatusConflict)
	})
	e, clk, _ := scheduled(t, core)
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 5*time.Second, "Code für 409", func() bool { return strings.Contains(e.logs.String(), ticker.CodeRefused) })
	clk.Set(clockAt(8, 0, 5))
	time.Sleep(200 * time.Millisecond)
	if n := len(core.requests()); n != 1 {
		t.Fatalf("%d Aufrufe, want 1", n)
	}
	if s, _ := e.schedule("q"); s.LastRun != "" || s.LastEnd != "" || s.State != contract.ScheduleReady {
		t.Fatalf("Status = %+v", s)
	}
}

// 5xx: keine Wiederholung bis zum nächsten Termin.
func TestServerErrorNotRetried(t *testing.T) {
	core := newFakeCore(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "kaputt", http.StatusInternalServerError)
	})
	e, clk, _ := scheduled(t, core)
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 5*time.Second, "Fehler-Code", func() bool { return strings.Contains(e.logs.String(), ticker.CodeFailed) })
	clk.Set(clockAt(8, 0, 20))
	time.Sleep(200 * time.Millisecond)
	if n := len(core.requests()); n != 1 {
		t.Fatalf("%d Aufrufe, want 1", n)
	}
}

// Invariante 9: HTTP-Frist 35 s; ein hängender Kern wird abgebrochen und
// nicht erneut gerufen.
func TestTriggerDeadlineThirtyFiveSecondsNoRetry(t *testing.T) {
	m, err := New(Config{SocketPath: "x", KeyDir: t.TempDir(), StoreDir: t.TempDir(), GatewayURL: "http://jnpt:8484"})
	if err != nil {
		t.Fatal(err)
	}
	if m.cfg.TriggerTimeout != 35*time.Second || m.trigger.client.Timeout != 35*time.Second {
		t.Fatalf("Frist = %v / %v, want 35s", m.cfg.TriggerTimeout, m.trigger.client.Timeout)
	}

	core := newFakeCore(t, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // hängt, bis der Taktgeber abbricht
	})
	e, clk, _ := scheduled(t, core, withTriggerTimeout(300*time.Millisecond))
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 5*time.Second, "Abbruch nach der Frist", func() bool { return strings.Contains(e.logs.String(), ticker.CodeFailed) })
	clk.Set(clockAt(8, 0, 10))
	time.Sleep(200 * time.Millisecond)
	if n := len(core.requests()); n != 1 {
		t.Fatalf("%d Aufrufe, want 1", n)
	}
	if s, _ := e.schedule("q"); s.LastRun != "" {
		t.Fatalf("Status = %+v", s)
	}
}

// Die Antwort des Kerns wird begrenzt gelesen; Freitext wird nie gespiegelt.
func TestAnswerFreeTextNeverMirrored(t *testing.T) {
	core := newFakeCore(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"end":"KANARIE-4711 Skriptausgabe"}`)
	})
	e, clk, _ := scheduled(t, core)
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 5*time.Second, "Ergebnis", func() bool {
		s, _ := e.schedule("q")
		return s.LastEnd != ""
	})
	raw, _ := json.Marshal(e.listTools())
	if s, _ := e.schedule("q"); s.LastEnd != "unknown" || strings.Contains(string(raw), "KANARIE") || strings.Contains(e.logs.String(), "KANARIE") {
		t.Fatalf("Status = %+v", s)
	}
}

// SEC R4 H1: schedules.json wird höchstens mit 64 KiB gelesen. Eine größere
// Datei — hier gültiges JSON, mit Leerzeichen auf 64 KiB + 1 Byte gebracht —
// ergibt fail-closed keine Zeitpläne und den festen Code; genau 64 KiB gehen.
func TestSchedulesFileOverSixtyFourKiBFailsClosed(t *testing.T) {
	core := newFakeCore(t, answer("ok"))
	e, clk, _ := scheduled(t, core)
	valid := `{"version":1,"schedules":[` + scheduleRow("q", "quick", e.fx.commit, "0 8 * * *", "UTC", true) + `]}`
	const limit = 64 << 10
	e.writeSchedulesRaw(valid + strings.Repeat(" ", limit+1-len(valid)))
	waitUntil(t, 5*time.Second, "Zeitpläne entfernt", func() bool {
		_, ok := e.schedules()
		return !ok
	})
	if !strings.Contains(e.logs.String(), codeSchedulesInvalid) {
		t.Fatalf("Code fehlt: %s", e.logs.String())
	}
	clk.Set(clockAt(8, 0, 1))
	time.Sleep(200 * time.Millisecond)
	if n := len(core.requests()); n != 0 {
		t.Fatalf("%d Auslösungen aus übergroßer Datei", n)
	}
	// Genau an der Grenze wird gelesen.
	e.writeSchedulesRaw(valid + strings.Repeat(" ", limit-len(valid)))
	waitUntil(t, 5*time.Second, "Zeitplan an der Grenze gelesen", func() bool {
		_, ok := e.schedule("q")
		return ok
	})
}

// SEC R4 H2: Der Auslöse-Aufruf folgt keiner Umleitung. Das zweite Ziel sieht
// weder einen Aufruf noch den runtime-key; der Taktgeber verbucht einen Fehler.
func TestTriggerFollowsNoRedirect(t *testing.T) {
	var mu sync.Mutex
	var second []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		second = append(second, r.Header.Get("Authorization"))
		mu.Unlock()
		answer("ok")(w, r)
	}))
	t.Cleanup(other.Close)
	core := newFakeCore(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/plugin/script-runner/schedules/q/run", http.StatusTemporaryRedirect)
	})
	e, clk, _ := scheduled(t, core)
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 5*time.Second, "Fehler-Code nach 307", func() bool { return strings.Contains(e.logs.String(), ticker.CodeFailed) })
	mu.Lock()
	defer mu.Unlock()
	if len(second) != 0 {
		t.Fatalf("Umleitung gefolgt: %d Aufrufe am zweiten Ziel, Authorization %q", len(second), second)
	}
	if n := len(core.requests()); n != 1 {
		t.Fatalf("%d Aufrufe am Gateway", n)
	}
	if s, _ := e.schedule("q"); s.LastRun != "" || !strings.Contains(e.logs.String(), "status=307") {
		t.Fatalf("Status = %+v, Log = %s", s, e.logs.String())
	}
}
