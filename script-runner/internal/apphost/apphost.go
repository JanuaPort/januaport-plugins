// Package apphost serves verified browser apps through a private gateway.
package apphost

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const csp = "default-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'; form-action 'self'"

// Config contains operator-controlled endpoints, never request-controlled URLs.
type Config struct {
	Domain, GatewayURL, LoginURL, KeyPath string
	Logger                                *slog.Logger
	Now                                   func() time.Time
}
type cached struct {
	checked time.Time
	commit  string
	files   map[string]asset
	status  int
}
type binding struct {
	app     string
	expires time.Time
}

// Host holds only RAM state. A restart requires a fresh browser login.
type Host struct {
	c        Config
	client   *http.Client
	mu       sync.Mutex
	cache    map[string]cached
	logins   map[string]binding
	sessions map[string]binding
}

var label = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var opaque = regexp.MustCompile(`^[A-Za-z0-9_-]{43,256}$`)

// New validates configuration and fails closed when the materialized key is absent.
func New(c Config) (*Host, error) {
	if c.KeyPath == "" {
		c.KeyPath = "/run/secrets/jnpt/runtime-key"
	}
	if _, err := (&Host{c: c}).key(); err != nil {
		return nil, err
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	for _, p := range strings.Split(c.Domain, ".") {
		if !label.MatchString(p) {
			return nil, errors.New("apphost: JNPT_APPS_DOMAIN ungültig")
		}
	}
	for i, raw := range []string{c.GatewayURL, c.LoginURL} {
		u, e := url.Parse(raw)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || (i == 1 && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			return nil, errors.New("apphost: Gateway-URLs fehlen oder sind ungültig")
		}
	}
	h := &Host{c: c, client: &http.Client{Timeout: 30 * time.Second, Transport: newTransport(), CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, cache: map[string]cached{}, logins: map[string]binding{}, sessions: map[string]binding{}}
	return h, nil
}
func (h *Host) key() (string, error) {
	f, e := os.Open(h.c.KeyPath)
	if e != nil {
		return "", errors.New("apphost: runtime-key fehlt oder ist nicht lesbar")
	}
	defer func() { _ = f.Close() }()
	b, e := io.ReadAll(io.LimitReader(f, 1025))
	key := strings.TrimSpace(string(b))
	suffix, ok := strings.CutPrefix(key, "jnptapphost_")
	if e != nil || len(b) > 1024 || !ok || !opaque.MatchString(suffix) {
		return "", errors.New("apphost: runtime-key ungültig")
	}
	return key, nil
}
func (h *Host) app(host string) string {
	suffix := ".apps." + h.c.Domain
	if !strings.HasSuffix(host, suffix) {
		return ""
	}
	name := strings.TrimSuffix(host, suffix)
	if !label.MatchString(name) {
		return ""
	}
	return name
}
func security(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}
func status(w http.ResponseWriter, n int) { w.WriteHeader(n) }
func cookie(w http.ResponseWriter, name, value string, age int, same http.SameSite) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: age, HttpOnly: true, Secure: true, SameSite: same})
}
func deleteLogin(w http.ResponseWriter) { cookie(w, "__Host-jnpt_login", "", -1, http.SameSiteLaxMode) }
func deleteSession(w http.ResponseWriter) {
	cookie(w, "__Host-jnpt_app", "", -1, http.SameSiteStrictMode)
}

// Handler is exclusively the browser listener (:8080).
func (h *Host) Handler() http.Handler { return http.HandlerFunc(h.serve) }
func (h *Host) serve(w http.ResponseWriter, r *http.Request) {
	security(w)
	app := h.app(r.Host)
	if app == "" {
		status(w, 404)
		return
	}
	if r.URL.Path == "/_jnpt/callback" {
		h.callback(w, r, app)
		return
	}
	key, e := h.key()
	if e != nil {
		status(w, 503)
		return
	}
	switch r.URL.Path {
	case "/mcp":
		h.proxy(w, r, app)
		return
	case "/_jnpt/logout":
		h.logout(w, r, app, key)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/internal/") || strings.HasPrefix(r.URL.Path, "/_jnpt/") || r.URL.Path == "/tls-ask" {
		status(w, 404)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		status(w, 405)
		return
	}
	if _, ok := h.session(r, app); !ok {
		if r.Method == "GET" && (r.Header.Get("Sec-Fetch-Mode") == "navigate" || strings.Contains(r.Header.Get("Accept"), "text/html")) {
			h.login(w, r, app)
		} else {
			status(w, 401)
		}
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if path.Clean(name) != name || strings.Contains(name, "\\") || strings.HasPrefix(name, "../") || path.Base(name) == "jnpt.app.json" {
		status(w, 404)
		return
	}
	files, code := h.packageFor(r.Context(), app, key)
	if code != 200 {
		status(w, code)
		return
	}
	f, ok := files[name]
	if !ok {
		status(w, 404)
		return
	}
	w.Header().Set("Content-Type", f.mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(f.data)))
	status(w, 200)
	if r.Method == "GET" {
		_, _ = w.Write(f.data)
	}
}

