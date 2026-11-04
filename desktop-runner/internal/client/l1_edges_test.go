package client

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetupSchemaAmAdapter(t *testing.T) {
	for _, kind := range []string{"name", "input", "freiNull", "felder9", "feldname", "feldDoppelt", "kollision", "maxFalscherTyp", "optionenFalscherTyp", "max201", "maxNegativ", "auswahlLeer", "auswahl21", "optionLeer", "optionDoppelt", "optionSteuerzeichen", "setupDoppelt", "v1Setup", "v3Grace", "v1Grace", "v2Setup"} {
		t.Run(kind, func(t *testing.T) {
			m := l1Manifest()
			s := Setup{Name: "raum", Label: "Raum", Input: json.RawMessage(`{}`), Frei: []FreeField{{Name: "titel", Typ: "text"}}}
			switch kind {
			case "name":
				s.Name = "../bad"
			case "input":
				s.Input = json.RawMessage(`[]`)
			case "freiNull":
				s.Frei = nil
			case "felder9":
				for i := 0; i < 8; i++ {
					s.Frei = append(s.Frei, FreeField{Name: fmt.Sprintf("feld%d", i), Typ: "text"})
				}
			case "feldname":
				s.Frei[0].Name = "../bad"
			case "feldDoppelt":
				s.Frei = append(s.Frei, s.Frei[0])
			case "kollision":
				s.Input = json.RawMessage(`{"titel":"fest"}`)
			case "maxFalscherTyp":
				s.Frei[0].Typ = "zahl"
				s.Frei[0].MaxLaenge = 1
			case "optionenFalscherTyp":
				s.Frei[0].Optionen = []string{"x"}
			case "max201":
				s.Frei[0].MaxLaenge = 201
			case "maxNegativ":
				s.Frei[0].MaxLaenge = -1
			case "auswahlLeer":
				s.Frei[0].Typ = "auswahl"
			case "auswahl21":
				s.Frei[0].Typ = "auswahl"
				for i := 0; i < 21; i++ {
					s.Frei[0].Optionen = append(s.Frei[0].Optionen, fmt.Sprint(i))
				}
			case "optionLeer":
				s.Frei[0].Typ = "auswahl"
				s.Frei[0].Optionen = []string{""}
			case "optionDoppelt":
				s.Frei[0].Typ = "auswahl"
				s.Frei[0].Optionen = []string{"x", "x"}
			case "optionSteuerzeichen":
				s.Frei[0].Typ = "auswahl"
				s.Frei[0].Optionen = []string{"x\ny"}
			case "v1Setup":
				m.Contract = 1
			case "v2Setup":
				m.Contract = 2
				m.Pins[0] = containerPin()
			case "v3Grace":
				m.Contract = 3
				hours := 2
				m.Pins[0].GraceHours = &hours
			case "v1Grace":
				m.Contract = 1
				hours := 2
				m.Pins[0].GraceHours = &hours
			}
			m.Pins[0].Setups = []Setup{s}
			if kind == "setupDoppelt" {
				m.Pins[0].Setups = append(m.Pins[0].Setups, s)
			}
			signed, c := l1Sign(t, encode(m), m.DeviceID)
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(signed) })
			got, err := a.Manifest(context.Background())
			if err == nil {
				_, err = VerifyManifest(got, c.Pubkey, c.DeviceID, 0)
			}
			if err == nil {
				t.Fatal("ungültiges Schema akzeptiert")
			}
		})
	}
}

