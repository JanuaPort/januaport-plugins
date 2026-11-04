package fenster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testAppToken    = "jnptpc_ABCDEFGHIJKLMNOPQRSTUVWXYZ.ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	testDeviceToken = "jnpt_" + "0123456789abcdef" + "0123456789abcdef"
)

type fakeView struct {
	mu    sync.Mutex
	sent  [][]byte
	fails bool
}

func (v *fakeView) Senden(b []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sent = append(v.sent, append([]byte(nil), b...))
	if v.fails {
		return errors.New("view weg")
	}
	return nil
}
func (v *fakeView) all() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []string
	for _, b := range v.sent {
		out = append(out, string(b))
	}
	return strings.Join(out, "\n")
}
func (v *fakeView) count() int { v.mu.Lock(); defer v.mu.Unlock(); return len(v.sent) }

type fakeGateway struct {
	listResult json.RawMessage
	callResult json.RawMessage
	err        error
	block      bool
	lists      int
	calls      []string
	args       []string
}

func (g *fakeGateway) Werkzeuge(ctx context.Context) (json.RawMessage, error) {
	g.lists++
	if g.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return g.listResult, g.err
}
func (g *fakeGateway) Werkzeug(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	g.calls = append(g.calls, name)
	g.args = append(g.args, string(arguments))
	return g.callResult, g.err
}

type fakeLokal struct {
	status    Status
	err       error
	statusN   int
	anhaltenN int
}

func (l *fakeLokal) Status(context.Context) (Status, error) { l.statusN++; return l.status, l.err }
func (l *fakeLokal) Anhalten(context.Context) error         { l.anhaltenN++; return l.err }

var mainFrame = Source{MainFrame: true, URL: "https://app-demo-pc.jnpt.invalid/index.html"}

func setup(t *testing.T) (*Bruecke, *fakeView, *fakeGateway, *fakeLokal) {
	t.Helper()
	v, g, l := &fakeView{}, &fakeGateway{}, &fakeLokal{}
	b, err := NewBruecke("demo", g, l, v)
	if err != nil {
		t.Fatal(err)
	}
	return b, v, g, l
}

