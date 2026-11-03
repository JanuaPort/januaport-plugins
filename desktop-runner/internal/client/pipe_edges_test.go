package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	panel "github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

func TestPipeKindHandleListe(t *testing.T) {
	b, err := os.ReadFile("../platform/process_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "handles := []windows.Handle{windows.Handle(inRead.Fd()), windows.Handle(outWrite.Fd()), windows.Handle(nul.Fd())}") ||
		!strings.Contains(s, "attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0]))") ||
		strings.Contains(s, "JanuaPort.Bedienfeld") || strings.Contains(s, "handles = append") {
		t.Fatal("H3: explizite Kind-Handleliste verändert")
	}
}

func TestSetupFehlerNieOfflineNachmelden(t *testing.T) {
	b := pipeBed(t)
	var hits atomic.Int32
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(503) })
	panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`)
	b.engine.WaitPanelRuns()
	if hits.Load() != 1 || len(b.launches) != 0 || len(b.st.Journal().Results) != 0 {
		t.Fatal("Offline-Setup", hits.Load(), b.st.Journal())
	}
	if m := panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`); m.Type != "fehler" || hits.Load() != 1 {
		t.Fatal(m, hits.Load())
	}
}

func TestSetupVeralteteFreigabeStartetNicht(t *testing.T) {
	for _, kind := range []string{"manifest", "abgelaufen", "halt_fortsetzen"} {
		t.Run(kind, func(t *testing.T) {
			b := pipeBed(t)
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
				var v map[string]json.RawMessage
				json.NewDecoder(r.Body).Decode(&v)
				var nonce string
				json.Unmarshal(v["nonce"], &nonce)
				expires := time.Now().Add(time.Hour)
				switch kind {
				case "manifest":
					b.engine.mu.Lock()
					b.engine.bundle.Manifest.Envelope.Sig = "withdrawn"
					b.engine.mu.Unlock()
				case "abgelaufen":
					expires = time.Now().Add(-time.Hour)
				case "halt_fortsetzen":
					b.engine.Halt("angehalten", time.Time{})
					b.engine.Halt("arbeitet", time.Time{})
				}
				json.NewEncoder(w).Encode(SetupStartReply{RunKey: "setup/demo/raum/" + nonce, Input: json.RawMessage(`{}`), RunToken: "synthetic", ExpiresAt: expires.UTC().Format(time.RFC3339), MCPPath: "/api/device/v1/mcp"})
			})
			panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`)
			b.engine.WaitPanelRuns()
			if len(b.launches) != 0 {
				t.Fatal("veraltete Setup-Freigabe gestartet")
			}
		})
	}
}

func TestSetupSperrtAndereStartwege(t *testing.T) {
	b := pipeBed(t)
	b.engine.admission.Lock()
	defer b.engine.admission.Unlock()
	var hits atomic.Int32
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) })
	if err := b.engine.scheduled(context.Background(), b.env.Origin, Schedule{Name: "daily", Input: json.RawMessage(`{}`)}, time.Now().UTC().Truncate(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := b.engine.adhoc(context.Background(), Job{JobID: "11111111-1111-4111-8111-111111111111"}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 || len(b.st.Journal().Results) != 0 {
		t.Fatal("anderer Startweg reserviert", hits.Load(), b.st.Journal())
	}
}

func TestPipeHaltSchliesstFrage(t *testing.T) {
	b := pipeBed(t)
	events, detach, _ := b.engine.Questions.Attach()
	defer detach()
	done := make(chan *int, 1)
	go func() {
		v, _ := b.engine.Questions.Ask(context.Background(), panel.Message{Type: "frage", Titel: "Demo", ID: "q1", Text: "Frage", Optionen: []string{"a", "b"}, FristS: 5})
		done <- v
	}()
	<-events
	// Call the same decoded command dispatch without attaching a second panel.
	b.engine.panelCommand(context.Background(), panel.Message{Type: "anhalten"})
	select {
	case v := <-done:
		if v != nil {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("Frage nach Halt offen")
	}
}

func TestPipeStatusFristUndStoerung(t *testing.T) {
	b := pipeBed(t)
	b.engine.observePanelNetwork(errors.New("offline"))
	b.engine.bundle.Manifest.LastSync = time.Now().Add(-25 * time.Hour)
	m := panelRaw(t, b.engine, `{"type":"status"}`)
	if m.Netz || !m.FristAbgelaufen || m.Setups[0].Startbar {
		t.Fatal(m)
	}
	b.engine.bundle.Manifest.Envelope.Sig = "broken"
	if m = panelRaw(t, b.engine, `{"type":"status"}`); m.Zustand != "stoerung" || len(m.Setups) != 0 {
		t.Fatal(m)
	}
}

func TestSetupDirektesExecuteBleibtGesperrt(t *testing.T) {
	b := pipeBed(t)
	entered, release := make(chan struct{}), make(chan struct{})
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(503) })
	panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`)
	<-entered
	if r := b.execute(); r.End != "internal" {
		t.Fatal(r)
	}
	close(release)
	b.engine.WaitPanelRuns()
	if len(b.launches) != 0 {
		t.Fatal("Execute umgeht Reservierung")
	}
}