func TestSetupStartVerweigertFremdeFreigaben(t *testing.T) {
	for _, kind := range []string{"pin", "setup", "signatur", "geraet", "server", "frei", "antwort", "altvertrag"} {
		t.Run(kind, func(t *testing.T) {
			m := l1Manifest()
			m.Pins[0].Setups = []Setup{{Name: "raum", Label: "Raum", Input: json.RawMessage(`{}`), Frei: []FreeField{}}}
			if kind == "altvertrag" {
				m.Contract = 3
			}
			signed, c := l1Sign(t, encode(m), m.DeviceID)
			pin := m.Pins[0].Pin
			setup := "raum"
			frei := json.RawMessage(`{}`)
			hits := 0
			switch kind {
			case "pin":
				pin = "other"
			case "setup":
				setup = "other"
			case "signatur":
				signed.Sig = "invalid"
			case "geraet":
				c.DeviceID = "other"
			case "frei":
				frei = json.RawMessage(`[]`)
			}
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
				hits++
				io.WriteString(w, `{"run_key":"setup/fremd/raum/nonce","input":{},"run_token":"token","expires_at":"2027-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp"}`)
			})
			if kind == "server" {
				if err := a.Server("rootless"); err != nil {
					t.Fatal(err)
				}
			}
			_, err := a.StartSetup(context.Background(), signed, c, pin, setup, frei)
			wantHits := 0
			if kind == "antwort" {
				wantHits = 1
			}
			if err == nil || hits != wantHits {
				t.Fatal("Setup-Sperre", err, hits)
			}
		})
	}
}

func TestL1WiderrufAmHTTPAdapter(t *testing.T) {
	for _, route := range []string{"halt", "setup", "result", "screenshot"} {
		t.Run(route, func(t *testing.T) {
			c, o := l1Order(t, true, "test_aaaaaaaa")
			m := l1Manifest()
			m.Pins[0].Setups = []Setup{{Name: "raum", Label: "Raum", Input: json.RawMessage(`{}`), Frei: []FreeField{}}}
			signed, setupCred := l1Sign(t, encode(m), m.DeviceID)
			hits := 0
			revoked := 0
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.Header.Get("Authorization") != "Bearer jnpt_synthetic" || r.Header.Get("X-Jnpt-Run-Token") != "" {
					t.Error("Tokenrollen")
				}
				w.WriteHeader(401)
				io.WriteString(w, `{"error":{"class":"revoked"}}`)
			})
			a.onRevoke = func() error { revoked++; return nil }
			var err error
			switch route {
			case "halt":
				err = a.Halt(context.Background(), "angehalten", "")
			case "setup":
				_, err = a.StartSetup(context.Background(), signed, setupCred, m.Pins[0].Pin, "raum", nil)
			case "result":
				err = a.ReportTest(context.Background(), Result{JobID: o.JobID, ManifestSeq: o.ManifestSeq, Commit: o.Commit, Result: json.RawMessage(`42`)}, c, o)
			case "screenshot":
				err = a.Screenshot(context.Background(), c, o, []byte{137, 80, 78, 71, 13, 10, 26, 10})
			}
			if err != ErrRevoked || revoked != 1 || hits != 1 {
				t.Fatal("Widerruf", err, revoked, hits)
			}
			if err = a.Halt(context.Background(), "arbeitet", ""); err != ErrRevoked || hits != 1 {
				t.Fatal("nach Widerruf gesendet", err, hits)
			}
		})
	}
}

func TestK8ArchivLaengsteWirksameFrist(t *testing.T) {
	for _, tc := range []struct {
		grace []int
		hours int
	}{{[]int{1, 72}, 72}, {[]int{1, 2}, 2}, {[]int{1, 0}, 24}, {nil, 24}} {
		m := l1Manifest()
		m.Pins = nil
		for _, h := range tc.grace {
			p := l1Manifest().Pins[0]
			if h != 0 {
				p.GraceHours = &h
			}
			m.Pins = append(m.Pins, p)
		}
		until, err := m.RetainUntil()
		issued, _ := time.Parse(time.RFC3339, m.IssuedAt)
		if err != nil || !until.Equal(issued.Add(time.Duration(tc.hours+24)*time.Hour)) {
			t.Fatal("Archivfrist", until, tc, err)
		}
	}
}

