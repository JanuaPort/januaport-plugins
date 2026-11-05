package apphost

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionMustBeIssuedUnexpiredAndHostBound(t *testing.T) {
	f := setup(t)
	for _, value := range []string{"invented", numberedAppToken(1)} {
		requireStatus(t, f.request("GET", "a.apps.example.test", "/", http.Header{"Cookie": []string{"__Host-jnpt_app=" + value}}, ""), 401)
	}
	requireStatus(t, f.request("GET", "b.apps.example.test", "/", session(), ""), 401)
	f.now = f.now.Add(48 * time.Second)
	requireStatus(t, f.get("/"), 401)
}

func TestGatewayErrorsNeverServeStalePackage(t *testing.T) {
	for _, code := range []int{401, 403, 500, 302} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			f := setup(t)
			requireStatus(t, f.get("/"), 200)
			f.status = code
			f.now = f.now.Add(5 * time.Second)
			requireStatus(t, f.get("/"), 503)
			requireStatus(t, f.get("/"), 503)
		})
	}
	f := setup(t)
	requireStatus(t, f.get("/"), 200)
	f.data = []byte("broken package")
	f.commit = strings.Repeat("f", 64)
	f.now = f.now.Add(5 * time.Second)
	requireStatus(t, f.get("/"), 503)
}

func TestKeyAdapterRejectsMissingWrongAndOversizedKeys(t *testing.T) {
	f := setup(t)
	for _, value := range []string{"", credential("app"), "wrong", strings.Repeat("x", 1025)} {
		if e := os.WriteFile(f.keyPath, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
		requireStatus(t, f.get("/"), 503)
		if _, e := New(f.h.c); e == nil {
			t.Fatal("invalid startup key accepted")
		}
	}
	f.h.c.KeyPath = filepath.Join(t.TempDir(), "missing")
	if _, e := New(f.h.c); e == nil {
		t.Fatal("missing key at startup")
	}
}

func TestConfigurationCannotRedirectLoginToHTTPOrUseURLCredentials(t *testing.T) {
	f := setup(t)
	for _, raw := range []string{"http://gateway.example.test", "https://user:pass@gateway.example.test", "https://gateway.example.test/path", "https://gateway.example.test?query=canary", "https://gateway.example.test#fragment", ""} {
		c := f.h.c
		c.LoginURL = raw
		if _, e := New(c); e == nil {
			t.Fatal("invalid login URL accepted")
		}
	}
}

func TestExchangeFailureDeletesLoginAndDoesNotLeak(t *testing.T) {
	for _, reply := range []struct {
		status int
		body   string
	}{{400, "secret-error-canary"}, {200, "invalid"}, {200, `{"token":"bad","expires_at":"2099-01-01T00:00:00Z","max_age":60}`}, {200, `{"token":"` + credential("app") + `","expires_at":"2000-01-01T00:00:00Z","max_age":60}`}} {
		f := setup(t)
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(reply.status)
			_, _ = io.WriteString(w, reply.body)
		}))
		t.Cleanup(bad.Close)
		c := f.login()
		f.h.c.GatewayURL = bad.URL
		r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary", http.Header{"Cookie": []string{c.String()}}, "")
		if r.StatusCode < 400 {
			t.Fatal("exchange failure accepted")
		}
		assertDeleted(t, r, "__Host-jnpt_login")
		if strings.Contains(contents(t, r), "canary") {
			t.Fatal("gateway body leaked")
		}
	}
}

func TestGatewayRedirectNeverForwardsAnyCredential(t *testing.T) {
	f := setup(t)
	calls := 0
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(200) }))
	defer foreign.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	f.h.c.GatewayURL = redirect.URL
	requireStatus(t, f.get("/"), 503)
	r := f.request("POST", "a.apps.example.test", "/mcp", proxyHeaders(), `{}`)
	if r.Header.Get("Location") != "" {
		t.Fatal("proxy redirect destination leaked")
	}
	c := f.login()
	_ = f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary", http.Header{"Cookie": []string{c.String()}}, "")
	relay := httptest.NewRecorder()
	f.h.RelayHandler().ServeHTTP(relay, httptest.NewRequest("GET", "http://relay/tls-ask?domain=a.apps.example.test", nil))
	if calls != 0 {
		t.Fatal("redirect followed")
	}
}

func TestMCPAcceptsExactBodyLimitAndNoPrivateQuery(t *testing.T) {
	f := setup(t)
	requireStatus(t, f.request("POST", "a.apps.example.test", "/mcp?app=foreign", proxyHeaders(), strings.Repeat("x", 8<<20)), 200)
	for _, route := range []string{"/_jnpt/logout", "/mcp"} {
		for _, method := range []string{"GET", "PUT", "DELETE", "HEAD", "OPTIONS"} {
			requireStatus(t, f.request(method, "a.apps.example.test", route, proxyHeaders(), ""), 403)
		}
	}
}

func TestCallbackAlwaysClearsCookieIncludingMissingCodeAndWrongMethod(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		f := setup(t)
		c := f.login()
		r := f.request(method, "a.apps.example.test", "/_jnpt/callback", http.Header{"Cookie": []string{c.String()}}, "app=b")
		requireStatus(t, r, 400)
		assertDeleted(t, r, "__Host-jnpt_login")
		if len(f.exchanges) != 0 {
			t.Fatal("invalid callback exchanged")
		}
	}
}

func TestRelayNeverEchoesGatewayBodyAndReadsRotatedKey(t *testing.T) {
	f := setup(t)
	f.key = credential("apphost") + "b"
	if e := os.WriteFile(f.keyPath, []byte(f.key), 0600); e != nil {
		t.Fatal(e)
	}
	relay := httptest.NewServer(f.h.RelayHandler())
	defer relay.Close()
	r, e := http.Get(relay.URL + "/tls-ask?domain=a.apps.example.test&ignored=canary")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = r.Body.Close() }()
	requireStatus(t, r, 200)
	if contents(t, r) != "" {
		t.Fatal("relay body")
	}
	r, e = http.Post(relay.URL+"/tls-ask?domain=a.apps.example.test", "text/plain", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = r.Body.Close() }()
	requireStatus(t, r, 404)
}
