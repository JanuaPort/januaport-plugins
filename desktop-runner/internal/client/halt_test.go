package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func haltBed(t *testing.T) *bed {
	t.Helper()
	b := newBed(t)
	b.engine.Config.Contract = WindowsContract
	b.engine.Config.Device.ID = "aaaaaaaaaaaaaaaa"
	b.engine.Config.Device.Kind = "workstation"
	b.engine.Config.HeartbeatS, b.engine.Config.PollHoldS = 60, 25
	m := l1Manifest()
	m.Pins = []Pin{b.env.Origin}
	signed, cred := l1Sign(t, encode(m), m.DeviceID)
	b.engine.Credential = cred
	b.engine.bundle = Bundle{ManifestState{signed, time.Now()}, b.engine.Config, map[string]Environment{b.env.Dir: b.env}}
	return b
}

func mustHalt(t *testing.T, e *Engine, state string, until time.Time) {
	t.Helper()
	if err := e.Halt(state, until); err != nil {
		t.Fatal(err)
	}
}

func TestHaltUeberstehtNeustart(t *testing.T) {
	b := haltBed(t)
	mustHalt(t, b.engine, "angehalten", time.Time{})
	var stored map[string]any
	if err := b.st.Load("halt", &stored); err != nil || stored["state"] != "angehalten" {
		t.Fatal(stored, err)
	}
	st, err := OpenState(b.st.Root, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	b.engine = &Engine{State: st, Config: b.engine.Config, Session: b.engine.Session, Spawn: b.spawn}
	if got := b.execute(); got.End != "cancelled" || len(b.launches) != 0 {
		t.Fatal(got, b.launches)
	}
}

func TestHaltDateiFehltOderDefekt(t *testing.T) {
	for _, raw := range []string{"", "broken", `TEST-CIPHER:{`, `TEST-CIPHER:{"state":"fremd"}`, `TEST-CIPHER:{"state":"arbeitet","until":"2026-11-01T12:00:00Z"}`, `TEST-CIPHER:{"state":"pausiert","until":"bad"}`, `TEST-CIPHER:{"state":"pausiert","until":"2026-11-01T12:00:00+01:00"}`, `TEST-CIPHER:{"state":"arbeitet","extra":true}`, "directory"} {
		t.Run(raw, func(t *testing.T) {
			b := haltBed(t)
			b.engine.Log = &Logger{Root: b.st.Root}
			if raw == "directory" {
				if err := os.Mkdir(filepath.Join(b.st.Root, "halt"), 0700); err != nil {
					t.Fatal(err)
				}
			} else if raw != "" {
				if err := os.WriteFile(filepath.Join(b.st.Root, "halt"), []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			state, _, fault := b.engine.HaltStatus()
			got := b.execute()
			if raw == "" {
				if state != "arbeitet" || fault || got.End != "ok" {
					t.Fatal(state, fault, got)
				}
			} else {
				log, _ := os.ReadFile(filepath.Join(b.st.Root, "runner.log"))
				if state != "angehalten" || !fault || got.End != "cancelled" || len(b.launches) != 0 || string(log) != "8 1\n" {
					t.Fatal(state, fault, got, string(log))
				}
				state, _, fault = b.engine.HaltStatus()
				if state != "angehalten" || !fault {
					t.Fatal("Störung verloren")
				}
			}
		})
	}
}

func TestHaltZwischenClaimUndSpawn(t *testing.T) {
	for _, state := range []string{"angehalten", "pausiert"} {
		t.Run(state, func(t *testing.T) {
			b := haltBed(t)
			job := Job{JobID: "11111111-1111-4111-8111-111111111111", Pin: b.env.Origin.Pin, Commit: b.env.Origin.Commit, Path: b.env.Origin.Path, Algo: b.env.Origin.Algo, ManifestSeq: 42, StartBy: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Input: json.RawMessage(`{}`)}
			job.Limits.WallS = 10
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/device/v1/jobs/claim":
					json.NewEncoder(w).Encode(map[string]any{"job": job})
				case "/api/device/v1/jobs/" + job.JobID + "/start":
					json.NewEncoder(w).Encode(StartReply{"token", job.StartBy, "/api/device/v1/mcp"})
				default:
					t.Error(r.URL.Path)
					w.WriteHeader(500)
				}
			})
			claimed, err := b.engine.claim(context.Background())
			if err != nil || claimed == nil {
				t.Fatal(claimed, err)
			}
			// Block the real preparation path after Claim/Start, before the spawn lock.
			entered, release := make(chan struct{}), make(chan struct{})
			b.engine.Temps = &RunTemps{Base: b.base(), Mkdir: func(path string, exclusive bool) error {
				if !exclusive {
					return os.MkdirAll(path, 0700)
				}
				close(entered)
				<-release
				return os.Mkdir(path, 0700)
			}}
			done := make(chan error, 1)
			go func() { done <- b.engine.adhoc(context.Background(), *claimed) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("Vorbereitung fehlt")
			}
			mustHalt(t, b.engine, state, time.Time{})
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if len(b.launches) != 0 {
				t.Fatal("Start nach Halt")
			}
			j := b.st.Journal()
			if len(j.Results) != 1 || j.Results[0].End != "cancelled" {
				t.Fatal(j)
			}
		})
	}
	// Structural lock witness supplements the deterministic interleaving: moving
	// the check just outside the mutex must fail even without scheduler luck.
	raw, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	spawn := strings.Index(source, "p, err := e.Spawn(runCtx, launch)")
	lock := strings.LastIndex(source[:spawn], "e.mu.Lock()")
	if !strings.Contains(source[lock:spawn], "e.haltedLocked(time.Now())") {
		t.Fatal("L2-1: Halt-Prüfung außerhalb der Spawn-Sperre")
	}
}