func reply(t *testing.T, v *fakeView) map[string]json.RawMessage {
	t.Helper()
	if v.count() != 1 {
		t.Fatalf("erwartet genau eine Antwort, erhalten %d: %s", v.count(), v.all())
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(v.sent[0], &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBrueckeStatus(t *testing.T) {
	b, v, g, l := setup(t)
	l.status = Status{Zustand: "pausiert", Bis: "2026-11-04T12:00:00Z", Netz: true}
	b.Empfangen(context.Background(), mainFrame, []byte(`{"id":"s1","befehl":"status","args":{}}`))
	m := reply(t, v)
	if string(m["id"]) != `"s1"` || string(m["ok"]) != "true" || string(m["ergebnis"]) != `{"zustand":"pausiert","bis":"2026-11-04T12:00:00Z","netz":true}` || len(m) != 3 {
		t.Fatal(v.all())
	}
	if l.statusN != 1 || l.anhaltenN != 0 || g.lists != 0 || len(g.calls) != 0 {
		t.Fatal("falsche Ziele aufgerufen")
	}
}

func TestBrueckeAnhalten(t *testing.T) {
	b, v, g, l := setup(t)
	b.Empfangen(context.Background(), mainFrame, []byte(`{"id":"h","befehl":"anhalten","args":{}}`))
	m := reply(t, v)
	if string(m["ok"]) != "true" || l.anhaltenN != 1 || l.statusN != 0 || g.lists != 0 {
		t.Fatal(v.all(), l)
	}
}

func TestBrueckeWerkzeugeUndWerkzeug(t *testing.T) {
	b, v, g, _ := setup(t)
	g.listResult = json.RawMessage(`{"tools":[{"name":"ping"}]}`)
	g.callResult = json.RawMessage(`{"content":[{"type":"text","text":"pong"}]}`)
	b.Empfangen(context.Background(), mainFrame, []byte(`{"id":"1","befehl":"werkzeuge","args":{}}`))
	b.Empfangen(context.Background(), mainFrame, []byte(`{"id":"2","befehl":"werkzeug","args":{"name":"ping","arguments":{"a":1}}}`))
	if v.count() != 2 || g.lists != 1 || len(g.calls) != 1 || g.calls[0] != "ping" || g.args[0] != `{"a":1}` {
		t.Fatal(v.all(), g)
	}
	if !strings.Contains(v.all(), `"ergebnis":{"tools":[{"name":"ping"}]}`) || !strings.Contains(v.all(), `"ergebnis":{"content":[{"type":"text","text":"pong"}]}`) {
		t.Fatal(v.all())
	}
}

func TestBrueckeUnerlaubteQuelleBleibtStumm(t *testing.T) {
	for name, src := range map[string]Source{
		"unterrahmen": {MainFrame: false, URL: mainFrame.URL},
		"fremdeSeite": {MainFrame: true, URL: "https://evil.example/"},
		"aboutBlank":  {MainFrame: true, URL: "about:blank"},
		"leer":        {},
	} {
		t.Run(name, func(t *testing.T) {
			b, v, g, l := setup(t)
			b.Empfangen(context.Background(), src, []byte(`{"id":"1","befehl":"anhalten","args":{}}`))
			if v.count() != 0 || l.anhaltenN != 0 || l.statusN != 0 || g.lists != 0 || len(g.calls) != 0 {
				t.Fatal("unerlaubte Quelle erhielt Antwort oder löste etwas aus")
			}
		})
	}
}

func TestBrueckeFesteFehlerBeiFalschemUmschlag(t *testing.T) {
	for _, tc := range []struct{ name, raw, id, code string }{
		{"unbekannterBefehl", `{"id":"9","befehl":"pause","args":{}}`, `"9"`, "befehl"},
		{"kaputt", `{"id":`, `null`, "umschlag"},
		{"fremdesFeld", `{"id":"9","befehl":"status","args":{},"x":1}`, `null`, "umschlag"},
		{"argsFalsch", `{"id":"9","befehl":"status","args":{"x":1}}`, `"9"`, "args"},
		{"zuGross", `{"id":"9","befehl":"werkzeug","args":{"name":"x","arguments":{"t":"` + strings.Repeat("a", MaxUmschlag) + `"}}}`, `null`, "zu_gross"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, v, g, l := setup(t)
			b.Empfangen(context.Background(), mainFrame, []byte(tc.raw))
			m := reply(t, v)
			if string(m["id"]) != tc.id || string(m["ok"]) != "false" || string(m["fehler"]) != `"`+tc.code+`"` || len(m) != 3 {
				t.Fatal(v.all())
			}
			if l.statusN+l.anhaltenN != 0 || g.lists != 0 || len(g.calls) != 0 {
				t.Fatal("trotz Fehler aufgerufen")
			}
		})
	}
}

func TestBrueckeFehlerAusGatewayNurAlsFesterCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"nichtAngemeldet", ErrNichtAngemeldet, "nicht_angemeldet"},
		{"abgelehnt", ErrAbgelehnt, "abgelehnt"},
		{"nichtErreichbar", ErrNichtErreichbar, "nicht_erreichbar"},
		{"zuGross", ErrZuGross, "zu_gross"},
		{"zuViele", ErrZuViele, "zu_viele"},
		{"fremderFehler", errors.New("boom"), "fehler"},
		{"umhuellt", fmt.Errorf("intern: %w", ErrAbgelehnt), "abgelehnt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, v, g, _ := setup(t)
			g.err = tc.err
			b.Empfangen(context.Background(), mainFrame, []byte(`{"id":"1","befehl":"werkzeuge","args":{}}`))
			m := reply(t, v)
			if string(m["fehler"]) != `"`+tc.code+`"` || len(m) != 3 {
				t.Fatal(v.all())
			}
		})
	}
}

