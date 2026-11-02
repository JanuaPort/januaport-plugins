package apphost

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Values are constructed at runtime, never credential-shaped literals in Git.
func credential(kind string) string { return "jnpt" + kind + "_" + strings.Repeat("a", 43) }

type fileSpec struct{ name, data, mode string }

// Independent writer for the public gitobjects.v1 wire contract (no git command).
func bundle(t *testing.T, app string, files []fileSpec) ([]byte, string) {
	t.Helper()
	objects := map[string][]byte{}
	put := func(kind byte, typ string, data []byte) string {
		h := sha256.Sum256(append(fmt.Appendf(nil, "%s %d%c", typ, len(data), 0), data...))
		id := hex.EncodeToString(h[:])
		objects[id] = append([]byte{kind}, data...)
		return id
	}
	entry := func(mode, name, id string) []byte {
		b, _ := hex.DecodeString(id)
		return append([]byte(mode+" "+name+"\x00"), b...)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	var tree []byte
	for _, f := range files {
		mode := f.mode
		if mode == "" {
			mode = "100644"
		}
		tree = append(tree, entry(mode, f.name, put('b', "blob", []byte(f.data)))...)
	}
	id := put('t', "tree", tree)
	id = put('t', "tree", entry("40000", app, id))
	id = put('t', "tree", entry("40000", "apps", id))
	commit := put('c', "commit", []byte("tree "+id+"\n\napp\n"))
	head, err := json.Marshal(map[string]any{"format": "gitobjects", "version": 1, "algo": "sha256", "commit": commit, "path": "apps/" + app, "objects": len(objects)})
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	frame := func(b []byte) { out = binary.BigEndian.AppendUint32(out, uint32(len(b))); out = append(out, b...) }
	frame(head)
	ids := make([]string, 0, len(objects))
	for k := range objects {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, k := range ids {
		frame(objects[k])
	}
	return out, commit
}

type fixture struct {
	t           *testing.T
	h           *Host
	server      *httptest.Server
	mu          sync.Mutex
	data        []byte
	commit      string
	status      int
	packages    int
	exchanges   []map[string]string
	proxied     http.Header
	proxyStatus int
	maxAge      int
	key         string
	keyPath     string
	now         time.Time
	logs        bytes.Buffer
}

func setup(t *testing.T, files ...fileSpec) *fixture {
	t.Helper()
	if len(files) == 0 {
		files = []fileSpec{{"index.html", "<h1>app</h1>", ""}, {"main.js", "console.log(1)", ""}, {"jnpt.app.json", "{}", ""}}
	}
	f := &fixture{t: t, status: 200, proxyStatus: 200, maxAge: 47, key: credential("apphost"), now: time.Now().Truncate(time.Second)}
	f.data, f.commit = bundle(t, "a", files)
	f.keyPath = filepath.Join(t.TempDir(), "runtime-key")
	if err := os.WriteFile(f.keyPath, []byte(f.key), 0600); err != nil {
		t.Fatal(err)
	}
	gw := httptest.NewServer(http.HandlerFunc(f.gateway))
	t.Cleanup(gw.Close)
	var err error
	f.h, err = New(Config{Domain: "example.test", GatewayURL: gw.URL, LoginURL: "https://gateway.example.test", KeyPath: f.keyPath, Now: func() time.Time { return f.now }, Logger: slog.New(slog.NewTextHandler(&f.logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewServer(f.h.Handler())
	t.Cleanup(f.server.Close)
	c := f.login()
	r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=fixture", http.Header{"Cookie": []string{c.String()}}, "")
	requireStatus(t, r, 200)
	f.exchanges = nil
	return f
}

func (f *fixture) gateway(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/mcp" && r.Header.Get("Authorization") != "Bearer "+f.key {
		w.WriteHeader(401)
		return
	}
	switch r.URL.Path {
	case "/internal/apps/package":
		f.packages++
		if r.Method != "GET" || r.URL.Query().Get("app") != "a" {
			w.WriteHeader(404)
			return
		}
		if f.status != 200 {
			w.WriteHeader(f.status)
			return
		}
		w.Header().Set("ETag", `"`+f.commit+`"`)
		if r.Header.Get("If-None-Match") == `"`+f.commit+`"` {
			w.WriteHeader(304)
			return
		}
		_, _ = w.Write(f.data)
	case "/internal/apps/exchange":
		var v struct {
			App      string `json:"app,omitempty"`
			Code     string `json:"code,omitempty"`
			Verifier string `json:"verifier,omitempty"`
			Revoke   string `json:"revoke,omitempty"`
		}
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if r.Method != "POST" || d.Decode(&v) != nil {
			w.WriteHeader(400)
			return
		}
		f.exchanges = append(f.exchanges, map[string]string{"app": v.App, "code": v.Code, "verifier": v.Verifier, "revoke": v.Revoke})
		if v.Revoke != "" {
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": credential("app"), "expires_at": f.now.Add(time.Duration(f.maxAge) * time.Second).UTC().Format(time.RFC3339), "max_age": f.maxAge})
	case "/internal/apps/tls-ask":
		if r.Method != "GET" || r.URL.Query().Get("domain") != "a.apps.example.test" || f.status != 200 {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(200)
	case "/mcp":
		f.proxied = r.Header.Clone()
		w.Header().Set("Set-Cookie", "gateway=forbidden")
		w.WriteHeader(f.proxyStatus)
		_, _ = io.WriteString(w, `{"ok":true}`)
	default:
		w.WriteHeader(404)
	}
}

func (f *fixture) request(method, host, path string, headers http.Header, body string) *http.Response {
	f.t.Helper()
	r, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	r.Host = host
	if headers != nil {
		r.Header = headers.Clone()
	}
	c := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(r)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}
func session() http.Header {
	return http.Header{"Cookie": []string{"__Host-jnpt_app=" + credential("app")}}
}
func (f *fixture) get(path string) *http.Response {
	return f.request("GET", "a.apps.example.test", path, session(), "")
}
func requireStatus(t *testing.T, r *http.Response, status int) {
	t.Helper()
	if r.StatusCode != status {
		t.Fatalf("status %d, want %d", r.StatusCode, status)
	}
}
func contents(t *testing.T, r *http.Response) string {
	t.Helper()
	b, e := io.ReadAll(r.Body)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestInvariant1VerifiedPackageAtHTTP(t *testing.T) {
	for _, kind := range []string{"valid", "tampered", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			want := 200
			if kind == "tampered" {
				f.data = bytes.Replace(f.data, []byte("<h1>app</h1>"), []byte("<h1>bad</h1>"), 1)
				want = 503
			}
			if kind == "foreign" {
				f.data, f.commit = bundle(t, "b", []fileSpec{{"index.html", "foreign", ""}})
				want = 503
			}
			r := f.get("/")
			requireStatus(t, r, want)
			if want != 200 && strings.Contains(contents(t, r), "foreign") {
				t.Fatal("unverified delivery")
			}
		})
	}
}

func TestInvariant2LoadRulesAtHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, data, mode string
		ok               bool
	}{
		{"index.html", strings.Repeat("x", 4<<20), "", true},
		{"index.html", strings.Repeat("x", (4<<20)+1), "", false},
		{"index.html", "<script>alert(1)</script>", "", false},
		{"index.html", "<p onclick='x()'>x</p>", "", false},
		{"evil.svg", "<svg><a href='java&#x73;cript:alert(1)'>x</a></svg>", "", false},
		{"evil.svg", "<svg onload='x()'/>", "", false},
		{"index.html", "-----BEGIN " + "PRIVATE KEY-----\ncanary", "", false},
		{"index.html", "x", "100755", false},
		{"index.html", "<script src='/main.js'></script>", "", true},
		{"index.html", "<a href='java\nscript:alert(1)'>x</a>", "", false},
		{"unknown.exe", "x", "", false},
	} {
		t.Run(fmt.Sprintf("%s-%d-%s", tc.name, len(tc.data), tc.mode), func(t *testing.T) {
			files := []fileSpec{{tc.name, tc.data, tc.mode}}
			if tc.name != "index.html" {
				files = append(files, fileSpec{"index.html", "safe", ""})
			}
			f := setup(t, files...)
			r := f.get("/")
			want := 503
			if tc.ok {
				want = 200
			}
			requireStatus(t, r, want)
			if !tc.ok && strings.Contains(contents(t, r), "canary") {
				t.Fatal("matched secret leaked")
			}
		})
	}
	for _, prefix := range []string{"jnpt" + "app_", "AK" + "IA", "gh" + "p_", "xo" + "xb-", "s" + "k-"} {
		t.Run(prefix, func(t *testing.T) {
			f := setup(t, fileSpec{"index.html", prefix + strings.Repeat("Z", 40), ""})
			requireStatus(t, f.get("/"), 503)
		})
	}
	for _, n := range []int{1000, 1001} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			files := []fileSpec{{"index.html", "ok", ""}}
			for i := 1; i < n; i++ {
				files = append(files, fileSpec{fmt.Sprintf("f%d.txt", i), "x", ""})
			}
			f := setup(t, files...)
			want := 200
			if n > 1000 {
				want = 503
			}
			requireStatus(t, f.get("/"), want)
		})
	}
	f := setup(t)
	requireStatus(t, f.get("/jnpt.app.json"), 404)
	f = setup(t, fileSpec{"main.js", "ok", ""})
	requireStatus(t, f.get("/"), 503)
}

func TestInvariant3HostAtHTTP(t *testing.T) {
	f := setup(t)
	for _, host := range []string{"gateway.example.test", "a.apps.foreign.test", "a_b.apps.example.test", "a.apps.example.test.evil.test", "a.b.apps.example.test", "-a.apps.example.test", "a.apps.example.test:443"} {
		requireStatus(t, f.request("GET", host, "/", session(), ""), 404)
	}
	if f.packages != 0 {
		t.Fatal("invalid host reached gateway")
	}
}

func (f *fixture) login() *http.Cookie {
	f.t.Helper()
	r := f.request("GET", "a.apps.example.test", "/", http.Header{"Accept": []string{"text/html"}}, "")
	requireStatus(f.t, r, 302)
	for _, c := range r.Cookies() {
		if c.Name == "__Host-jnpt_login" {
			u, e := url.Parse(r.Header.Get("Location"))
			if e != nil {
				f.t.Fatal(e)
			}
			h := sha256.Sum256([]byte(c.Value))
			if u.Host != "gateway.example.test" || u.Path != "/app-login/start" || u.Query().Get("app") != "a" || u.Query().Get("c") != base64.RawURLEncoding.EncodeToString(h[:]) {
				f.t.Fatal("login redirect contract")
			}
			return c
		}
	}
	f.t.Fatal("login cookie missing")
	return nil
}

func TestInvariant4BrowserBindingAtHTTP(t *testing.T) {
	f := setup(t)
	valid := f.login()
	for _, value := range []string{"", "wrong", strings.Repeat("b", 43)} {
		h := http.Header{}
		if value != "" {
			h.Set("Cookie", "__Host-jnpt_login="+value)
		}
		r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary", h, "")
		requireStatus(t, r, 400)
		assertDeleted(t, r, "__Host-jnpt_login")
	}
	if len(f.exchanges) != 0 {
		t.Fatal("unbound callback exchanged")
	}
	h := http.Header{"Cookie": []string{valid.String()}}
	requireStatus(t, f.request("GET", "b.apps.example.test", "/_jnpt/callback?code=canary", h, ""), 400)
	if len(f.exchanges) != 0 {
		t.Fatal("cross-host login")
	}
	valid = f.login()
	h.Set("Cookie", valid.String())
	f.now = f.now.Add(301 * time.Second)
	requireStatus(t, f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary", h, ""), 400)
	if len(f.exchanges) != 0 {
		t.Fatal("expired login")
	}
}
func assertDeleted(t *testing.T, r *http.Response, name string) {
	t.Helper()
	for _, c := range r.Cookies() {
		if c.Name == name && c.MaxAge < 0 {
			return
		}
	}
	t.Fatal("cookie not deleted", name)
}

func TestInvariant5AppFromHostAtHTTP(t *testing.T) {
	f := setup(t)
	c := f.login()
	h := http.Header{"Cookie": []string{c.String()}}
	r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?app=b&code=canary", h, "")
	requireStatus(t, r, 200)
	assertDeleted(t, r, "__Host-jnpt_login")
	if len(f.exchanges) != 1 || f.exchanges[0]["app"] != "a" || f.exchanges[0]["code"] != "canary" || f.exchanges[0]["verifier"] != c.Value {
		t.Fatal("exchange contract", len(f.exchanges))
	}
	requireStatus(t, f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary", h, ""), 400)
	if len(f.exchanges) != 1 {
		t.Fatal("login replay")
	}
}

func TestInvariant6CookiesAndCSPAtHTTP(t *testing.T) {
	f := setup(t)
	c := f.login()
	if strings.Replace(c.String(), c.Value, "VALUE", 1) != "__Host-jnpt_login=VALUE; Path=/; Max-Age=300; HttpOnly; Secure; SameSite=Lax" {
		t.Fatal("login cookie literal")
	}
	r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary", http.Header{"Cookie": []string{c.String()}}, "")
	requireStatus(t, r, 200)
	found := false
	for _, cookie := range r.Cookies() {
		if cookie.Name == "__Host-jnpt_app" {
			found = true
			if strings.Replace(cookie.String(), cookie.Value, "VALUE", 1) != "__Host-jnpt_app=VALUE; Path=/; Max-Age=47; HttpOnly; Secure; SameSite=Strict" {
				t.Fatal("session cookie literal")
			}
		}
	}
	if !found {
		t.Fatal("missing session")
	}
	body := contents(t, r)
	if !strings.Contains(body, `http-equiv="refresh"`) || strings.Contains(body, "<script") {
		t.Fatal("callback landing page")
	}
	for _, r := range []*http.Response{r, f.get("/")} {
		for k, v := range map[string]string{"Content-Security-Policy": "default-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'; form-action 'self'", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store"} {
			if r.Header.Get(k) != v {
				t.Fatal("header literal", k)
			}
		}
	}
	for _, age := range []int{0, -1} {
		f.maxAge = age
		c = f.login()
		r = f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=canary", http.Header{"Cookie": []string{c.String()}}, "")
		for _, cookie := range r.Cookies() {
			if cookie.Name == "__Host-jnpt_app" && cookie.MaxAge >= 0 {
				t.Fatal("nonpositive lifetime created session")
			}
		}
		assertDeleted(t, r, "__Host-jnpt_login")
	}
}

func proxyHeaders() http.Header {
	h := session()
	h.Set("Origin", "https://a.apps.example.test")
	h.Set("X-Jnpt-App", "1")
	return h
}
func TestInvariant7ProxyAtHTTP(t *testing.T) {
	f := setup(t)
	for _, mode := range []string{"origin", "marker", "method", "session"} {
		h := proxyHeaders()
		method := "POST"
		switch mode {
		case "origin":
			h.Set("Origin", "https://foreign.test")
		case "marker":
			h.Del("X-Jnpt-App")
		case "method":
			method = "GET"
		case "session":
			h.Del("Cookie")
		}
		r := f.request(method, "a.apps.example.test", "/mcp", h, `{}`)
		if r.StatusCode < 400 {
			t.Fatal("forbidden proxy", mode)
		}
	}
	if f.proxied != nil {
		t.Fatal("forbidden request reached gateway")
	}
	h := proxyHeaders()
	for _, k := range []string{"Authorization", "X-Jnpt-Run-Token", "X-Jnpt-Other", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host"} {
		h.Set(k, "browser-canary")
	}
	r := f.request("POST", "a.apps.example.test", "/mcp?secret=query-canary", h, `{}`)
	requireStatus(t, r, 200)
	if f.proxied.Get("Authorization") != "Bearer "+credential("app") {
		t.Fatal("bearer not set")
	}
	for k := range f.proxied {
		lower := strings.ToLower(k)
		if lower == "cookie" || strings.HasPrefix(lower, "x-jnpt-") || strings.HasPrefix(lower, "x-forwarded-") || lower == "forwarded" {
			t.Fatal("header reached gateway", k)
		}
	}
	if r.Header.Get("Set-Cookie") != "" {
		t.Fatal("gateway cookie forwarded")
	}
	body := contents(t, r)
	for _, secret := range []string{credential("app"), "browser-canary", "query-canary"} {
		if strings.Contains(body+f.logs.String(), secret) {
			t.Fatal("secret in response/log")
		}
	}
	r = f.request("POST", "a.apps.example.test", "/mcp", proxyHeaders(), strings.Repeat("x", (8<<20)+1))
	requireStatus(t, r, 413)
	f.proxyStatus = 401
	r = f.request("POST", "a.apps.example.test", "/mcp", proxyHeaders(), `{}`)
	requireStatus(t, r, 401)
	assertDeleted(t, r, "__Host-jnpt_app")
}

func TestCacheETagRevocationAndKeyRotationAtHTTP(t *testing.T) {
	f := setup(t)
	requireStatus(t, f.get("/"), 200)
	requireStatus(t, f.get("/"), 200)
	if f.packages != 1 {
		t.Fatal("cache miss")
	}
	f.now = f.now.Add(5 * time.Second)
	requireStatus(t, f.get("/"), 200)
	if f.packages != 2 {
		t.Fatal("304 not requested")
	}
	f.status = 404
	f.now = f.now.Add(5 * time.Second)
	requireStatus(t, f.get("/"), 404)
	f.status = 200
	requireStatus(t, f.get("/"), 404)
	f.now = f.now.Add(5 * time.Second)
	requireStatus(t, f.get("/"), 200)
	f.key = credential("apphost") + "b"
	if err := os.WriteFile(f.keyPath, []byte(f.key), 0600); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(5 * time.Second)
	requireStatus(t, f.get("/"), 200)
	if err := os.Remove(f.keyPath); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, f.get("/"), 503)
}

func TestStaticLogoutAndRelaySeparationAtHTTP(t *testing.T) {
	f := setup(t)
	requireStatus(t, f.request("GET", "a.apps.example.test", "/main.js", nil, ""), 401)
	requireStatus(t, f.request("POST", "a.apps.example.test", "/", session(), ""), 405)
	r := f.request("HEAD", "a.apps.example.test", "/", session(), "")
	requireStatus(t, r, 200)
	if contents(t, r) != "" {
		t.Fatal("HEAD body")
	}
	for _, p := range []string{"/internal/apps/package", "/internal/apps/exchange", "/internal/apps/tls-ask", "/tls-ask", "/../index.html", "/%2e%2e/index.html"} {
		requireStatus(t, f.get(p), 404)
	}
	for _, method := range []string{"GET", "POST"} {
		requireStatus(t, f.request(method, "a.apps.example.test", "/_jnpt/logout", session(), ""), 403)
	}
	if len(f.exchanges) != 0 {
		t.Fatal("forbidden logout exchanged")
	}
	r = f.request("POST", "a.apps.example.test", "/_jnpt/logout", proxyHeaders(), "")
	requireStatus(t, r, 204)
	assertDeleted(t, r, "__Host-jnpt_app")
	if len(f.exchanges) != 1 || f.exchanges[0]["revoke"] != credential("app") {
		t.Fatal("revoke contract")
	}
	relay := httptest.NewServer(f.h.RelayHandler())
	defer relay.Close()
	for _, tc := range []struct {
		path   string
		status int
	}{{"/tls-ask?domain=a.apps.example.test", 200}, {"/tls-ask?domain=b.apps.example.test", 404}, {"/tls-ask?domain=a.apps.foreign.test", 404}, {"/mcp", 404}, {"/", 404}} {
		resp, err := http.Get(relay.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, resp, tc.status)
		_ = resp.Body.Close()
	}
	f.status = 404
	r2, err := http.Get(relay.URL + "/tls-ask?domain=a.apps.example.test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r2.Body.Close() }()
	requireStatus(t, r2, 404)
}

func TestLogsNeverContainCredentialsOrQuery(t *testing.T) {
	f := setup(t)
	c := f.login()
	h := proxyHeaders()
	h.Add("Cookie", c.String())
	h.Set("Authorization", "log-auth-canary")
	_ = f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=log-code-canary&verifier=log-verifier-canary", h, "")
	_ = f.request("POST", "a.apps.example.test", "/mcp?query=log-query-canary", h, `{}`)
	for _, v := range []string{c.Value, credential("app"), credential("apphost"), "log-auth-canary", "log-code-canary", "log-verifier-canary", "log-query-canary"} {
		if strings.Contains(f.logs.String(), v) {
			t.Fatal("secret logged")
		}
	}
}