type haltProcess struct {
	Process
	kills *atomic.Int32
}

func (p haltProcess) Kill() error { p.kills.Add(1); return p.Process.Kill() }

type haltWriteFailure struct{ before func() }

func (w haltWriteFailure) Write([]byte) (int, error) {
	if w.before != nil {
		w.before()
	}
	return 0, io.ErrClosedPipe
}
func (haltWriteFailure) Close() error { return nil }

type haltInputProcess struct {
	Process
	input io.WriteCloser
}

func (p haltInputProcess) Input() io.WriteCloser { return p.input }

func TestHaltBeiWriteFrameFehler(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		halt       bool
	}{{"ohne_Halt", "internal", false}, {"mit_Halt", "cancelled", true}} {
		t.Run(tc.name, func(t *testing.T) {
			b := haltBed(t)
			b.exit = func(*io.PipeWriter) {} // WriteFrame fails before reading output.
			writes := 0
			b.engine.Spawn = func(ctx context.Context, l Launch) (Process, error) {
				p, err := b.spawn(ctx, l)
				return haltInputProcess{p, haltWriteFailure{before: func() {
					writes++
					if tc.halt {
						mustHalt(t, b.engine, "angehalten", time.Time{})
					}
				}}}, err
			}
			if got := b.execute(); writes != 1 || got.End != tc.want {
				t.Fatal("WriteFrame-Fehler falsch zugeordnet", writes, got)
			}
		})
	}
}

func TestAnhaltenBeendetLaufOhneNetz(t *testing.T) {
	// Synchronous witness: removing only Kill cannot hide behind Execute's own
	// cancellation cleanup racing with the assertion below.
	probe := haltBed(t)
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	var directKills atomic.Int32
	probe.engine.active = haltProcess{&fakeProc{r: r, w: w}, &directKills}
	var cancelled atomic.Bool
	probe.engine.activeCancel = func() { cancelled.Store(true) }
	mustHalt(t, probe.engine, "angehalten", time.Time{})
	if directKills.Load() != 1 || !cancelled.Load() {
		t.Fatal("Anhalten ohne synchrones Cancel/Kill")
	}

	b := haltBed(t)
	started := make(chan struct{})
	b.exit = func(w *io.PipeWriter) { close(started) }
	var kills atomic.Int32
	b.engine.Spawn = func(ctx context.Context, l Launch) (Process, error) {
		p, err := b.spawn(ctx, l)
		return haltProcess{p, &kills}, err
	}
	done := make(chan Result, 1)
	go func() { done <- b.execute() }()
	<-started
	mustHalt(t, b.engine, "angehalten", time.Time{}) // API is nil: no network dependency.
	if kills.Load() == 0 {
		t.Error("Halt kehrt ohne Kill zurück")
	}
	select {
	case got := <-done:
		if got.End != "cancelled" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("Lauf lebt")
	}
}

