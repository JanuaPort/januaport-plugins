package fensterio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/anmeldung"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
)

type lateGateway struct{ started, release chan struct{} }

func (g lateGateway) Werkzeuge(context.Context) (json.RawMessage, error) {
	close(g.started)
	<-g.release
	return json.RawMessage(`{"private":"alte-person"}`), nil
}
func (g lateGateway) Werkzeug(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return g.Werkzeuge(ctx)
}
func TestPersonenwechselVerwirftAlteBrueckenantwort(t *testing.T) {
	g := lateGateway{make(chan struct{}), make(chan struct{})}
	h, e := NewHost(HostConfig{App: "demo", Gateway: g, Lokal: &countLokal{}})
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	h.Receive(page, msg("old", "werkzeuge", "{}"))
	<-g.started
	h.Suspend()
	h.Receive(page, msg("blocked", "status", "{}"))
	h.Resume()
	close(g.release)
	h.Wait()
	if out := h.Drain(); len(out) != 0 {
		t.Fatal("alte Person sichtbar", string(out[0]))
	}
	h.Receive(page, msg("new", "status", "{}"))
	h.Wait()
	if len(h.Drain()) != 1 {
		t.Fatal("neue Seite bleibt gesperrt")
	}
}

func TestAbmeldenWartetNichtAufGatewayUndTokenKommtNichtZurueck(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"app_token":"jnptpc_synthetic-late","expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp"}`)
	}))
	defer srv.Close()
	a, e := client.NewAPI(client.Trust{URL: srv.URL}, deviceToken, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	g := NewGateway(a, "demo")
	done := make(chan error, 1)
	go func() { _, e := g.Werkzeuge(context.Background()); done <- e }()
	<-entered
	within(t, time.Second, "Abmelden", g.Forget)
	close(release)
	if e := <-done; e == nil {
		t.Fatal("alter Token wieder eingesetzt")
	}
	if !g.session.IsZero() {
		t.Fatal("Token behalten")
	}
}

func TestAnmeldungOhneTraegerInBruecke(t *testing.T) {
	for _, class := range []string{"kein_zugriff", "platz_gesperrt", "unbekannt"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			fmt.Fprintf(w, `{"error":{"class":%q,"message":"Kontoname PRIVATE jnptpc_synthetic"}}`, class)
		}))
		a, e := client.NewAPI(client.Trust{URL: srv.URL}, deviceToken, nil)
		if e != nil {
			t.Fatal(e)
		}
		_, e = a.AppSession(context.Background(), "demo", "code", strings.Repeat("v", 43))
		if e == nil {
			t.Fatal("Ablehnung fehlt")
		}
		if got := loginMessage(e); strings.Contains(got, "PRIVATE") || strings.Contains(got, "jnptpc_") || got != anmeldung.Message(class) {
			t.Fatal(got)
		}
		a.Close()
		srv.Close()
	}
	for _, class := range []string{"kein_zugriff", "platz_gesperrt", "abgebrochen"} {
		if loginMessage(loginError(class)) != anmeldung.Message(class) {
			t.Fatal(class)
		}
	}
	if loginMessage(errors.New("PRIVATE")) != anmeldung.Message("") {
		t.Fatal("freier Fehlertext")
	}
}

func TestAnmeldenHaengtNichtUndStartetNurEinmal(t *testing.T) {
	entered, release := make(chan struct{}, 2), make(chan struct{})
	wake := make(chan struct{}, 2)
	b := newHostBed(t, nil)
	l := NewLogin(NewGateway(nil, "demo"), b.h, func(ctx context.Context) (client.PersonSession, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return client.PersonSession{}, ctx.Err()
	}, func() { wake <- struct{}{} })
	within(t, time.Second, "Anmelden", l.Start)
	<-entered
	b.h.Receive(page, msg("halt-login", "anhalten", "{}"))
	waitFor(t, "Halt waehrend Login", func() bool { return b.l.anhaltenN.Load() == 1 })
	l.Start()
	select {
	case <-entered:
		t.Fatal("zweiter Login")
	default:
	}
	l.Close()
	<-wake
}
