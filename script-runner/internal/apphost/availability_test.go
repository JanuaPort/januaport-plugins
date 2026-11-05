package apphost

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestF2AnonymousNavigationsCannotExhaustLoginAtHTTP(t *testing.T) {
	f := setup(t)
	for i := 0; i < 5000; i++ {
		r := f.request("GET", "a.apps.example.test", "/", http.Header{"Accept": {"text/html"}}, "")
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		if r.StatusCode != 302 {
			t.Fatalf("anonymous navigation %d: status %d", i+1, r.StatusCode)
		}
	}
	c := f.login()
	r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=after-navigation", http.Header{"Cookie": {c.String()}}, "")
	requireStatus(t, r, 200)
	requireStatus(t, f.get("/"), 200)
}

// This stub models the core's code/app/PKCE binding. The callback arrives at a
// fresh Host with no prior navigation; only the browser carries the verifier.
func TestF2StatelessCallbackCoreBindingAtHTTP(t *testing.T) {
	f := setup(t)
	c := f.login()
	challenge := sha256.Sum256([]byte(c.Value))
	var calls atomic.Int32
	var used atomic.Bool
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var v struct{ App, Code, Verifier string }
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if r.Method != "POST" || r.URL.Path != "/internal/apps/exchange" || r.Header.Get("Authorization") != "Bearer "+f.key || d.Decode(&v) != nil {
			w.WriteHeader(400)
			return
		}
		if v.App != "a" || v.Code != "issued-code" || sha256.Sum256([]byte(v.Verifier)) != challenge || used.Swap(true) {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": credential("app"), "expires_at": f.now.Add(time.Minute), "max_age": 60})
	}))
	t.Cleanup(gw.Close)
	config := f.h.c
	config.GatewayURL = gw.URL
	h, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewServer(h.Handler())
	t.Cleanup(f.server.Close)
	for _, tc := range []struct {
		host, verifier string
		want           int
	}{
		{"a", strings.Repeat("b", 43), 401},
		{"b", c.Value, 401},
		{"a", c.Value, 200},
		{"a", c.Value, 401}, // Code reuse is rejected by the core, not a local table.
	} {
		r := f.request("GET", tc.host+".apps.example.test", "/_jnpt/callback?code=issued-code&app=b", http.Header{"Cookie": {"__Host-jnpt_login=" + tc.verifier}}, "")
		requireStatus(t, r, tc.want)
		assertDeleted(t, r, "__Host-jnpt_login")
	}
	if calls.Load() != 4 {
		t.Fatal("valid cookie form must reach core exchange", calls.Load())
	}
}

func TestF2SessionCapacityEvictsEarliestAtHTTP(t *testing.T) {
	f := setup(t)
	// Arrange a full table, with the earliest expiry neither first nor last.
	f.h.sessions = make(map[string]binding)
	for i := 0; i < 4096; i++ {
		f.h.sessions[numberedAppToken(i)] = binding{"a", f.now.Add(time.Hour)}
	}
	f.h.sessions[numberedAppToken(2000)] = binding{"a", f.now.Add(time.Second)}
	c := f.login()
	r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=capacity", http.Header{"Cookie": {c.String()}}, "")
	requireStatus(t, r, 200)
	requireStatus(t, f.get("/"), 200)
	for _, tc := range []struct {
		token string
		want  int
	}{{numberedAppToken(2000), 401}, {numberedAppToken(0), 200}, {numberedAppToken(4095), 200}} {
		requireStatus(t, f.request("GET", "a.apps.example.test", "/", http.Header{"Cookie": {"__Host-jnpt_app=" + tc.token}}, ""), tc.want)
	}
	f.h.mu.Lock()
	defer f.h.mu.Unlock()
	if len(f.h.sessions) != 4096 {
		t.Fatal("session table must stay bounded", len(f.h.sessions))
	}
}

func numberedAppToken(i int) string {
	return "jnpt" + "app_" + fmt.Sprintf("%026d", i) + "." + strings.Repeat("B", 26)
}

func TestH2PackageFetchIsolationAndCoalescingAtHTTP(t *testing.T) {
	f := setup(t)
	waiterReturned := make(chan struct{})
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.h.Handler().ServeHTTP(w, r)
		if r.Header.Get("X-Probe-Waiter") == "1" {
			close(waiterReturned)
		}
	}))
	t.Cleanup(f.server.Close)
	bData, bCommit := bundle(t, "b", []fileSpec{{"index.html", "app-b", ""}})
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var once sync.Once
	var fetches atomic.Int32
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/apps/package" || r.Header.Get("Authorization") != "Bearer "+f.key {
			w.WriteHeader(401)
			return
		}
		data, commit := bData, bCommit
		if r.URL.Query().Get("app") == "a" {
			fetches.Add(1)
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			data, commit = f.data, f.commit
		}
		w.Header().Set("ETag", `"`+commit+`"`)
		_, _ = w.Write(data)
	}))
	t.Cleanup(gw.Close)
	// Release before httptest cleanup even when an assertion fails.
	defer unblock()
	f.h.c.GatewayURL = gw.URL
	bToken := numberedAppToken(1)
	f.h.sessions[bToken] = binding{"b", f.now.Add(time.Hour)}
	request := func(ctx context.Context, app, token string, waiter bool) (int, string, error) {
		r, err := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/", nil)
		if err != nil {
			return 0, "", err
		}
		r.Host = app + ".apps.example.test"
		r.Header.Set("Cookie", "__Host-jnpt_app="+token)
		if waiter {
			r.Header.Set("X-Probe-Waiter", "1")
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			return 0, "", err
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			n, _, err := request(ctx, "a", credential("app"), false)
			if err == nil && n != 200 {
				err = fmt.Errorf("app a: %d", n)
			}
			results <- err
		}()
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("app a never reached gateway")
	}
	bctx, bcancel := context.WithTimeout(ctx, time.Second)
	defer bcancel()
	n, body, err := request(bctx, "b", bToken, false)
	if err != nil || n != 200 || body != "app-b" {
		t.Fatalf("app b blocked by app a: status=%d err=%v", n, err)
	}
	// A waiting request can cancel independently of the in-flight fetch.
	wctx, wcancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer wcancel()
	_, _, err = request(wctx, "a", credential("app"), true)
	if err == nil {
		t.Fatal("canceled waiter remained blocked")
	}
	select {
	case <-waiterReturned:
	case <-time.After(time.Second):
		t.Fatal("canceled waiter still occupies the apphost handler")
	}
	// All remaining requests share the one result, including late arrivals.
	unblock()
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if fetches.Load() != 1 {
		t.Fatal("concurrent fetches for one app", fetches.Load())
	}
}