func TestPauseLaesstLaufendenZuEnde(t *testing.T) {
	b := haltBed(t)
	started, release := make(chan struct{}), make(chan struct{})
	b.exit = func(w *io.PipeWriter) {
		close(started)
		<-release
		WriteFrame(w, map[string]any{"type": "exited", "code": 0})
	}
	done := make(chan Result, 1)
	go func() { done <- b.execute() }()
	<-started
	if err := b.engine.Pause(0); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		t.Fatal("Pause bricht Lauf ab", got)
	default:
	}
	var hits atomic.Int32
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(204) })
	if _, err := b.engine.claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.engine.scheduled(context.Background(), b.env.Origin, Schedule{Name: "minute"}, time.Now().UTC().Truncate(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := b.engine.adhoc(context.Background(), Job{}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 || len(b.st.Journal().Results) != 0 {
		t.Fatal("Halt holt Claim/Slot/Testlauf", hits.Load())
	}
	if !b.engine.isReady() {
		t.Fatal("Halt verändert ready")
	}
	close(release)
	if got := <-done; got.End != "ok" {
		t.Fatal(got)
	}
}

func TestFortsetzenHoltNichtsNach(t *testing.T) {
	b := haltBed(t)
	var hits atomic.Int32
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(204) })
	slot := time.Now().UTC().Truncate(time.Minute)
	mustHalt(t, b.engine, "angehalten", time.Time{})
	mustHalt(t, b.engine, "arbeitet", time.Time{})
	for _, missed := range []time.Time{slot.Add(-time.Minute), slot} {
		if err := b.engine.scheduled(context.Background(), b.env.Origin, Schedule{Name: "minute"}, missed); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.launches) != 0 || len(b.st.Journal().Results) != 0 || hits.Load() != 0 {
		t.Fatal("Slot nachgeholt")
	}
	// Resume permits future ordinary execution, but does not invoke it itself.
	if got := b.execute(); got.End != "ok" {
		t.Fatal(got)
	}
}

func TestHaltBefehleStartenNie(t *testing.T) {
	b := haltBed(t)
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/device/v1/halt" {
			t.Error("Befehl startet", r.URL.Path)
		}
		w.WriteHeader(204)
	})
	for _, minutes := range []int{15, 60, 0} {
		if err := b.engine.Pause(minutes); err != nil {
			t.Fatal(err)
		}
		state, until, fault := b.engine.HaltStatus()
		if state != "pausiert" || fault || (minutes == 0) != until.IsZero() {
			t.Fatal(state, until, fault)
		}
		if minutes != 0 && (time.Until(until) > time.Duration(minutes)*time.Minute || time.Until(until) < time.Duration(minutes)*time.Minute-time.Second) {
			t.Fatal(until)
		}
		mustHalt(t, b.engine, "arbeitet", time.Time{})
	}
	mustHalt(t, b.engine, "angehalten", time.Time{})
	if err := b.engine.Pause(15); err == nil {
		t.Fatal("Pause hebt Anhalten auf")
	}
	mustHalt(t, b.engine, "arbeitet", time.Time{})
	for _, minutes := range []int{-1, 1, 30, 61} {
		if err := b.engine.Pause(minutes); err == nil {
			t.Fatal("freie Pause", minutes)
		}
	}
	for _, state := range []string{"", "stoerung", "start"} {
		if err := b.engine.Halt(state, time.Time{}); err == nil {
			t.Fatal("freier Befehl")
		}
	}
	if err := b.engine.Halt("arbeitet", time.Now()); err == nil {
		t.Fatal("until bei arbeitet")
	}
	if len(b.launches) != 0 || len(b.st.Journal().Results) != 0 {
		t.Fatal("Befehl startet")
	}
	mustHalt(t, b.engine, "arbeitet", time.Time{})
	b.session.Account = "wrong"
	if got := b.execute(); got.End != "refused" || got.Detail != "account" {
		t.Fatal("B3 Konto umgangen", got)
	}
}