func TestSetupPauseLaesstLaufEnden(t *testing.T) {
	b := pipeBed(t)
	started, release := make(chan struct{}), make(chan struct{})
	b.exit = func(w *io.PipeWriter) {
		close(started)
		<-release
		WriteFrame(w, map[string]any{"type": "exited", "code": 0})
	}
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		var v map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&v)
		var nonce string
		json.Unmarshal(v["nonce"], &nonce)
		json.NewEncoder(w).Encode(SetupStartReply{RunKey: "setup/demo/raum/" + nonce, Input: json.RawMessage(`{}`), RunToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), MCPPath: "/api/device/v1/mcp"})
	})
	panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`)
	<-started
	if m := panelRaw(t, b.engine, `{"type":"pause","minuten":0}`); m.Zustand != "pausiert" {
		t.Fatal(m)
	}
	close(release)
	b.engine.WaitPanelRuns()
	j := b.st.Journal()
	if len(j.Results) != 1 || j.Results[0].End != "ok" {
		t.Fatal(j)
	}
}

func TestSetupFreigabeDirektVorSpawn(t *testing.T) {
	for _, kind := range []string{"manifest", "halt_fortsetzen"} {
		t.Run(kind, func(t *testing.T) {
			b := pipeBed(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var prepare atomic.Bool
			b.engine.Session = func() (Session, error) {
				if prepare.Swap(false) {
					if kind == "manifest" {
						b.engine.mu.Lock()
						b.engine.bundle.Manifest.Envelope.Sig = "withdrawn"
						b.engine.mu.Unlock()
					} else {
						b.engine.Halt("angehalten", time.Time{})
						b.engine.Halt("arbeitet", time.Time{})
					}
				}
				return b.session, nil
			}
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
				var v map[string]json.RawMessage
				json.NewDecoder(r.Body).Decode(&v)
				var nonce string
				json.Unmarshal(v["nonce"], &nonce)
				close(entered)
				<-release
				json.NewEncoder(w).Encode(SetupStartReply{RunKey: "setup/demo/raum/" + nonce, Input: json.RawMessage(`{}`), RunToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), MCPPath: "/api/device/v1/mcp"})
			})
			panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`)
			<-entered
			prepare.Store(true)
			close(release)
			b.engine.WaitPanelRuns()
			if len(b.launches) != 0 {
				t.Fatal("Setup-Freigabe vor Spawn nicht neu geprüft")
			}
		})
	}
}

func TestSetupSperrtUndBrichtClaimAb(t *testing.T) {
	b := pipeBed(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() { close(release); b.engine.WaitPanelRuns() }()
	claimEntered := make(chan struct{})
	var claims atomic.Int32
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "jobs/claim") {
			if claims.Add(1) == 1 {
				close(claimEntered)
			}
			<-r.Context().Done()
			return
		}
		close(entered)
		<-release
		w.WriteHeader(503)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	claimDone := make(chan error, 1)
	go func() {
		j, err := b.engine.claim(ctx)
		if j != nil {
			err = errors.New("alter Claim")
		}
		claimDone <- err
	}()
	<-claimEntered
	panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`)
	<-entered
	select {
	case err := <-claimDone:
		if err != nil {
			t.Fatal("offener Claim nicht verworfen", err)
		}
	case <-time.After(time.Second):
		t.Fatal("offener Claim nicht sofort abgebrochen")
	}
	pollCtx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	j, err := b.engine.claim(pollCtx)
	if err != nil || j != nil || claims.Load() != 1 {
		t.Fatal("Claim während Setup", j, err, claims.Load())
	}
}

func TestPipeStatusPasstNichtWirdStoerung(t *testing.T) {
	b := pipeBed(t)
	m, _ := ParseManifest(b.engine.bundle.Manifest.Envelope.Manifest)
	m.Pins[0].Setups = nil
	for i := 0; i < 80; i++ {
		m.Pins[0].Setups = append(m.Pins[0].Setups, Setup{Name: fmt.Sprintf("raum%d", i), Label: strings.Repeat("x", 40), Input: json.RawMessage(`{}`), Frei: []FreeField{}})
	}
	signed, cred := l1Sign(t, encode(m), m.DeviceID)
	b.engine.bundle.Manifest.Envelope = signed
	b.engine.Credential = cred
	if got := panelRaw(t, b.engine, `{"type":"status"}`); got.Type != "fehler" || got.Code != "stoerung" {
		t.Fatal(got)
	}
}
