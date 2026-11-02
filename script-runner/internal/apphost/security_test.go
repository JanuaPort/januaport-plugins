package apphost

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func noCORS(t *testing.T, h http.Header) {
	t.Helper()
	for key := range h {
		if strings.HasPrefix(strings.ToLower(key), "access-control-") {
			t.Fatalf("C-1: forbidden response header %s", key)
		}
	}
}

func TestC1NoCORSOnAnyResponse(t *testing.T) {
	f := setup(t)
	for _, tc := range []struct {
		method, host, path string
		h                  http.Header
		body               string
	}{
		{"GET", "foreign.test", "/", nil, ""},
		{"GET", "a.apps.example.test", "/", session(), ""},
		{"HEAD", "a.apps.example.test", "/", session(), ""},
		{"GET", "a.apps.example.test", "/missing", session(), ""},
		{"GET", "a.apps.example.test", "/main.js", nil, ""},
		{"GET", "a.apps.example.test", "/", http.Header{"Accept": []string{"text/html"}}, ""},
		{"GET", "a.apps.example.test", "/_jnpt/callback?code=canary", nil, ""},
		{"POST", "a.apps.example.test", "/", session(), ""},
		{"OPTIONS", "a.apps.example.test", "/mcp", http.Header{"Origin": []string{"https://b.apps.example.test"}, "Access-Control-Request-Method": []string{"POST"}}, ""},
		{"POST", "a.apps.example.test", "/mcp", proxyHeaders(), `{}`},
		{"POST", "a.apps.example.test", "/mcp", proxyHeaders(), strings.Repeat("x", (8<<20)+1)},
		{"POST", "a.apps.example.test", "/_jnpt/logout", nil, ""},
	} {
		r := f.request(tc.method, tc.host, tc.path, tc.h, tc.body)
		noCORS(t, r.Header)
	}
	c := f.login()
	r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary", http.Header{"Cookie": []string{c.String()}}, "")
	requireStatus(t, r, 200)
	noCORS(t, r.Header)
	f.proxyStatus = 401
	r = f.request("POST", "a.apps.example.test", "/mcp", proxyHeaders(), `{}`)
	requireStatus(t, r, 401)
	noCORS(t, r.Header)
	c = f.login()
	r = f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary", http.Header{"Cookie": []string{c.String()}}, "")
	noCORS(t, r.Header)
	r = f.request("POST", "a.apps.example.test", "/_jnpt/logout", proxyHeaders(), "")
	requireStatus(t, r, 204)
	noCORS(t, r.Header)
	if e := os.Remove(f.keyPath); e != nil {
		t.Fatal(e)
	}
	r = f.get("/")
	requireStatus(t, r, 503)
	noCORS(t, r.Header)
	for _, p := range []string{"/", "/tls-ask?domain=foreign.test", "/tls-ask?domain=a.apps.example.test"} {
		r := httptest.NewRecorder()
		f.h.RelayHandler().ServeHTTP(r, httptest.NewRequest("GET", "http://relay"+p, nil))
		noCORS(t, r.Header())
	}
}

func TestC2CallbackHasLiteralRefreshAndSecurityHeaders(t *testing.T) {
	f := setup(t)
	c := f.login()
	r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary&next=https://foreign.test/&redirect_uri=https://foreign.test/", http.Header{"Cookie": []string{c.String()}}, "")
	requireStatus(t, r, 200)
	if contents(t, r) != `<!doctype html><meta http-equiv="refresh" content="0;url=/"><a href="/">Weiter</a>` {
		t.Fatal("C-2: callback landing page changed")
	}
	for k, v := range map[string]string{"Content-Security-Policy": "default-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'; form-action 'self'", "X-Content-Type-Options": "nosniff", "Cache-Control": "no-store", "Referrer-Policy": "no-referrer"} {
		if r.Header.Get(k) != v {
			t.Fatal("C-2: security header", k)
		}
	}
}