func TestErgebnisNachNeustartMitSignierterFreigabe(t *testing.T) {
	c, order := l1Order(t, true, "test_aaaaaaaa")
	st, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	// The actual claim composition persists the historical authorization before
	// environment lookup, without changing the journal format.
	e := Engine{State: st, Credential: c, bundle: Bundle{Manifest: ManifestState{Envelope: order.Envelope}}}
	job := Job{JobID: order.JobID, Pin: order.Pin, Commit: order.Commit, Path: order.Path, Algo: order.Algo, ManifestSeq: order.ManifestSeq, Input: json.RawMessage(`{"private":"SYNTHETIC_INPUT_CANARY"}`), StartBy: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	if err = e.adhoc(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	grant, err := os.ReadFile(filepath.Join(st.Root, "test-"+job.JobID))
	if err != nil || strings.Contains(string(grant), "SYNTHETIC_INPUT_CANARY") {
		t.Fatal("Freigabebindung speichert Claim-Eingabe", err)
	}
	if err = st.DeliverOne(func(Result) error { return nil }); err != nil {
		t.Fatal(err)
	}
	r := Result{JobID: order.JobID, ManifestSeq: order.ManifestSeq, Commit: order.Commit, End: "ok", FinishedAt: "2026-11-01T12:01:00Z", Result: json.RawMessage(`{"wert":42}`)}
	if err = st.Append(r); err != nil {
		t.Fatal(err)
	}
	st, err = OpenState(st.Root, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	a := l1API(t, func(w http.ResponseWriter, req *http.Request) {
		hits++
		var got Result
		if json.NewDecoder(req.Body).Decode(&got) != nil || string(got.Result) != string(r.Result) {
			t.Error("result verloren")
		}
		w.WriteHeader(204)
	})
	e = Engine{State: st, API: a, Credential: c}
	e.deliverOne(context.Background())
	if hits != 1 || len(st.Journal().Results) != 0 {
		t.Fatal("Replay", hits, st.Journal())
	}
	if err = st.Append(r); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(st.Root, "test-"+r.JobID), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	e.deliverOne(context.Background())
	if hits != 1 || len(st.Journal().Results) != 1 {
		t.Fatal("Replay ohne verifizierte Freigabe")
	}
}

func TestVertrag4ClaimUndErgebnisOhneServerfelder(t *testing.T) {
	hits := 0
	response := `{"job":{"job_id":"12345678-1234-4234-8234-123456789abc","pin":"demo","commit":"` + strings.Repeat("a", 40) + `","path":"demo","algo":"sha1","manifest_seq":42,"input":{},"limits":{"wall_s":1,"tool_calls":0},"start_by":"2027-01-01T00:00:00Z"}}`
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) { hits++; io.WriteString(w, response) })
	if _, err := a.Claim(context.Background()); err != nil {
		t.Fatal("positiver v4-Claim", err)
	}
	for _, bad := range []string{strings.Replace(response, `"path":"demo"`, `"path":"../escape"`, 1), strings.Replace(response, `"pin":"demo"`, `"allowed_secret_names":[],"pin":"demo"`, 1)} {
		response = bad
		if _, err := a.Claim(context.Background()); err == nil {
			t.Fatal("Serverfeld/unsicherer Pfad akzeptiert")
		}
	}
	before := hits
	names := []string{}
	if err := a.Report(context.Background(), Result{MountedSecretNames: &names}); err == nil || hits != before {
		t.Fatal("Server-Ergebnisfeld gesendet")
	}
	if err := a.Halt(context.Background(), "pausiert", "2026-11-01T12:00:00+00:00"); err == nil || hits != before {
		t.Fatal("Halt ohne Z")
	}
	if err := a.Server("rootless"); err != nil {
		t.Fatal(err)
	}
	if err := a.Halt(context.Background(), "arbeitet", ""); err == nil || hits != before {
		t.Fatal("Server-Halt")
	}
}

func TestTestgeraetUpgradeSynchronisiertVertrag4(t *testing.T) {
	for _, oldContract := range []int{1, 3, 4} {
		t.Run(fmt.Sprint(oldContract), func(t *testing.T) {
			old := l1Manifest()
			old.Contract = oldContract
			old.TestDevice = true
			old.Pins = []Pin{}
			c := Credential{DeviceID: old.DeviceID}
			st, err := OpenState(t.TempDir(), testCipher{})
			if err != nil {
				t.Fatal(err)
			}
			cfg := Config{Contract: WindowsContract, ExpectedAccount: "synthetic", HeartbeatS: 60, PollHoldS: 25}
			cfg.Device.ID = c.DeviceID
			cfg.Device.Kind = "workstation"
			cfg.StartPolicy.Mode = "always"
			legacyCfg := cfg
			legacyCfg.Contract = oldContract
			next := l1Manifest()
			next.TestDevice = true
			next.Pins = []Pin{}
			next.Seq = 43
			// Sign with the same installation key as the cached manifest.
			pub, key, _ := ed25519.GenerateKey(nil)
			c.Pubkey = pub
			sign := func(m Manifest) SignedManifest {
				b := encode(m)
				can, _ := canonical(b)
				return SignedManifest{Manifest: b, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, can))}
			}
			if err = st.Save("manifest", Bundle{Manifest: ManifestState{Envelope: sign(old)}, Config: legacyCfg, Environments: map[string]Environment{}}); err != nil {
				t.Fatal(err)
			}
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Jnpt-Desktop-Contract") != "5" {
					t.Error("Upgrade mit altem Header")
				}
				switch r.URL.Path {
				case "/api/device/v1/config":
					json.NewEncoder(w).Encode(cfg)
				case "/api/device/v1/manifest":
					json.NewEncoder(w).Encode(sign(next))
				case "/api/device/v1/heartbeat":
					io.WriteString(w, `{"cancel":[],"manifest_seq_current":43}`)
				default:
					t.Error("unerwarteter Upgrade-Weg", r.URL.Path)
					w.WriteHeader(204)
				}
			})
			e := Engine{State: st, API: a, Credential: c, Session: func() (Session, error) { return Session{Active: true, Account: "synthetic"}, nil }}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- e.Run(ctx) }()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				if e.view().Config.Contract == WindowsContract {
					cancel()
					break
				}
				select {
				case err = <-done:
					t.Fatal("Upgrade vor Sync beendet", err)
				case <-ctx.Done():
					t.Fatal("Upgrade-Sync fehlt")
				case <-ticker.C:
				}
			}
			if err = <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			m, err := ParseManifest(e.view().Manifest.Envelope.Manifest)
			if err != nil || m.Contract != WindowsContract || !m.TestDevice || m.Seq != 43 {
				t.Fatal("Testgerät bleibt auf altem Stand", m, err)
			}
		})
	}
}

