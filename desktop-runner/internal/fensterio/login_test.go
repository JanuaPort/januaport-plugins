package fensterio

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

type loginView struct {
	events               []string
	quiet, clear         func(bool)
	failQuiet, failClear bool
	name, message        string
	signed               bool
}

func (v *loginView) Quiesce(done func(bool)) error {
	v.events = append(v.events, "quiet")
	v.quiet = done
	if v.failQuiet {
		return errors.New("private")
	}
	return nil
}
func (v *loginView) ClearBrowsingData(done func(bool)) error {
	v.events = append(v.events, "clear")
	v.clear = done
	if v.failClear {
		return errors.New("private")
	}
	return nil
}
func (v *loginView) Reload() error { v.events = append(v.events, "reload"); return nil }
func (v *loginView) Account(name, message string, busy, signed bool) {
	v.name = name
	v.message = message
	v.signed = signed
}
func (v *loginView) FailLogin() { v.events = append(v.events, "fail") }

func TestLoginStartmeldungBleibtNachPumpUndAbmelden(t *testing.T) {
	l := NewLogin(NewGateway(nil, "demo"), nil, nil, nil)
	defer l.Close()
	v := &loginView{}
	l.Pump(v)
	if v.message != "Bitte mit dem eigenen Konto anmelden." || v.name != "" || v.signed {
		t.Errorf("erster Pump: name=%q message=%q signed=%v", v.name, v.message, v.signed)
	}
	l.SignOut(v)
	v.quiet(true)
	v.clear(true)
	l.Pump(v)
	if v.message != "Abgemeldet." || v.name != "" || v.signed {
		t.Errorf("Pump nach Abmelden: name=%q message=%q signed=%v", v.name, v.message, v.signed)
	}
}

func personForTest(t *testing.T) client.PersonSession {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"app_token":"jnptpc_synthetic-person","expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp","anzeigename":"Gleich","person_gewechselt":true}`)
	}))
	t.Cleanup(srv.Close)
	a, e := client.NewAPI(client.Trust{URL: srv.URL}, deviceToken, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(a.Close)
	p, e := a.AppSession(context.Background(), "demo", "code", strings.Repeat("v", 43))
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestPersonenwechselVorFreigabeUndAbmeldenLoeschen(t *testing.T) {
	p := personForTest(t)
	for _, changed := range []bool{true, false} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			p.Changed = changed
			g := NewGateway(nil, "demo")
			wake := make(chan struct{}, 4)
			l := NewLogin(g, nil, func(context.Context) (client.PersonSession, error) { return p, nil }, func() { wake <- struct{}{} })
			defer l.Close()
			l.name = "Gleich" // zwei Personen dürfen denselben Namen tragen
			l.signed = true
			g.install(p.Session)
			v := &loginView{}
			l.Start()
			select {
			case <-wake:
			case <-time.After(time.Second):
				t.Fatal("blockiert")
			}
			l.Pump(v)
			if changed {
				if !reflect.DeepEqual(v.events, []string{"quiet"}) || !g.session.IsZero() {
					t.Fatal("vor Bereinigung freigegeben", v.events)
				}
				v.quiet(true)
				if !reflect.DeepEqual(v.events, []string{"quiet", "clear"}) {
					t.Fatal(v.events)
				}
				v.clear(true)
				if !reflect.DeepEqual(v.events, []string{"quiet", "clear", "reload"}) {
					t.Fatal(v.events)
				}
			} else if len(v.events) != 0 {
				t.Fatal("gleiche Person verliert Eingaben")
			}
			if g.session.IsZero() || v.name != "Gleich" {
				t.Fatal("nicht freigegeben")
			}
			v.events = nil
			l.SignOut(v)
			if !g.session.IsZero() {
				t.Fatal("Abmelden behält Token")
			}
			v.quiet(true)
			v.clear(true)
			if !reflect.DeepEqual(v.events, []string{"quiet", "clear", "reload"}) || v.name != "" {
				t.Fatal(v.events, v.name)
			}
		})
	}
}

func TestLoginBereinigungsfehlerSperrtUndSpaetesErgebnisVerworfen(t *testing.T) {
	p := personForTest(t)
	for _, phase := range []string{"quiet-start", "quiet-result", "clear-start", "clear-result"} {
		t.Run(phase, func(t *testing.T) {
			wake := make(chan struct{}, 1)
			g := NewGateway(nil, "demo")
			l := NewLogin(g, nil, func(context.Context) (client.PersonSession, error) { return p, nil }, func() { wake <- struct{}{} })
			defer l.Close()
			v := &loginView{failQuiet: phase == "quiet-start", failClear: phase == "clear-start"}
			l.Start()
			<-wake
			l.Pump(v)
			if phase != "quiet-start" {
				v.quiet(phase != "quiet-result")
			}
			if phase == "clear-result" {
				v.clear(false)
			}
			if !g.session.IsZero() || v.events[len(v.events)-1] != "fail" {
				t.Fatal("trotz Fehler freigegeben", v.events)
			}
		})
	}
	wake := make(chan struct{}, 2)
	release := make(chan struct{})
	g := NewGateway(nil, "demo")
	l := NewLogin(g, nil, func(context.Context) (client.PersonSession, error) { <-release; return p, nil }, func() { wake <- struct{}{} })
	l.Start()
	l.Close()
	close(release)
	<-wake
	l.Pump(&loginView{})
	if !g.session.IsZero() {
		t.Fatal("spätes Ergebnis")
	}
}

func TestGatewayPersonRequiredAmAdapter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		fmt.Fprint(w, `{"error":{"class":"person_required","message":"PRIVATE"}}`)
	}))
	defer srv.Close()
	a, e := client.NewAPI(client.Trust{URL: srv.URL}, deviceToken, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	g := NewGateway(a, "demo")
	if _, e := g.Werkzeuge(context.Background()); !errors.Is(e, fenster.ErrNichtAngemeldet) {
		t.Fatal(e)
	}
}
