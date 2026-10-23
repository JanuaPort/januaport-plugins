package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestAblehnung(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	m := Manifest{Contract: 1, DeviceID: "own", Seq: 2, IssuedAt: "2026-10-23T10:00:00Z", GraceHours: 2, Pins: []Pin{}}
	sign := func(m Manifest) SignedManifest {
		b := encode(m)
		c, _ := canonical(b)
		return SignedManifest{b, base64.StdEncoding.EncodeToString(ed25519.Sign(key, c))}
	}
	good := sign(m)
	if _, e := VerifyManifest(good, pub, "own", 1); e != nil {
		t.Fatal(e)
	}
	foreign := m
	foreign.DeviceID = "foreign"
	old := m
	old.Seq = 1
	bad := good
	bad.Sig = base64.StdEncoding.EncodeToString(make([]byte, 64))
	for _, v := range []SignedManifest{sign(foreign), sign(old), bad} {
		if _, e := VerifyManifest(v, pub, "own", 1); e != ErrManifest {
			t.Fatal("Manifest akzeptiert", e)
		}
	}
}
func TestKontoUndManipulationVorSpawn(t *testing.T) {
	root := t.TempDir()
	st, _ := OpenState(root, testCipher{})
	dir := filepath.Join(root, "envs", "test")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "file"), []byte("safe"), 0600)
	seal, _ := Snapshot(dir)
	starts := 0
	c := Config{ExpectedAccount: "own"}
	c.StartPolicy.Mode = "always"
	e := Engine{State: st, Config: c, Session: func() (Session, error) { return Session{Active: true, Account: "wrong"}, nil }, Spawn: func(context.Context, Launch) (Process, error) { starts++; return nil, ErrProtocol }}
	env := Environment{Dir: "test", Files: seal, Entry: "main.py", Timeout: 10}
	r := Result{RunKey: "pin/daily/2026-10-23T10:00:00Z", ManifestSeq: 1, Commit: strings.Repeat("a", 64)}
	got := e.Execute(context.Background(), r, env, json.RawMessage(`{}`), "PRIVATE_TOKEN", 10, 10)
	if got.End != "refused" || got.Detail != "account" || starts != 0 {
		t.Fatal(got, starts)
	}
	e.Session = func() (Session, error) { return Session{Active: true, Account: "own"}, nil }
	os.WriteFile(filepath.Join(dir, "file"), []byte("evil"), 0600)
	got = e.Execute(context.Background(), r, env, json.RawMessage(`{}`), "PRIVATE_TOKEN", 10, 10)
	if got.End != "refused" || got.Detail != "hash" || starts != 0 {
		t.Fatal(got, starts)
	}
}
func TestCronZeitzoneUndSlots(t *testing.T) {
	s := Schedule{Name: "daily", Cron: "30 2 * * *", TZ: "Europe/Berlin", Input: json.RawMessage(`{}`)}
	parsed, e := parseSchedule(s)
	if e != nil {
		t.Fatal(e)
	}
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	a := parsed.Next(start)
	b := parsed.Next(a)
	if a.Equal(b) || a.Hour() != 0 || b.Hour() != 1 {
		t.Fatal(a, b)
	}
	st, _ := OpenState(t.TempDir(), testCipher{})
	p := Pin{Pin: "demo"}
	k := RunKey(p, s, a)
	ok, e := st.ReserveSlot("demo/daily", k)
	if !ok || e != nil {
		t.Fatal(e)
	}
	st, _ = OpenState(st.Root, testCipher{})
	ok, e = st.ReserveSlot("demo/daily", k)
	if ok || e != nil {
		t.Fatal("Slot wiederholt", e)
	}
}
func TestFrameGrenzen(t *testing.T) {
	for _, b := range [][]byte{{0, 0, 0, 0}, {255, 255, 255, 255}, {0, 0, 0, 2, '{'}} {
		if _, e := ReadFrame(bytes.NewReader(b)); e == nil {
			t.Fatal("Rahmen akzeptiert")
		}
	}
}

func TestVeralteterStartSynchronisiertErneut(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	slot := time.Now().UTC().Truncate(time.Minute)
	p := Pin{Pin: "demo", Commit: strings.Repeat("a", 64), Path: "demo", Algo: "sha256", DeclaredTools: []string{"tool"}, Schedules: []Schedule{{Name: "minute", Cron: "* * * * *", TZ: "UTC", Input: json.RawMessage(`{}`)}}}
	m := Manifest{Contract: 1, DeviceID: "own", Seq: 1, IssuedAt: slot.Format(time.RFC3339), GraceHours: 1, Pins: []Pin{p}}
	sign := func(m Manifest) SignedManifest {
		b := encode(m)
		c, _ := canonical(b)
		return SignedManifest{b, base64.StdEncoding.EncodeToString(ed25519.Sign(key, c))}
	}
	st, _ := OpenState(t.TempDir(), testCipher{})
	dir := envKey(p)
	os.MkdirAll(filepath.Join(st.Root, "envs", dir), 0700)
	env := Environment{Origin: p, Dir: dir, Files: []FileSeal{}, Tools: p.DeclaredTools}
	cfg := Config{Contract: 1, ExpectedAccount: "own", HeartbeatS: 60, PollHoldS: 25}
	cfg.Device.ID = "own"
	cfg.Device.Kind = "workstation"
	cfg.StartPolicy.Mode = "always"
	var seqs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/device/v1/config":
			json.NewEncoder(w).Encode(cfg)
		case "/api/device/v1/manifest":
			next := m
			next.Seq = 2
			json.NewEncoder(w).Encode(sign(next))
		case "/api/device/v1/runs/start":
			seqs = append(seqs, r.Header.Get("X-Jnpt-Manifest-Seq"))
			if len(seqs) == 1 {
				w.WriteHeader(409)
				w.Write([]byte(`{"error":{"class":"manifest","message":"Das Manifest ist nicht aktuell."}}`))
			} else {
				json.NewEncoder(w).Encode(StartReply{"token", slot.Add(time.Hour).Format(time.RFC3339), "/api/device/v1/mcp"})
			}
		default:
			t.Error("unerwarteter Weg", r.URL.Path)
		}
	}))
	defer srv.Close()
	api, _ := NewAPI(Trust{URL: srv.URL}, "token", st.Revoke)
	defer api.Close()
	e := Engine{State: st, API: api, Credential: Credential{DeviceID: "own", Pubkey: pub}, Config: cfg, bundle: Bundle{ManifestState{sign(m), slot}, cfg, map[string]Environment{dir: env}}, Session: func() (Session, error) { return Session{Active: true, Account: "own"}, nil }}
	if err := e.scheduled(context.Background(), p, p.Schedules[0], slot); err != nil {
		t.Fatal(err)
	}
	if len(seqs) != 2 || seqs[0] != "1" || seqs[1] != "2" {
		t.Fatal(seqs)
	}
	j := st.Journal()
	if len(j.Results) != 1 || j.Results[0].ManifestSeq != 2 {
		t.Fatal(j)
	}
}