func TestPauseEndetAutomatisch(t *testing.T) {
	b := haltBed(t)
	until := time.Now().UTC().Add(100 * time.Millisecond)
	mustHalt(t, b.engine, "pausiert", until)
	if state, _, _ := b.engine.HaltStatus(); state != "pausiert" {
		t.Fatal(state)
	}
	time.Sleep(120 * time.Millisecond)
	if state, _, fault := b.engine.HaltStatus(); state != "arbeitet" || fault {
		t.Fatal(state, fault)
	}
	var stored haltState
	if err := b.st.Load("halt", &stored); err != nil || stored.State != "arbeitet" {
		t.Fatal(stored, err)
	}
}

func TestHaltMeldungKoalesziert(t *testing.T) {
	b := haltBed(t)
	var sent []haltState
	fail := true
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/device/v1/halt" || r.Method != "POST" || r.Header.Get("X-Jnpt-Desktop-Contract") != "5" {
			t.Error(r.URL.Path, r.Header)
		}
		var body struct {
			Contract int    `json:"contract"`
			State    string `json:"state"`
			Until    string `json:"until"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		sent = append(sent, haltState{State: body.State, Until: body.Until})
		if fail {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	})
	base := time.Now()
	for i := 0; i < 60; i++ {
		state := "pausiert"
		if i%2 == 1 {
			state = "arbeitet"
		}
		mustHalt(t, b.engine, state, time.Time{})
		b.engine.deliverHalt(context.Background(), base.Add(time.Duration(i)*time.Second))
	}
	if len(sent) != 6 {
		t.Fatal("mehr/weniger als sechs Versuche", len(sent))
	}
	fail = false
	mustHalt(t, b.engine, "angehalten", time.Time{})
	b.engine.deliverHalt(context.Background(), base.Add(time.Minute))
	if len(sent) != 7 || sent[6].State != "angehalten" {
		t.Fatal("letzter Zustand fehlt", sent)
	}
	b.engine.deliverHalt(context.Background(), base.Add(2*time.Minute))
	if len(sent) != 7 {
		t.Fatal("bestätigten Zustand wiederholt")
	}
}

func TestHaltRunSperrtClaimsUndHaeltHeartbeat(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "halt", true: "defekt"}[corrupt], func(t *testing.T) {
			b := haltBed(t)
			if err := b.st.Save("manifest", b.engine.bundle); err != nil {
				t.Fatal(err)
			}
			if corrupt {
				os.WriteFile(filepath.Join(b.st.Root, "halt"), []byte("broken"), 0600)
			} else {
				mustHalt(t, b.engine, "angehalten", time.Time{})
			}
			var claims, ready, reports atomic.Int32
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/device/v1/heartbeat":
					var h Heartbeat
					json.NewDecoder(r.Body).Decode(&h)
					if h.Ready {
						ready.Add(1)
					}
					w.Write([]byte(`{"cancel":[],"manifest_seq_current":42}`))
				case "/api/device/v1/jobs/claim":
					claims.Add(1)
					w.WriteHeader(204)
				case "/api/device/v1/halt":
					reports.Add(1)
					w.WriteHeader(204)
				default:
					w.WriteHeader(503)
				}
			})
			// Fresh engine: the startup path, not an already loaded RAM flag.
			e := &Engine{State: b.st, API: b.engine.API, Credential: b.engine.Credential, Session: b.engine.Session, Spawn: b.spawn}
			ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
			defer cancel()
			_ = e.Run(ctx)
			if claims.Load() != 0 || ready.Load() == 0 || reports.Load() == 0 || len(b.launches) != 0 {
				t.Fatal(claims.Load(), ready.Load(), reports.Load(), len(b.launches))
			}
		})
	}
}