// Plan 4c: Ein spiegelnder Kern (Fake-Gateway) steckt Träger in Fehlertexte und
// Ergebnisse. Nichts davon darf bei der Seite ankommen.
func TestBrueckeAntwortOhneTraeger(t *testing.T) {
	spiegel := fmt.Sprintf("401 Unauthorized: Authorization: Bearer %s / X-Jnpt-App-Token: %s", testDeviceToken, testAppToken)
	cases := []struct {
		name  string
		setup func(g *fakeGateway, l *fakeLokal)
		raw   string
		code  string
	}{
		{"rohFehler", func(g *fakeGateway, l *fakeLokal) { g.err = errors.New(spiegel) }, `{"id":"1","befehl":"werkzeuge","args":{}}`, "fehler"},
		{"rohFehlerWerkzeug", func(g *fakeGateway, l *fakeLokal) { g.err = errors.New(spiegel) }, `{"id":"1","befehl":"werkzeug","args":{"name":"x","arguments":{}}}`, "fehler"},
		{"festerCodeMitTraegerText", func(g *fakeGateway, l *fakeLokal) { g.err = fmt.Errorf("%w: %s", ErrNichtAngemeldet, spiegel) }, `{"id":"1","befehl":"werkzeuge","args":{}}`, "nicht_angemeldet"},
		{"lokalFehler", func(g *fakeGateway, l *fakeLokal) { l.err = errors.New(spiegel) }, `{"id":"1","befehl":"status","args":{}}`, "fehler"},
		{"ergebnisSpiegeltAppToken", func(g *fakeGateway, l *fakeLokal) {
			g.listResult = json.RawMessage(`{"echo":"` + testAppToken + `"}`)
		}, `{"id":"1","befehl":"werkzeuge","args":{}}`, "antwort_ungueltig"},
		{"ergebnisSpiegeltGeraetetoken", func(g *fakeGateway, l *fakeLokal) {
			g.callResult = json.RawMessage(`{"content":[{"type":"text","text":"Bearer ` + testDeviceToken + `"}]}`)
		}, `{"id":"1","befehl":"werkzeug","args":{"name":"x","arguments":{}}}`, "antwort_ungueltig"},
		{"ergebnisMitEscape", func(g *fakeGateway, l *fakeLokal) {
			g.callResult = json.RawMessage(`{"t":"jnptpc_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"}`)
		}, `{"id":"1","befehl":"werkzeug","args":{"name":"x","arguments":{}}}`, "antwort_ungueltig"},
		// SEC-R4 F3: Träger, die erst nach dem JSON-Unescaping sichtbar sind (kein Treffer im Rohtext).
		{"traegerNurEscaped", func(g *fakeGateway, l *fakeLokal) {
			g.callResult = json.RawMessage(`{"t":"\u006anptpc_` + strings.Repeat("Q", 30) + `"}`)
		}, `{"id":"1","befehl":"werkzeug","args":{"name":"x","arguments":{}}}`, "antwort_ungueltig"},
		{"traegerMusterNurEscaped", func(g *fakeGateway, l *fakeLokal) {
			g.listResult = json.RawMessage(`{"t":["a",{"b":"np" + "tx"}]}`)
			g.listResult = json.RawMessage(`{"t":["a",{"b":"jn\u0070trun_` + strings.Repeat("Q", 30) + `"}]}`)
		}, `{"id":"1","befehl":"werkzeuge","args":{}}`, "antwort_ungueltig"},
		{"traegerImSchluessel", func(g *fakeGateway, l *fakeLokal) {
			g.listResult = json.RawMessage(`{"jn` + `ptpc_` + strings.Repeat("Q", 30) + `":1}`)
		}, `{"id":"1","befehl":"werkzeuge","args":{}}`, "antwort_ungueltig"},
		{"traegerImSchluesselNurEscaped", func(g *fakeGateway, l *fakeLokal) {
			g.listResult = json.RawMessage(`{"a":{"\u006anptpc_` + strings.Repeat("Q", 30) + `":1}}`)
		}, `{"id":"1","befehl":"werkzeuge","args":{}}`, "antwort_ungueltig"},
		{"traegerTiefVerschachtelt", func(g *fakeGateway, l *fakeLokal) {
			g.listResult = json.RawMessage(`{"a":[[{"b":[{"c":"\u006anptapp_` + strings.Repeat("Q", 30) + `"}]}]]}`)
		}, `{"id":"1","befehl":"werkzeuge","args":{}}`, "antwort_ungueltig"},
		{"ergebnisKeinJSON", func(g *fakeGateway, l *fakeLokal) { g.listResult = json.RawMessage(`<html>`) }, `{"id":"1","befehl":"werkzeuge","args":{}}`, "antwort_ungueltig"},
		{"ergebnisLeer", func(g *fakeGateway, l *fakeLokal) { g.listResult = nil }, `{"id":"1","befehl":"werkzeuge","args":{}}`, "antwort_ungueltig"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, v, g, l := setup(t)
			tc.setup(g, l)
			b.Empfangen(context.Background(), mainFrame, []byte(tc.raw))
			m := reply(t, v)
			out := v.all()
			for _, forbidden := range []string{testAppToken, testDeviceToken, "jnptpc_", "jnpt_0123", "Bearer", "X-Jnpt", "Unauthorized", "echo"} {
				if strings.Contains(out, forbidden) {
					t.Fatalf("Antwort enthält %q: %s", forbidden, out)
				}
			}
			if string(m["ok"]) != "false" || string(m["fehler"]) != `"`+tc.code+`"` {
				t.Fatal(out)
			}
		})
	}
}

