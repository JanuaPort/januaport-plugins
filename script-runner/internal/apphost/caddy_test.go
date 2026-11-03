package apphost

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run via probe/apphost.ps1: real caddy:2.11.7, isolated Docker networks.
// The ordinary suite has no Docker dependency; the probe runs this FULL package.
func TestInvariant11RealCaddy(t *testing.T) {
	admin := os.Getenv("K4_CADDY_ADMIN")
	if admin == "" {
		t.Skip("requires named Caddy 2.11.7 Docker probe")
	}
	f := setup(t)
	var browser *http.Server
	for _, srv := range []struct {
		addr    string
		handler http.Handler
	}{{":8080", f.h.Handler()}, {":8081", f.h.RelayHandler()}} {
		listener, e := Listen(context.Background(), srv.addr)
		if e != nil {
			t.Fatal(e)
		}
		server := &http.Server{Handler: srv.handler, ReadHeaderTimeout: 5 * time.Second}
		if srv.addr == ":8080" {
			browser = server
		}
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(func() { _ = server.Close() })
	}
	load := func(config string) {
		t.Helper()
		r, e := http.NewRequest("POST", admin+"/load", strings.NewReader(config))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "text/caddyfile")
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("Caddy load: %d %s", resp.StatusCode, b)
		}
	}
	readConfig := func() []byte {
		t.Helper()
		r, e := http.Get(admin + "/config/")
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = r.Body.Close() }()
		b, e := io.ReadAll(r.Body)
		if e != nil {
			t.Fatal(e)
		}
		requireStatus(t, r, 200)
		return b
	}
	base := "{\n admin 0.0.0.0:2019\n auto_https disable_redirects\n log {\n output file /probe/server.log\n }\n import /probe/apps-global.d/*.caddy\n}\nhttp://:8085 { respond /healthz healthy 200\n respond 404\n}\nimport /probe/apps.d/*.caddy\n"
	for _, dir := range []string{"/probe/apps-global.d", "/probe/apps.d"} {
		if e := os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
	}
	withoutImports := strings.ReplaceAll(strings.ReplaceAll(base, " import /probe/apps-global.d/*.caddy\n", ""), "import /probe/apps.d/*.caddy\n", "")
	load(withoutImports)
	baselineConfig := readConfig()
	load(base)
	if !bytes.Equal(baselineConfig, readConfig()) {
		t.Fatal("empty Apps imports changed Caddy configuration")
	}
	baseline, e := http.Get("http://k4-caddy:8085/healthz")
	if e != nil {
		t.Fatal(e)
	}
	requireStatus(t, baseline, 200)
	_ = baseline.Body.Close()
	site, e := os.ReadFile("../../../box/apps.caddy")
	if e != nil {
		t.Fatal(e)
	}
	global, e := os.ReadFile("../../apps-global.caddy")
	if e != nil {
		t.Fatal(e)
	}
	config := strings.ReplaceAll(string(site), "{$JNPT_APPS_DOMAIN}", "example.test")
	config = strings.ReplaceAll(config, "output stdout", "output file /probe/access.log")
	for p, b := range map[string][]byte{"apps-global.d/apps-global.caddy": global, "apps.d/apps.caddy": []byte(config)} {
		if e := os.WriteFile(filepath.Join("/probe", p), b, 0644); e != nil {
			t.Fatal(e)
		}
	}
	load(base)
	resp, e := http.Get(admin + "/config/")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	var cfg any
	if json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("Caddy config not JSON")
	}
	var visit func(any)
	upstreams := 0
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["handler"] == "reverse_proxy" {
				b, _ := json.Marshal(x["upstreams"])
				if string(b) != `[{"dial":"script-runner-apphost:8080"}]` {
					t.Fatal("browser proxy target", string(b))
				}
				upstreams++
			}
			for _, child := range x {
				visit(child)
			}
		case []any:
			for _, child := range x {
				visit(child)
			}
		}
	}
	visit(cfg)
	if upstreams != 1 {
		t.Fatal("proxy count", upstreams)
	}
	client := func() *http.Client {
		return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, "k4-caddy:443")
		}}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	} // Test CA is freshly generated per container.
	request := func(host, p string, headers http.Header) *http.Response {
		t.Helper()
		r, e := http.NewRequest("GET", "https://"+host+p, nil)
		if e != nil {
			t.Fatal(e)
		}
		r.Header = headers
		resp, e := client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	for _, p := range []string{"/internal/apps/package", "/internal/apps/exchange", "/internal/apps/tls-ask", "/tls-ask"} {
		requireStatus(t, request("a.apps.example.test", p, session()), 404)
	}
	requireStatus(t, request("a.apps.example.test", "/", session()), 200)
	c := f.login()
	h := http.Header{"Cookie": []string{c.String()}, "Authorization": []string{"log-authorization-canary"}}
	requireStatus(t, request("a.apps.example.test", "/_jnpt/callback?code=caddy-code-canary&verifier=caddy-verifier-canary", h), 200)
	// The ask relay denies unknown names: fresh TLS handshake must fail.
	r, e := client().Get("https://b.apps.example.test/")
	if e == nil {
		_ = r.Body.Close()
		t.Fatal("certificate for unknown app")
	}
	f.mu.Lock()
	f.status = 404
	f.mu.Unlock()
	// Use the relay for a stopped formerly-live name; existing certs are cached by Caddy.
	r, e = http.Get("http://script-runner-apphost:8081/tls-ask?domain=a.apps.example.test")
	if e != nil {
		t.Fatal(e)
	}
	requireStatus(t, r, 404)
	_ = r.Body.Close()
	// A failed upstream must not leak the callback query through Caddy's error logger.
	if e := browser.Close(); e != nil {
		t.Fatal(e)
	}
	requireStatus(t, request("a.apps.example.test", "/_jnpt/callback?code=upstream-error-canary", h), 502)
	logBytes, e := os.ReadFile("/probe/access.log")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(logBytes), "/_jnpt/callback") {
		t.Fatal("callback not logged")
	}
	for _, secret := range []string{"caddy-code-canary", "caddy-verifier-canary", "log-authorization-canary", c.Value, credential("app")} {
		if strings.Contains(string(logBytes), secret) {
			t.Fatal("Caddy logged secret")
		}
	}
	serverLog, e := os.ReadFile("/probe/server.log")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(logBytes)+string(serverLog), "upstream-error-canary") {
		t.Fatal("Caddy error log leaked callback code")
	}
	for _, p := range []string{"/probe/apps.d/apps.caddy", "/probe/apps-global.d/apps-global.caddy"} {
		if e := os.Remove(p); e != nil {
			t.Fatal(e)
		}
	}
	load(base)
	r, e = http.Get("http://k4-caddy:8085/healthz")
	if e != nil {
		t.Fatal(e)
	}
	requireStatus(t, r, 200)
	_ = r.Body.Close()
	if !bytes.Equal(baselineConfig, readConfig()) {
		t.Fatal("removing Apps changed baseline configuration")
	}
}

func TestInvariant10RealNetworkIsolation(t *testing.T) {
	if os.Getenv("K4_CADDY_ADMIN") == "" {
		t.Skip("requires isolated Docker networks")
	}
	// The probe script checks the runner's live listener from inside its container.
	// Here both Docker DNS and the known IP must be inaccessible from the Apps net.
	r, e := http.Get("http://k4-runner-control:8090/")
	if e == nil {
		_ = r.Body.Close()
		t.Fatal("runner control unexpectedly reachable by DNS")
	}
	addr := os.Getenv("K4_RUNNER_IP")
	if addr == "" {
		t.Fatal("missing runner IP")
	}
	conn, e := net.DialTimeout("tcp", net.JoinHostPort(addr, "8090"), time.Second)
	if e == nil {
		_ = conn.Close()
		t.Fatal("script-runner:8090 reachable across networks")
	}
}
