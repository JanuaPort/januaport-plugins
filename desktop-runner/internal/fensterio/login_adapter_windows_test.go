//go:build windows

package fensterio

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/anmeldung"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
)

// Ausschließlich Go-HTTP, kein Systembrowser, kein Windows-/GUI-Aufruf.
func TestLoginVomKnopfBisSessionAmAdapter(t *testing.T) {
	for _, failure := range []string{"", "kein_zugriff", "platz_gesperrt", "abgebrochen"} {
		t.Run(failure, func(t *testing.T) {
			var state, challenge string
			var port, sessions, opens int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("X-Jnpt-Desktop-Contract") != "5" || r.Header.Get("Authorization") != "Bearer "+deviceToken {
					t.Error("Geraetekanal")
				}
				switch r.URL.Path {
				case "/api/device/v1/apps/demo/login":
					var b struct {
						Challenge, State string
						Port             int
					}
					if json.NewDecoder(r.Body).Decode(&b) != nil {
						t.Error("JSON")
					}
					state, challenge, port = b.State, b.Challenge, b.Port
					fmt.Fprint(w, `{"authorize_url":"https://idp.example/authorize?a=b&state=c"}`)
				case "/api/device/v1/apps/demo/session":
					sessions++
					var b struct{ Code, Verifier string }
					if json.NewDecoder(r.Body).Decode(&b) != nil || b.Code != "synthetic-code" || !anmeldung.ValidVerifier(b.Verifier) {
						t.Error("Tausch")
					}
					// Der Verifier gehoert genau zur Herausforderung der Login-Anfrage.
					sum := sha256.Sum256([]byte(b.Verifier))
					if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
						t.Error("PKCE-Bindung")
					}
					fmt.Fprint(w, `{"app_token":"jnptpc_synthetic-person","expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp","anzeigename":"Gleich","person_gewechselt":true}`)
				default:
					t.Error("Route", r.URL.Path)
				}
			}))
			defer srv.Close()
			a, e := client.NewAPI(client.Trust{URL: srv.URL}, deviceToken, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			person, e := loginDevice(ctx, a, "demo", func(raw string) error {
				opens++
				if raw != "https://idp.example/authorize?a=b&state=c" || !anmeldung.ValidState(state) || !anmeldung.ValidChallenge(challenge) {
					t.Error("Browserstart")
				}
				suffix := "code=synthetic-code"
				if failure != "" {
					suffix = "fehler=" + failure
				}
				res, e := http.Get(fmt.Sprintf("http://127.0.0.1:%d/jnpt/callback?state=%s&%s", port, state, suffix))
				if e != nil {
					return e
				}
				defer res.Body.Close()
				if res.StatusCode != 200 {
					t.Error("Rueckruf")
				}
				return nil
			})
			if opens != 1 {
				t.Fatal("kein nativer Start")
			}
			if failure == "" {
				if e != nil || person.Session.IsZero() || sessions != 1 {
					t.Fatal("kein Tausch", e)
				}
			} else if e == nil || sessions != 0 || loginMessage(e) != anmeldung.Message(failure) {
				t.Fatal("Fehler hat Code eingeloest", e)
			}
		})
	}
}

func TestLoopbackFristUndUngueltigerBrowserstart(t *testing.T) {
	f, _ := anmeldung.New()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	l, e := newLoopback(ctx, f)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	select {
	case <-l.done:
	case <-time.After(time.Second):
		t.Fatal("Frist schliesst nicht")
	}
	for _, u := range []string{"http://idp.example/", "https://name@idp.example.org/", "file:///a", "https://idp.example/\x00"} {
		if e := OpenInBrowser(u); e == nil {
			t.Fatal("ungepruefter Shell-Start", strings.Split(u, "?")[0])
		}
	}
}
