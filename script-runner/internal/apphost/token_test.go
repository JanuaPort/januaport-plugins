package apphost

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAppTokenFormAtHTTP(t *testing.T) {
	prefix := "jnpt" + "app_"
	left, right := strings.Repeat("A", 26), strings.Repeat("B", 26)
	for _, tc := range []struct {
		name, token string
		valid       bool
	}{
		{"core", credential("app"), true},
		{"base32", prefix + "ABCDEFGHIJKLMNOPQRSTUVWXYZ.234567ABCDEFGHIJKLMNOPQRST", true},
		{"allowed_alphabet", prefix + strings.Repeat("a0_-", 6) + "z9." + strings.Repeat("Z9_-", 6) + "a0", true},
		{"no_dot", prefix + left + right, false},
		{"replaced_dot", prefix + left + "-" + right, false},
		{"two_dots", prefix + left + "." + right + ".C", false},
		{"empty_left", prefix + "." + right, false},
		{"empty_right", prefix + left + ".", false},
		{"short_left", prefix + left[:25] + "." + right, false},
		{"short_right", prefix + left + "." + right[:25], false},
		{"long_left", prefix + left + "A." + right, false},
		{"long_right", prefix + left + "." + right + "B", false},
		{"oversized", prefix + strings.Repeat("A", 257) + "." + right, false},
		{"foreign_left", prefix + left[:25] + "+." + right, false},
		{"foreign_right", prefix + left + "." + right[:25] + "/", false},
		{"missing_prefix", left + "." + right, false},
		{"wrong_prefix", "jnpt" + "apphost_" + left + "." + right, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			f.mu.Lock()
			f.token = tc.token
			f.mu.Unlock()
			f.h.mu.Lock()
			clear(f.h.sessions)
			f.h.mu.Unlock()
			login := f.login()
			r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=form", http.Header{"Cookie": {login.String()}}, "")
			want := 401
			if tc.valid {
				want = 200
			}
			t.Run("callback", func(t *testing.T) {
				requireStatus(t, r, want)
				assertDeleted(t, r, "__Host-jnpt_login")
				f.h.mu.Lock()
				_, stored := f.h.sessions[tc.token]
				f.h.mu.Unlock()
				if stored != tc.valid {
					t.Fatal("callback session registration", stored)
				}
				if !tc.valid {
					for _, c := range r.Cookies() {
						if c.Name == "__Host-jnpt_app" && c.MaxAge >= 0 {
							t.Fatal("invalid token set as session cookie")
						}
					}
				}
			})
			// Seed even malformed values: session() must validate form itself,
			// rather than passing solely because a RAM binding exists.
			f.h.mu.Lock()
			f.h.sessions[tc.token] = binding{"a", f.now.Add(time.Minute)}
			f.h.mu.Unlock()
			h := proxyHeaders()
			h.Set("Cookie", "__Host-jnpt_app="+tc.token)
			t.Run("session", func(t *testing.T) {
				requireStatus(t, f.request("GET", "a.apps.example.test", "/", h, ""), want)
			})
			t.Run("mcp", func(t *testing.T) {
				requireStatus(t, f.request("POST", "a.apps.example.test", "/mcp", h, `{}`), want)
			})
			t.Run("logout", func(t *testing.T) {
				logoutStatus := 401
				if tc.valid {
					logoutStatus = 204
				}
				requireStatus(t, f.request("POST", "a.apps.example.test", "/_jnpt/logout", h, ""), logoutStatus)
			})
			f.mu.Lock()
			defer f.mu.Unlock()
			if tc.valid {
				if f.proxied.Get("Authorization") != "Bearer "+tc.token {
					t.Fatal("token changed on MCP forwarding")
				}
				if len(f.exchanges) != 2 || f.exchanges[1]["revoke"] != tc.token {
					t.Fatal("token changed on logout")
				}
			} else if f.proxied != nil || f.packages != 0 || len(f.exchanges) != 1 {
				t.Fatal("invalid session reached gateway")
			}
		})
	}
}

func TestAppTokenCallbackCookieSessionAndMCPAtHTTP(t *testing.T) {
	f := setup(t)
	login := f.login()
	r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=roundtrip", http.Header{"Cookie": {login.String()}}, "")
	requireStatus(t, r, 200)
	var appCookie *http.Cookie
	for _, c := range r.Cookies() {
		if c.Name == "__Host-jnpt_app" {
			appCookie = c
		}
	}
	if appCookie == nil || appCookie.Value != credential("app") {
		t.Fatal("SetCookie changed the dotted token")
	}
	if !strings.Contains(r.Header.Get("Set-Cookie"), "__Host-jnpt_login=") {
		t.Fatal("login cookie deletion missing")
	}
	wantCookie := "__Host-jnpt_app=" + credential("app") + "; Path=/; Max-Age=47; HttpOnly; Secure; SameSite=Strict"
	if appCookie.String() != wantCookie {
		t.Fatal("session cookie wire value or attributes changed")
	}
	found := false
	for _, value := range r.Header.Values("Set-Cookie") {
		found = found || value == wantCookie
	}
	if !found {
		t.Fatal("SetCookie did not preserve the exact wire value")
	}
	// Build the next browser request from the received cookie, not the fixture.
	next, err := http.NewRequest("GET", f.server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	next.AddCookie(appCookie)
	h := next.Header.Clone()
	requireStatus(t, f.request("GET", "a.apps.example.test", "/", h, ""), 200)
	h.Set("Origin", "https://a.apps.example.test")
	h.Set("X-Jnpt-App", "1")
	requireStatus(t, f.request("POST", "a.apps.example.test", "/mcp", h, `{}`), 200)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.proxied.Get("Authorization") != "Bearer "+appCookie.Value {
		t.Fatal("MCP bearer differs from callback cookie")
	}
}

func TestAppTokenDotDoesNotRelaxOtherFieldsAtHTTP(t *testing.T) {
	f := setup(t)
	// Same length as a valid opaque value; only the dot makes it invalid.
	dotted := strings.Repeat("A", 26) + "." + strings.Repeat("B", 26)
	r := f.request("GET", "a.apps.example.test", "/_jnpt/callback?code=dotted-verifier", http.Header{"Cookie": {"__Host-jnpt_login=" + dotted}}, "")
	requireStatus(t, r, 400)
	assertDeleted(t, r, "__Host-jnpt_login")
	if len(f.exchanges) != 0 {
		t.Fatal("dotted login verifier reached exchange")
	}
	if err := os.WriteFile(f.keyPath, []byte("jnpt"+"apphost_"+dotted), 0600); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, f.get("/"), 503)
	if _, err := New(f.h.c); err == nil {
		t.Fatal("dotted apphost key accepted at startup")
	}
}
