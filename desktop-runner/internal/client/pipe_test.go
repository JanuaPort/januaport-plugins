package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	panel "github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

func panelRaw(t *testing.T, e *Engine, s string) panel.Message {
	t.Helper()
	var in, out bytes.Buffer
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(s)))
	in.Write(head[:])
	in.WriteString(s)
	e.HandlePanel(context.Background(), &in, &out)
	raw, err := panel.ReadFrame(&out)
	if err != nil {
		t.Fatal("keine Pipe-Antwort", err)
	}
	m, err := panel.DecodeServer(raw)
	if err != nil {
		t.Fatal(err, string(raw))
	}
	return m
}

func pipeBed(t *testing.T) *bed {
	b := haltBed(t)
	m := l1Manifest()
	m.Pins = []Pin{b.env.Origin}
	m.Pins[0].Setups = []Setup{{Name: "raum", Label: "Raum", Input: json.RawMessage(`{}`), Frei: []FreeField{}}}
	signed, cred := l1Sign(t, encode(m), m.DeviceID)
	b.engine.Credential = cred
	b.engine.bundle.Manifest.Envelope = signed
	b.engine.panelOnline = true
	return b
}

func TestPipeRoheRahmenAmLaeufer(t *testing.T) {
	for _, s := range []string{`{"type":"unbekannt"}`, `{"type":"status","token":"x"}`, `{"type":"status","input":{}}`, `{"type":"status","mode":"always"}`, `{"type":"status","pin":"demo"}`, `{"type":"setup_start","pin":"demo","setup":"raum","frei":{}}`, `{"type":"screenshot"}`, `{"type":"ergebnis","wert":42}`, strings.Repeat("x", 4097)} {
		b := pipeBed(t)
		var hits atomic.Int32
		b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) })
		if got := panelRaw(t, b.engine, s); got.Type != "fehler" || got.Code != "protokoll" {
			t.Fatal(s, got)
		}
		if hits.Load() != 0 || len(b.launches) != 0 {
			t.Fatal("ungültiger Rahmen hat Wirkung")
		}
	}
}

func TestSetupStartNurAusManifest(t *testing.T) {
	for _, which := range []string{"angehalten", "pausiert", "beschaeftigt", "reserviert", "offline", "unbekannt", "fremder_pin", "frei", "signatur", "konto", "alt"} {
		t.Run(which, func(t *testing.T) {
			b := pipeBed(t)
			var hits atomic.Int32
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) })
			pin, setup := "demo", "raum"
			switch which {
			case "angehalten", "pausiert":
				mustHalt(t, b.engine, which, time.Time{})
			case "beschaeftigt":
				b.engine.runMu.Lock()
				defer b.engine.runMu.Unlock()
			case "reserviert":
				b.engine.admission.Lock()
				defer b.engine.admission.Unlock()
			case "offline":
				b.engine.panelOnline = false
			case "unbekannt":
				setup = "fremd"
			case "fremder_pin":
				pin = "fremd"
			case "signatur":
				b.engine.bundle.Manifest.Envelope.Sig = "broken"
			case "konto":
				b.session.Account = "fremd"
			case "frei", "alt":
				m, _ := ParseManifest(b.engine.bundle.Manifest.Envelope.Manifest)
				if which == "frei" {
					m.Pins[0].Setups[0].Frei = []FreeField{{Name: "titel", Typ: "text", MaxLaenge: 40}}
				} else {
					m.Contract = 3
				}
				signed, cred := l1Sign(t, encode(m), m.DeviceID)
				b.engine.Credential = cred
				b.engine.bundle.Manifest.Envelope = signed
			}
			got := panelRaw(t, b.engine, `{"type":"setup_start","pin":"`+pin+`","setup":"`+setup+`"}`)
			if got.Type != "fehler" || hits.Load() != 0 || len(b.launches) != 0 {
				t.Fatal("Setup zugelassen", got, hits.Load())
			}
		})
	}
}

