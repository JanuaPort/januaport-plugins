package client

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPaketMedientypAmHTTPAdapter(t *testing.T) {
	h, raw := goldenPackage(t)
	for _, mime := range []string{"application/json", "application/octet-stream", "application/vnd.jnpt.gitobjects.v1"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", mime)
			w.Write(raw)
		}))
		a, err := NewAPI(Trust{URL: srv.URL}, "token", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.Package(context.Background(), Pin{Commit: h.Commit, Algo: h.Algo, Path: h.Path})
		a.Close()
		srv.Close()
		if (err == nil) != (mime == "application/vnd.jnpt.gitobjects.v1") {
			t.Fatal("Medientyp", mime, err)
		}
	}
}

func TestPairTLSUndLeererPubkey(t *testing.T) {
	hits := 0
	key := ""
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if _, ok := r.Header["Authorization"]; ok {
			t.Error("pair mit Authorization")
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"device_id": "device", "token": "jnpt_test_secret", "expires_at": "2027-01-01T00:00:00Z", "anlage_pubkey": key})
	}))
	defer srv.Close()
	api, e := NewAPI(Trust{URL: srv.URL}, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = api.Pair(context.Background(), "ABCD-EFGH-JKMN", "test"); e == nil || hits != 0 {
		t.Fatalf("ungültiges TLS: %v hits=%d", e, hits)
	}
	hash := sha256.Sum256(srv.Certificate().Raw)
	api, e = NewAPI(Trust{URL: srv.URL, Fingerprint: hex.EncodeToString(hash[:])}, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = api.Pair(context.Background(), "ABCD-EFGH-JKMN", "test"); e == nil {
		t.Fatal("leerer Pubkey akzeptiert")
	}
	key = base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, e = api.Pair(context.Background(), "ABCD-EFGH-JKMN", "test"); e != nil {
		t.Fatal(e)
	}
}

func TestNurAnlageUndKeinePrivatenBodys(t *testing.T) {
	var bodies [][]byte
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		paths = append(paths, r.URL.Path)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "heartbeat") {
			io.WriteString(w, `{"cancel":[],"manifest_seq_current":1}`)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	api, e := NewAPI(Trust{URL: srv.URL}, "jnpt_test_secret", nil)
	if e != nil {
		t.Fatal(e)
	}
	c := Config{ExpectedAccount: "PRIVATE_ACCOUNT"}
	c.StartPolicy.Mode = "idle"
	c.StartPolicy.IdleMinutes = 3
	s := Session{Active: true, Account: "PRIVATE_ACCOUNT", Idle: 999999999999}
	_, e = api.Heartbeat(context.Background(), Heartbeat{1, Ready(s, c, true), "test", 1})
	if e != nil {
		t.Fatal(e)
	}
	if e = api.Report(context.Background(), Result{RunKey: "demo/daily/2026-10-23T10:00:00Z", ManifestSeq: 1, Commit: strings.Repeat("a", 64), End: "ok", Log: "", FinishedAt: "2026-10-23T10:01:00Z"}); e != nil {
		t.Fatal(e)
	}
	for _, b := range bodies {
		for _, secret := range []string{"PRIVATE_ACCOUNT", "999999", "stdout", "session", "idle", "account", "jnpt_test_secret"} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("verbotene Daten: %s", secret)
			}
		}
	}
	if len(paths) != 2 || paths[1] != "/api/device/v1/runs/result" {
		t.Fatal(paths)
	}
}

func TestRedirectUndRemoteHTTPAbgewiesen(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer src.Close()
	a, _ := NewAPI(Trust{URL: src.URL}, "secret", nil)
	if _, e := a.Pair(context.Background(), "ABCD-EFGH-JKMN", "test"); e == nil || hits != 0 {
		t.Fatal("Redirect gefolgt")
	}
	if _, e := NewAPI(Trust{URL: "http://192.0.2.1"}, "", nil); e == nil {
		t.Fatal("Remote HTTP")
	}
}
