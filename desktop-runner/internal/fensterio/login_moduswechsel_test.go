package fensterio

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

// #1537 Punkt 4: Wechselt eine offene App im laufenden Fenster von Modus b nach a, vergisst das Fenster die
// persönliche Sitzung wie beim Personenwechsel (Gateway sperren, altes Dokument beenden, Browserdaten löschen, neu
// laden) und nimmt danach den Gerätezweig (personal=false) statt dauerhaft ErrNichtAngemeldet zu liefern.

func persoenlichesLogin(t *testing.T) (*Login, *Gateway, *fakeAppAPI) {
	t.Helper()
	api := &fakeAppAPI{}
	g := NewGateway(api, "demo")
	g.install(personForTest(t).Session)
	l := NewLogin(g, nil, nil, nil)
	l.signed, l.name = true, "Gleich"
	t.Cleanup(l.Close)
	return l, g, api
}

func TestModuswechselBNachAVergisstDiePersonUndNimmtDenGeraetezweig(t *testing.T) {
	l, g, api := persoenlichesLogin(t)
	v := &loginView{}
	l.ToDeviceMode(v)
	// Sofort gesperrt: Die persönliche Sitzung antwortet nicht mehr, auch nicht während das alte Dokument endet.
	if _, err := g.Werkzeuge(context.Background()); !errors.Is(err, fenster.ErrNichtAngemeldet) || api.rpcs != 0 {
		t.Fatalf("Gateway nicht gesperrt: %v, %d RPC", err, api.rpcs)
	}
	v.quiet(true)
	v.clear(true)
	if want := []string{"quiet", "clear", "reload"}; !reflect.DeepEqual(v.events, want) {
		t.Fatalf("Ablauf %v, erwartet %v", v.events, want)
	}
	if l.signed || l.name != "" {
		t.Errorf("Login zeigt noch die Person: signed=%v name=%q", l.signed, l.name)
	}
	if g.personalExpired() {
		t.Fatal("Gateway bleibt im persönlichen Zweig: dauerhaft ErrNichtAngemeldet")
	}
	if _, err := g.Werkzeuge(context.Background()); err != nil || api.tokens != 1 || api.rpcs != 1 {
		t.Fatalf("Gerätezweig: %v, %d Tokenabrufe, %d RPC (erwartet 1, 1)", err, api.tokens, api.rpcs)
	}
}

// Jeder Fehler im Übergang schließt das Fenster; das Gateway bleibt gesperrt, nie halb offen.
func TestModuswechselScheitertBleibtGesperrt(t *testing.T) {
	for name, tc := range map[string]struct {
		view   *loginView
		finish func(*loginView)
	}{
		"Quiesce": {&loginView{failQuiet: true}, func(*loginView) {}},
		"Frist":   {&loginView{}, func(v *loginView) { v.quiet(false) }},
		"Clear":   {&loginView{failClear: true}, func(v *loginView) { v.quiet(true) }},
		"Clear-Antwort": {&loginView{}, func(v *loginView) {
			v.quiet(true)
			v.clear(false)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			l, g, api := persoenlichesLogin(t)
			l.ToDeviceMode(tc.view)
			tc.finish(tc.view)
			if got := tc.view.events[len(tc.view.events)-1]; got != "fail" {
				t.Fatalf("Ablauf %v, erwartet Abbruch mit fail", tc.view.events)
			}
			if _, err := g.Werkzeuge(context.Background()); !errors.Is(err, fenster.ErrNichtAngemeldet) || api.rpcs != 0 {
				t.Fatalf("Gateway nach Fehler offen: %v, %d RPC", err, api.rpcs)
			}
		})
	}
}

// Ein laufender Anmeldeversuch (Browser offen) darf nach dem Wechsel keine Person mehr einsetzen.
func TestModuswechselVerwirftLaufendeAnmeldung(t *testing.T) {
	api := &fakeAppAPI{}
	g := NewGateway(api, "demo")
	release := make(chan struct{})
	done := make(chan struct{})
	l := NewLogin(g, nil, func(ctx context.Context) (client.PersonSession, error) {
		defer close(done)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return personForTest(t), nil
	}, nil)
	defer l.Close()
	l.Start()
	v := &loginView{}
	l.ToDeviceMode(v)
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Anmeldelauf endet nicht")
	}
	v.quiet(true)
	v.clear(true)
	l.Pump(v)
	if l.signed || g.personalExpired() {
		t.Fatalf("späte Anmeldung wirkt: signed=%v", l.signed)
	}
	if _, err := g.Werkzeuge(context.Background()); err != nil || api.tokens != 1 {
		t.Fatalf("Gerätezweig: %v, %d Tokenabrufe", err, api.tokens)
	}
}

func TestModuswechselNachCloseTutNichts(t *testing.T) {
	l, _, _ := persoenlichesLogin(t)
	l.Close()
	v := &loginView{}
	l.ToDeviceMode(v)
	if len(v.events) != 0 {
		t.Fatalf("geschlossenes Login arbeitet: %v", v.events)
	}
}