func (h *Host) request(ctx context.Context, method, route, key string, body []byte) (*http.Response, error) {
	r, e := http.NewRequestWithContext(ctx, method, h.c.GatewayURL+route, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	r.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	return h.client.Do(r)
}
func (h *Host) packageFor(ctx context.Context, app, key string) (map[string]asset, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.c.Now()
	old := h.cache[app]
	if !old.checked.IsZero() && now.Sub(old.checked) < 5*time.Second {
		return old.files, old.status
	}
	// Bound host-driven RAM use; evict old entries before admitting another app.
	if len(h.cache) >= 256 {
		for name, c := range h.cache {
			if now.Sub(c.checked) >= 5*time.Second {
				delete(h.cache, name)
			}
		}
		if len(h.cache) >= 256 {
			return nil, 503
		}
	}
	r, e := http.NewRequestWithContext(ctx, "GET", h.c.GatewayURL+"/internal/apps/package?app="+url.QueryEscape(app), nil)
	if e != nil {
		return nil, 503
	}
	r.Header.Set("Authorization", "Bearer "+key)
	if old.commit != "" {
		r.Header.Set("If-None-Match", `"`+old.commit+`"`)
	}
	next := cached{checked: now, status: 503}
	defer func() { h.cache[app] = next }()
	resp, e := h.client.Do(r)
	if e != nil {
		return nil, 503
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 404 {
		next.status = 404
		return nil, 404
	}
	if resp.StatusCode == 304 && old.files != nil {
		next = old
		next.checked = now
		return next.files, 200
	}
	if resp.StatusCode != 200 {
		return nil, 503
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (128<<20)+1))
	if e != nil || len(b) > 128<<20 {
		return nil, 503
	}
	etag := resp.Header.Get("ETag")
	if len(etag) != 66 || etag[0] != '"' || etag[65] != '"' {
		return nil, 503
	}
	commit := etag[1:65]
	files, e := loadPackage(b, commit, app)
	if e != nil {
		h.c.Logger.Warn("app_package_rejected")
		return nil, 503
	}
	next.files, next.commit, next.status = files, commit, 200
	return files, 200
}

func (h *Host) login(w http.ResponseWriter, r *http.Request, app string) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		status(w, 503)
		return
	}
	v := base64.RawURLEncoding.EncodeToString(b)
	h.mu.Lock()
	h.prune()
	if len(h.logins) >= 4096 {
		h.mu.Unlock()
		status(w, 503)
		return
	}
	h.logins[v] = binding{app, h.c.Now().Add(300 * time.Second)}
	h.mu.Unlock()
	cookie(w, "__Host-jnpt_login", v, 300, http.SameSiteLaxMode)
	sum := sha256.Sum256([]byte(v))
	q := url.Values{"app": {app}, "c": {base64.RawURLEncoding.EncodeToString(sum[:])}}
	http.Redirect(w, r, h.c.LoginURL+"/app-login/start?"+q.Encode(), http.StatusFound)
}
func (h *Host) prune() {
	for _, m := range []map[string]binding{h.logins, h.sessions} {
		for k, b := range m {
			if !h.c.Now().Before(b.expires) {
				delete(m, k)
			}
		}
	}
}
func (h *Host) session(r *http.Request, app string) (string, bool) {
	c, e := r.Cookie("__Host-jnpt_app")
	if e != nil {
		return "", false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.sessions[c.Value]
	return c.Value, ok && b.app == app && h.c.Now().Before(b.expires)
}
func (h *Host) callback(w http.ResponseWriter, r *http.Request, app string) {
	deleteLogin(w)
	c, e := r.Cookie("__Host-jnpt_login")
	if e != nil {
		status(w, 400)
		return
	}
	h.mu.Lock()
	b, ok := h.logins[c.Value]
	delete(h.logins, c.Value)
	h.mu.Unlock()
	if !ok || b.app != app || !h.c.Now().Before(b.expires) || r.Method != "GET" || r.URL.Query().Get("code") == "" {
		status(w, 400)
		return
	}
	key, e := h.key()
	if e != nil {
		status(w, 503)
		return
	}
	payload, _ := json.Marshal(map[string]string{"app": app, "code": r.URL.Query().Get("code"), "verifier": c.Value})
	resp, e := h.request(r.Context(), "POST", "/internal/apps/exchange", key, payload)
	if e != nil {
		status(w, 502)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		status(w, 401)
		return
	}
	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
		MaxAge    int       `json:"max_age"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&result) != nil {
		status(w, 502)
		return
	}
	suffix, valid := strings.CutPrefix(result.Token, "jnptapp_")
	age := result.MaxAge
	if remaining := int(result.ExpiresAt.Sub(h.c.Now()).Seconds()); remaining < age {
		age = remaining
	}
	if !valid || !opaque.MatchString(suffix) || age <= 0 {
		status(w, 401)
		return
	}
	h.mu.Lock()
	h.prune()
	if len(h.sessions) >= 4096 {
		h.mu.Unlock()
		status(w, 503)
		return
	}
	h.sessions[result.Token] = binding{app, h.c.Now().Add(time.Duration(age) * time.Second)}
	h.mu.Unlock()
	cookie(w, "__Host-jnpt_app", result.Token, age, http.SameSiteStrictMode)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><meta http-equiv="refresh" content="0;url=/"><a href="/">Weiter</a>`)
}
func sameOrigin(r *http.Request) bool {
	return r.Method == "POST" && r.Header.Get("Origin") == "https://"+r.Host && r.Header.Get("X-Jnpt-App") == "1"
}
func (h *Host) logout(w http.ResponseWriter, r *http.Request, app, key string) {
	if !sameOrigin(r) {
		status(w, 403)
		return
	}
	token, ok := h.session(r, app)
	deleteSession(w)
	if !ok {
		status(w, 401)
		return
	}
	h.mu.Lock()
	delete(h.sessions, token)
	h.mu.Unlock()
	b, _ := json.Marshal(map[string]string{"revoke": token})
	resp, e := h.request(r.Context(), "POST", "/internal/apps/exchange", key, b)
	if e != nil {
		status(w, 502)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status(w, 502)
		return
	}
	status(w, 204)
}
func (h *Host) proxy(w http.ResponseWriter, r *http.Request, app string) {
	if !sameOrigin(r) {
		status(w, 403)
		return
	}
	token, ok := h.session(r, app)
	if !ok {
		status(w, 401)
		return
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
	if e != nil {
		status(w, 400)
		return
	}
	if len(b) > 8<<20 {
		status(w, 413)
		return
	}
	out, e := http.NewRequestWithContext(r.Context(), "POST", h.c.GatewayURL+"/mcp", bytes.NewReader(b))
	if e != nil {
		status(w, 502)
		return
	}
	// An allowlist also strips Connection-nominated headers and every future X-Jnpt header.
	for _, k := range []string{"Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-Id"} {
		if v := r.Header.Values(k); len(v) > 0 {
			out.Header[k] = append([]string(nil), v...)
		}
	}
	out.Header.Set("Authorization", "Bearer "+token)
	resp, e := h.client.Do(out)
	if e != nil {
		status(w, 502)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 401 {
		deleteSession(w)
		h.mu.Lock()
		delete(h.sessions, token)
		h.mu.Unlock()
	}
	for _, k := range []string{"Content-Type", "Mcp-Session-Id", "Mcp-Protocol-Version"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	status(w, resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// RelayHandler is exclusively :8081, never exposed by the browser proxy.
func (h *Host) RelayHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/tls-ask" {
			status(w, 404)
			return
		}
		domain := r.URL.Query().Get("domain")
		if h.app(domain) == "" {
			status(w, 403)
			return
		}
		key, e := h.key()
		if e != nil {
			status(w, 503)
			return
		}
		resp, e := h.request(r.Context(), "GET", "/internal/apps/tls-ask?domain="+url.QueryEscape(domain), key, nil)
		if e != nil {
			status(w, 503)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 200 {
			status(w, 404)
			return
		}
		status(w, 200)
	})
}