func TestTraegerErkennung(t *testing.T) {
	for raw, want := range map[string]bool{
		testAppToken:    true,
		testDeviceToken: true,
		"jnptapp_ABCDEFGHIJKLMNOPQRSTUVWXYZ.ABCD": true,
		"xx jnptrun_aaaaaaaaaaaaaaaaaaaaaaaaaaaa": true,
		"jnpt-bedienfeld.exe":                     false,
		"jnpt_kurz":                               false,
		"kein Träger, nur Text":                   false,
		"jnptpc_":                                 true,
	} {
		if got := EnthaeltTraeger([]byte(raw)); got != want {
			t.Errorf("EnthaeltTraeger(%q) = %v", raw, got)
		}
	}
}

func TestBrueckeZeitgrenze(t *testing.T) {
	b, v, g, _ := setup(t)
	g.block = true
	b.Frist = 20 * time.Millisecond
	b.Empfangen(context.Background(), mainFrame, []byte(`{"id":"1","befehl":"werkzeuge","args":{}}`))
	if m := reply(t, v); string(m["fehler"]) != `"nicht_erreichbar"` {
		t.Fatal(v.all())
	}
}

func TestBrueckeViewFehlerIstHarmlos(t *testing.T) {
	b, v, _, _ := setup(t)
	v.fails = true
	b.Empfangen(context.Background(), mainFrame, []byte(`{"id":"1","befehl":"status","args":{}}`))
	if v.count() != 1 {
		t.Fatal("Antwort nicht versucht")
	}
}

func TestNewBrueckeAppnameUndNil(t *testing.T) {
	g, l, v := &fakeGateway{}, &fakeLokal{}, &fakeView{}
	for _, app := range []string{"", "Demo", "a-b"} {
		if _, err := NewBruecke(app, g, l, v); err == nil {
			t.Errorf("Appname %q angenommen", app)
		}
	}
	if _, err := NewBruecke("demo", nil, l, v); err == nil {
		t.Error("ohne Gateway")
	}
	if _, err := NewBruecke("demo", g, nil, v); err == nil {
		t.Error("ohne Lokal")
	}
	if _, err := NewBruecke("demo", g, l, nil); err == nil {
		t.Error("ohne View")
	}
}

// SEC-R4 F3: Die Erkennung arbeitet auf dem entschlüsselten JSON, nicht nur auf dem Rohtext.
func TestTraegerErkennungNachUnescaping(t *testing.T) {
	q := strings.Repeat("Q", 30)
	for raw, want := range map[string]bool{
		`"\u006anptpc_` + q + `"`:                true,
		`"\u006e` + `ptpc_` + q + `"`:            false, // "nptpc_…": kein j, kein Träger
		`{"\u006anptpc_` + q + `":1}`:            true,
		`{"a":{"\u006anptapp_` + q + `":[1]}}`:   true,
		`["x",{"y":["\u006anptrun_` + q + `"]}]`: true,
		`{"a":"\u006anptpc_"}`:                   true,
		`{"a":"\u006anpt_kurz"}`:                 false,
		`{"a":"\u006anpt-bedienfeld.exe"}`:       false,
		`{"a":1,"b":[true,null,"text"]}`:         false,
		`"\u00e4\u00f6\u00fc"`:                   false,
	} {
		if got := EnthaeltTraeger([]byte(raw)); got != want {
			t.Errorf("EnthaeltTraeger(%s) = %v", raw, got)
		}
	}
}
