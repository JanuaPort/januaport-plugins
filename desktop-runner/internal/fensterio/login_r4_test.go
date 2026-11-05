package fensterio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/anmeldung"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

func pumpLoginResult(t *testing.T, l *Login, v *loginView, wake <-chan struct{}) {
	t.Helper()
	l.Start()
	select {
	case <-wake:
	case <-time.After(3 * time.Second):
		t.Fatal("Login blockiert")
	}
	l.Pump(v)
}

// Beide Fälle benutzen den echten HTTP-Adapter, Gateway, Host und Login.
// Kein direkter Suspend-Aufruf: Die Grenze muss durch switchPerson entstehen.
func loginDiscardsOldReply(t *testing.T, queued bool) {
	t.Helper()
	p := personForTest(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	k := &kern{mcpBody: func(_ string, _ json.RawMessage, _ string) string {
		close(entered)
		<-release
		return `{"private":"alte-person"}`
	}}
	g := newGateway(t, k)
	g.install(p.Session)
	h, err := NewHost(HostConfig{App: "demo", Gateway: g, Lokal: &countLokal{}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	wake := make(chan struct{}, 1)
	l := NewLogin(g, h, func(context.Context) (client.PersonSession, error) { return p, nil }, func() { wake <- struct{}{} })
	defer l.Close()
	l.signed, l.name = true, p.Name
	h.Receive(page, msg("old", "werkzeuge", "{}"))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP nicht gestartet")
	}
	if queued {
		close(release)
		h.Wait()
		h.mu.Lock()
		n := len(h.out)
		h.mu.Unlock()
		if n != 1 {
			t.Fatal("Kontrolle: alte Antwort fehlt vor Wechsel")
		}
	}
	v := &loginView{}
	pumpLoginResult(t, l, v, wake)
	if v.quiet == nil {
		t.Fatal("kein Wechsel")
	}
	// Auch zwischen Suspend und Freigabe darf die UI keine alte Antwort abholen.
	if out := h.Drain(); len(out) != 0 {
		t.Fatal("alte Warteschlange beim Wechsel ausgeliefert")
	}
	v.quiet(true)
	v.clear(true)
	if !queued {
		close(release)
	}
	h.Wait()
	if out := h.Drain(); len(out) != 0 {
		t.Fatal("alte Antwort nach Login ausgeliefert", string(out[0]))
	}
	h.Receive(page, msg("new", "status", "{}"))
	h.Wait()
	if len(h.Drain()) != 1 {
		t.Fatal("neue Seite bleibt gesperrt")
	}
}

func TestLoginVerwirftLaufendeAntwort(t *testing.T) { loginDiscardsOldReply(t, false) }
func TestLoginVerwirftWartendeAntwort(t *testing.T) { loginDiscardsOldReply(t, true) }

func TestLoginSitzungsablaufEntferntKontoAmAdapter(t *testing.T) {
	for _, tc := range []struct {
		status int
		class  string
	}{{401, "unauthorized"}, {401, "revoked"}, {503, "unavailable"}} {
		t.Run(tc.class, func(t *testing.T) {
			p := personForTest(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/device/v1/mcp" {
					t.Error("unerwartete Route", r.URL.Path)
				}
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if tc.class == "revoked" {
					fmt.Fprint(w, `{"error":{"class":"revoked"}}`)
				} else {
					fmt.Fprintf(w, `{"error":{"class":%q,"message":"Feste Testmeldung"}}`, tc.class)
				}
			}))
			defer srv.Close()
			api, err := client.NewAPI(client.Trust{URL: srv.URL}, deviceToken, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			g := NewGateway(api, "demo")
			wake := make(chan struct{}, 1)
			l := NewLogin(g, nil, func(context.Context) (client.PersonSession, error) { return p, nil }, func() { wake <- struct{}{} })
			defer l.Close()
			v := &loginView{}
			pumpLoginResult(t, l, v, wake)
			v.quiet(true)
			v.clear(true)
			if !v.signed || v.name == "" {
				t.Fatal("Kontrolle: keine Anmeldung")
			}
			_, err = g.Werkzeuge(context.Background())
			l.Pump(v)
			if tc.status == 401 {
				if !errors.Is(err, fenster.ErrNichtAngemeldet) {
					t.Fatal(err)
				}
				if v.signed || v.name != "" || v.message == anmeldung.Message("angemeldet") {
					t.Fatal("abgelaufenes Konto bleibt sichtbar")
				}
			} else if !v.signed || v.name != p.Name || v.message != anmeldung.Message("angemeldet") {
				t.Fatal("Netzfehler meldet Person ab")
			}
		})
	}
}

func TestLoginGleichePersonOhneLokaleSitzungLaedtNeu(t *testing.T) {
	for _, signed := range []bool{false, true} {
		p := personForTest(t)
		p.Changed = false
		g := NewGateway(nil, "demo")
		wake := make(chan struct{}, 1)
		l := NewLogin(g, nil, func(context.Context) (client.PersonSession, error) { return p, nil }, func() { wake <- struct{}{} })
		defer l.Close()
		if signed {
			l.signed, l.name = true, p.Name
			g.install(p.Session)
		}
		v := &loginView{}
		pumpLoginResult(t, l, v, wake)
		if v.quiet != nil {
			v.quiet(true)
			v.clear(true)
		}
		reloaded := false
		for _, event := range v.events {
			reloaded = reloaded || event == "reload"
		}
		if reloaded == signed {
			t.Fatalf("signed=%v reload=%v", signed, reloaded)
		}
		if !v.signed || v.name != p.Name {
			t.Fatal("Konto fehlt")
		}
		// Wiederanmeldung nach explizitem Abmelden trifft denselben Kern-Grant.
		l.SignOut(v)
		v.quiet(true)
		v.clear(true)
		v = &loginView{}
		pumpLoginResult(t, l, v, wake)
		if v.quiet != nil {
			v.quiet(true)
			v.clear(true)
		}
		reloaded = false
		for _, event := range v.events {
			reloaded = reloaded || event == "reload"
		}
		if !reloaded || !v.signed {
			t.Fatal("gleiche Person nach Abmelden nicht neu geladen")
		}
	}
}