func TestC3VerifierEntropyAndHostCookies(t *testing.T) {
	f := setup(t)
	seen := map[string]bool{}
	for range 16 {
		c := f.login()
		b, e := base64.RawURLEncoding.DecodeString(c.Value)
		if e != nil || len(b) < 32 || seen[c.Value] {
			t.Fatal("C-3: verifier entropy")
		}
		seen[c.Value] = true
		if c.Domain != "" || !c.Secure || c.Path != "/" || strings.Replace(c.String(), c.Value, "VALUE", 1) != "__Host-jnpt_login=VALUE; Path=/; Max-Age=300; HttpOnly; Secure; SameSite=Lax" {
			t.Fatal("C-3: login cookie")
		}
		r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary", http.Header{"Cookie": []string{c.String()}}, "")
		found := false
		for _, cookie := range r.Cookies() {
			if cookie.Domain != "" || cookie.Path != "/" || !cookie.Secure || !strings.HasPrefix(cookie.Name, "__Host-") {
				t.Fatal("C-3: cookie scope")
			}
			if cookie.Name == "__Host-jnpt_app" {
				found = true
				if strings.Replace(cookie.String(), cookie.Value, "VALUE", 1) != "__Host-jnpt_app=VALUE; Path=/; Max-Age=47; HttpOnly; Secure; SameSite=Strict" {
					t.Fatal("C-3: session cookie literal")
				}
			}
		}
		if !found {
			t.Fatal("C-3: session missing")
		}
	}
	src, e := os.ReadFile("apphost.go")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(src), `"crypto/rand"`) || !strings.Contains(string(src), "rand.Read(b)") {
		t.Fatal("C-3: crypto/rand source missing")
	}
}

func TestI2ExplicitTCPUnderMultipathEnvironment(t *testing.T) {
	t.Setenv("GODEBUG", "multipathtcp=1")
	if !(&net.Dialer{}).MultipathTCP() || !(&net.ListenConfig{}).MultipathTCP() {
		t.Fatal("MPTCP positive control")
	}
	d := newDialer()
	lc := newListenConfig()
	if d.MultipathTCP() || lc.MultipathTCP() {
		t.Fatal("I-2: MPTCP enabled")
	}
	l, e := Listen(context.Background(), "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = l.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := l.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	tr := newTransport()
	c, e := tr.DialContext(context.Background(), "tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = c.Close() }()
	peer := <-accepted
	defer func() { _ = peer.Close() }()
	for _, c := range []net.Conn{c, peer} {
		enabled, e := c.(*net.TCPConn).MultipathTCP()
		if e != nil || enabled {
			t.Fatal("I-2: connection used MPTCP", e)
		}
	}
}

func TestH1RelaySyntaxAndOnlyDomainAtHTTP(t *testing.T) {
	f := setup(t)
	calls := 0
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/internal/apps/tls-ask" || r.URL.RawQuery != "domain=a.apps.example.test" || r.Header.Get("Authorization") != "Bearer "+f.key {
			t.Error("H-1: relay contract")
		}
		w.WriteHeader(200)
	}))
	defer gw.Close()
	f.h.c.GatewayURL = gw.URL
	relay := httptest.NewServer(f.h.RelayHandler())
	defer relay.Close()
	for _, domain := range []string{"", "example.test", "a_b.apps.example.test", "a.apps.foreign.test", "a.apps.example.test:443", "a.b.apps.example.test", "a.apps.example.test.", "a.apps.example.test/path", "A.apps.example.test", "-a.apps.example.test"} {
		r, e := http.Get(relay.URL + "/tls-ask?domain=" + url.QueryEscape(domain))
		if e != nil {
			t.Fatal(e)
		}
		requireStatus(t, r, 403)
		if contents(t, r) != "" {
			t.Fatal("H-1: error body")
		}
		_ = r.Body.Close()
	}
	if calls != 0 {
		t.Fatal("H-1: invalid domain forwarded")
	}
	r, e := http.Get(relay.URL + "/tls-ask?domain=a.apps.example.test&code=canary&app=b&url=http://foreign.test")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = r.Body.Close() }()
	requireStatus(t, r, 200)
	if calls != 1 || contents(t, r) != "" {
		t.Fatal("H-1: positive relay")
	}
}