func TestAlterCacheSperrtClaims(t *testing.T) {
	m := l1Manifest()
	m.Contract = 1
	m.Pins = []Pin{}
	signed, c := l1Sign(t, encode(m), m.DeviceID)
	st, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(st.Root, "envs", "cache")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	seal, err := Snapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Contract: 1, ExpectedAccount: "synthetic"}
	cfg.StartPolicy.Mode = "always"
	b := Bundle{Manifest: ManifestState{Envelope: signed}, Config: cfg, Environments: map[string]Environment{"cache": {Dir: "cache", Files: seal}}}
	if err = st.Save("manifest", b); err != nil {
		t.Fatal(err)
	}
	claims := 0
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/device/v1/heartbeat":
			io.WriteString(w, `{"cancel":[],"manifest_seq_current":42}`)
		case "/api/device/v1/jobs/claim":
			claims++
			w.WriteHeader(204)
		default:
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"class":"unavailable","message":"unavailable"}}`)
		}
	})
	e := Engine{State: st, API: a, Credential: c, bundle: b, Session: func() (Session, error) { return Session{Active: true, Account: "synthetic"}, nil }}
	if e.isReady() {
		t.Fatal("alter Cache ist claim-bereit")
	}
	e.bundle.Config.Contract = 4
	if !e.isReady() {
		t.Fatal("positive Bereitschaftskontrolle")
	}
	e.bundle = Bundle{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err = e.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if claims != 0 {
		t.Fatal("Claim ohne neuen Vertrag-4-Sync", claims)
	}
}