func TestSetupStartAmPipeHTTPAdapter(t *testing.T) {
	b := pipeBed(t)
	var hits atomic.Int32
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/device/v1/runs/start" || r.Header.Get("X-Jnpt-Manifest-Seq") != "42" {
			t.Error("Setup-Weg", r.URL.Path)
		}
		var v struct {
			Pin   string          `json:"pin"`
			Setup string          `json:"setup_id"`
			Nonce string          `json:"nonce"`
			Frei  json.RawMessage `json:"frei"`
		}
		if json.NewDecoder(r.Body).Decode(&v) != nil || v.Pin != "demo" || v.Setup != "raum" || len(v.Nonce) != 22 || string(v.Frei) != "{}" {
			t.Error("Setup-Anfrage", v)
		}
		json.NewEncoder(w).Encode(SetupStartReply{RunKey: "setup/demo/raum/" + v.Nonce, Input: json.RawMessage(`{}`), RunToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), MCPPath: "/api/device/v1/mcp"})
	})
	if got := panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`); got.Type != "zustand" {
		t.Fatal(got)
	}
	b.engine.WaitPanelRuns()
	j := b.st.Journal()
	if hits.Load() != 1 || len(b.launches) != 1 || len(j.Results) != 1 || j.Results[0].End != "ok" || j.Results[0].RanOffline || !strings.HasPrefix(j.Results[0].RunKey, "setup/demo/raum/") {
		t.Fatal(hits.Load(), len(b.launches), j)
	}
}

func TestSetupHaltWaerendHTTP(t *testing.T) {
	b := pipeBed(t)
	entered, release := make(chan struct{}), make(chan struct{})
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		var v map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&v)
		var nonce string
		json.Unmarshal(v["nonce"], &nonce)
		close(entered)
		<-release
		json.NewEncoder(w).Encode(SetupStartReply{RunKey: "setup/demo/raum/" + nonce, Input: json.RawMessage(`{}`), RunToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), MCPPath: "/api/device/v1/mcp"})
	})
	got := panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`)
	if got.Type != "zustand" {
		t.Fatal(got)
	}
	<-entered
	if got = panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`); got.Type != "fehler" {
		t.Fatal("zweiter Start", got)
	}
	if got = panelRaw(t, b.engine, `{"type":"anhalten"}`); got.Zustand != "angehalten" {
		t.Fatal(got)
	}
	close(release)
	b.engine.WaitPanelRuns()
	if len(b.launches) != 0 {
		t.Fatal("Start nach Halt")
	}
}

func TestPipeHaltBefehle(t *testing.T) {
	b := pipeBed(t)
	var hits atomic.Int32
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })
	for _, x := range []struct{ raw, state string }{{`{"type":"pause","minuten":0}`, "pausiert"}, {`{"type":"weiter"}`, "arbeitet"}, {`{"type":"anhalten"}`, "angehalten"}, {`{"type":"fortsetzen"}`, "arbeitet"}} {
		if got := panelRaw(t, b.engine, x.raw); got.Zustand != x.state {
			t.Fatal(got)
		}
	}
	if hits.Load() != 0 || len(b.launches) != 0 {
		t.Fatal("Halt startet/netzt")
	}
}

func TestPipeOhneTraeger(t *testing.T) {
	b := pipeBed(t)
	const secret = "CANARY-CREDENTIAL-NOT-FOR-PANEL"
	b.engine.Credential.Token = secret
	if err := b.st.Save("credential", b.engine.Credential); err != nil {
		t.Fatal(err)
	}
	b.engine.Config.StartPolicy.Mode = "always"
	got := panelRaw(t, b.engine, `{"type":"status"}`)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "zustand" || len(got.Setups) != 1 {
		t.Fatal(got)
	}
	for _, s := range []string{secret, `"token"`, `"input"`, `"mode"`, `"path"`, b.st.Root} {
		if bytes.Contains(raw, []byte(s)) {
			t.Fatal("Träger in Pipe", s)
		}
	}
}

func TestPipeBesetztStartetOhnePipe(t *testing.T) {
	b := pipeBed(t)
	b.engine.Log = &Logger{Root: b.st.Root}
	calls := 0
	closePipe := b.engine.StartPanel(context.Background(), func() (panel.Acceptor, error) { calls++; return nil, panel.ErrOccupied })
	defer closePipe()
	raw, err := os.ReadFile(filepath.Join(b.st.Root, "runner.log"))
	if err != nil || string(raw) != "9 1\n" || calls != 1 {
		t.Fatal(string(raw), calls, err)
	}
	if got := b.execute(); got.End != "ok" {
		t.Fatal("Läufer nach Squatting gestoppt", got)
	}
}

func TestSkriptRoheTestfunktionenBleibenGesperrt(t *testing.T) {
	// L5 accepts well-formed helper frames; malformed or unknown frames still
	// fail in Go before forwarding. Authorization is tested with raw L5 frames.
	for _, raw := range []string{`{"type":"screenshot","token":"x"}`, `{"type":"ergebnis","wert":42,"test":true}`, `{"type":"frage","text":"x","optionen":["a"],"frist_s":0}`} {
		b := pipeBed(t)
		var hits atomic.Int32
		b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })
		b.exit = func(w *io.PipeWriter) {
			var h [4]byte
			binary.BigEndian.PutUint32(h[:], uint32(len(raw)))
			w.Write(h[:])
			w.Write([]byte(raw))
		}
		r := b.execute()
		if r.End != "internal" || len(r.Result) != 0 || hits.Load() != 0 {
			t.Fatal("roher Skript-Rahmen", raw, r, hits.Load())
		}
	}
}

func TestPipeFrageAntwortWeiterleitung(t *testing.T) {
	b := pipeBed(t)
	server, ui := net.Pipe()
	defer ui.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { b.engine.HandlePanel(ctx, server, server); close(done) }()
	if err := panel.WriteFrame(ui, panel.Message{Type: "status"}); err != nil {
		t.Fatal(err)
	}
	panel.ReadFrame(ui)
	q := panel.Message{Type: "frage", Titel: "Demo", ID: "q1", Text: "Weiter?", Optionen: []string{"Ja", "Nein"}, FristS: 5}
	answer := make(chan *int, 1)
	go func() { v, _ := b.engine.Questions.Ask(ctx, q); answer <- v }()
	raw, err := panel.ReadFrame(ui)
	if err != nil {
		t.Fatal(err)
	}
	m, err := panel.DecodeServer(raw)
	if err != nil || m.Type != "frage" {
		t.Fatal(m, err)
	}
	zero := 0
	if err = panel.WriteFrame(ui, panel.Message{Type: "antwort", ID: "q1", Wahl: &zero}); err != nil {
		t.Fatal(err)
	}
	// Both the command acknowledgement and question removal traverse the pipe.
	seen := false
	for i := 0; i < 2; i++ {
		raw, err = panel.ReadFrame(ui)
		if err != nil {
			t.Fatal(err)
		}
		m, _ = panel.DecodeServer(raw)
		seen = seen || m.Type == "frage_weg"
	}
	if v := <-answer; v == nil || *v != 0 || !seen {
		t.Fatal(v, seen)
	}
	ui.Close()
	<-done
}
