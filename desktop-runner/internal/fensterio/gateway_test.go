package fensterio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

const deviceToken = "jnpt_" + "0123456789abcdef" + "0123456789abcdef"

// kern ist ein Fake-Gateway auf echtem HTTP. Es spiegelt auf Wunsch die Träger.
type kern struct {
	mu          sync.Mutex
	tokenFetch  int
	mcpCalls    int
	issued      []string
	mcpStatus   []int // pro MCP-Aufruf; danach 200
	tokenStatus []int
	mcpBody     func(method string, params json.RawMessage, token string) string
	headers     []http.Header
	bodies      []string
}

func (k *kern) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k.mu.Lock()
		defer k.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		k.headers = append(k.headers, r.Header.Clone())
		raw, _ := io.ReadAll(r.Body)
		k.bodies = append(k.bodies, string(raw))
		fail := func(status int, class, msg string) {
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"error":{"class":%q,"message":%q}}`, class, msg)
		}
		switch r.URL.Path {
		case "/api/device/v1/apps/demo/token":
			k.tokenFetch++
			if len(k.tokenStatus) > 0 {
				s := k.tokenStatus[0]
				k.tokenStatus = k.tokenStatus[1:]
				if s == 401 {
					fail(401, "unauthorized", "Die Anmeldung ist fehlgeschlagen.")
					return
				}
				if s == 429 {
					fail(429, "rate_limited", "Es sind zu viele Anfragen eingegangen.")
					return
				}
			}
			tok := fmt.Sprintf("jnptpc_ID%02dABCDEFGHIJKLMNOPQRSTU.SECRET%02dABCDEFGHIJKLMNOPQRS", k.tokenFetch, k.tokenFetch)
			k.issued = append(k.issued, tok)
			_, _ = fmt.Fprintf(w, `{"app_token":%q,"expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp"}`, tok)
		case "/api/device/v1/mcp":
			k.mcpCalls++
			if len(k.mcpStatus) > 0 {
				s := k.mcpStatus[0]
				k.mcpStatus = k.mcpStatus[1:]
				switch s {
				case 401:
					fail(401, "unauthorized", "Die Anmeldung ist fehlgeschlagen.")
					return
				case 429:
					fail(429, "rate_limited", "Es sind zu viele Anfragen eingegangen.")
					return
				case 503:
					fail(503, "unavailable", "Der Dienst ist vorübergehend nicht verfügbar.")
					return
				case 413:
					fail(413, "too_large", "Die Anfrage überschreitet die zulässige Größe.")
					return
				}
			}
			var req struct {
				ID     int64           `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				t.Error("kein JSON-RPC", string(raw))
			}
			body := `{"tools":[]}`
			if k.mcpBody != nil {
				body = k.mcpBody(req.Method, req.Params, r.Header.Get("X-Jnpt-App-Token"))
			}
			if strings.HasPrefix(body, "ERROR:") {
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32602,"message":%q}}`, req.ID, strings.TrimPrefix(body, "ERROR:"))
				return
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, body)
		default:
			fail(404, "not_found", "Der Auftrag oder das Paket wurde nicht gefunden.")
		}
	})
}

func newGateway(t *testing.T, k *kern) *Gateway {
	t.Helper()
	srv := httptest.NewServer(k.handler(t))
	t.Cleanup(srv.Close)
	api, err := client.NewAPI(client.Trust{URL: srv.URL}, deviceToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	return NewGateway(api, "demo")
}

func TestGatewayHolTokenEinmalUndSendetNurZweiTraeger(t *testing.T) {
	k := &kern{mcpBody: func(method string, params json.RawMessage, token string) string {
		if method == "tools/call" {
			return `{"content":[{"type":"text","text":"ok"}]}`
		}
		return `{"tools":[{"name":"ping"}]}`
	}}
	g := newGateway(t, k)
	ctx := context.Background()
	list, err := g.Werkzeuge(ctx)
	if err != nil || string(list) != `{"tools":[{"name":"ping"}]}` {
		t.Fatal(string(list), err)
	}
	res, err := g.Werkzeug(ctx, "ping", json.RawMessage(`{"a":1}`))
	if err != nil || string(res) != `{"content":[{"type":"text","text":"ok"}]}` {
		t.Fatal(string(res), err)
	}
	if k.tokenFetch != 1 || k.mcpCalls != 2 {
		t.Fatalf("Token nicht wiederverwendet: %d Abrufe, %d MCP-Aufrufe", k.tokenFetch, k.mcpCalls)
	}
	for i, h := range k.headers {
		if h.Get("X-Jnpt-Desktop-Contract") != "5" || h.Get("Authorization") != "Bearer "+deviceToken {
			t.Errorf("Anfrage %d: Vertrag/Authorization %v", i, h)
		}
		if len(h.Values("X-Jnpt-Run-Token")) != 0 {
			t.Errorf("Anfrage %d: Lauf-Token gesendet", i)
		}
		if i > 0 && (len(h.Values("X-Jnpt-App-Token")) != 1 || h.Get("X-Jnpt-App-Token") != k.issued[0]) {
			t.Errorf("Anfrage %d: App-Token %v", i, h.Values("X-Jnpt-App-Token"))
		}
		if i == 0 && len(h.Values("X-Jnpt-App-Token")) != 0 {
			t.Error("Token-Ausstellung darf kein App-Token tragen")
		}
	}
	// Die RPC-Körper: tools/list ohne Parameter, tools/call mit name und arguments.
	var call struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(k.bodies[2]), &call); err != nil || call.JSONRPC != "2.0" || call.Method != "tools/call" || call.Params.Name != "ping" || string(call.Params.Arguments) != `{"a":1}` {
		t.Fatal(k.bodies[2])
	}
	if !strings.Contains(k.bodies[1], `"method":"tools/list"`) {
		t.Fatal(k.bodies[1])
	}
	if k.bodies[0] != "" {
		t.Fatal("Token-Ausstellung braucht einen leeren Körper", k.bodies[0])
	}
}

func TestGatewayHoltBei401NeuUndWiederholtEinmal(t *testing.T) {
	k := &kern{mcpStatus: []int{401}}
	g := newGateway(t, k)
	if _, err := g.Werkzeuge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if k.tokenFetch != 2 || k.mcpCalls != 2 {
		t.Fatalf("%d Token, %d MCP", k.tokenFetch, k.mcpCalls)
	}
	if k.headers[3].Get("X-Jnpt-App-Token") != k.issued[1] || k.issued[0] == k.issued[1] {
		t.Fatal("Wiederholung nicht mit neuem Token")
	}
	// Das neue Token bleibt im RAM und wird weiterverwendet.
	if _, err := g.Werkzeuge(context.Background()); err != nil || k.tokenFetch != 2 {
		t.Fatal("Token nicht wiederverwendet", err, k.tokenFetch)
	}
}

func TestGatewayZweite401IstNichtAngemeldet(t *testing.T) {
	k := &kern{mcpStatus: []int{401, 401, 401, 401}}
	g := newGateway(t, k)
	_, err := g.Werkzeug(context.Background(), "ping", json.RawMessage(`{}`))
	if !errors.Is(err, fenster.ErrNichtAngemeldet) {
		t.Fatal(err)
	}
	if k.mcpCalls != 2 || k.tokenFetch != 2 {
		t.Fatalf("keine Schleife erlaubt: %d MCP, %d Token", k.mcpCalls, k.tokenFetch)
	}
}

func TestGatewayFehlerabbildung(t *testing.T) {
	for _, tc := range []struct {
		name string
		k    *kern
		want error
	}{
		{"tokenAusstellung401", &kern{tokenStatus: []int{401}}, fenster.ErrNichtAngemeldet},
		{"tokenAusstellung429", &kern{tokenStatus: []int{429}}, fenster.ErrZuViele},
		{"mcp429", &kern{mcpStatus: []int{429}}, fenster.ErrZuViele},
		{"mcp503", &kern{mcpStatus: []int{503}}, fenster.ErrNichtErreichbar},
		{"mcp413", &kern{mcpStatus: []int{413}}, fenster.ErrZuGross},
		{"rpcFehler", &kern{mcpBody: func(string, json.RawMessage, string) string { return "ERROR:Werkzeug nicht erlaubt" }}, fenster.ErrAbgelehnt},
		{"ergebnisLeer", &kern{mcpBody: func(string, json.RawMessage, string) string { return "null" }}, fenster.ErrFehler},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGateway(t, tc.k)
			_, err := g.Werkzeuge(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestGatewayNetzfehlerIstNichtErreichbar(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	api, err := client.NewAPI(client.Trust{URL: url}, deviceToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewGateway(api, "demo").Werkzeuge(context.Background()); !errors.Is(err, fenster.ErrNichtErreichbar) {
		t.Fatal(err)
	}
}

// Fehler und Formatierung tragen nie ein Token, auch nicht mit %+v / %#v.
func TestGatewayFehlerOhneTraeger(t *testing.T) {
	k := &kern{mcpStatus: []int{401, 401}}
	g := newGateway(t, k)
	_, err := g.Werkzeuge(context.Background())
	for _, text := range []string{err.Error(), fmt.Sprintf("%v|%+v|%#v|%s", err, err, err, err)} {
		for _, secret := range append([]string{deviceToken, "jnptpc_", "SECRET"}, k.issued...) {
			if strings.Contains(text, secret) {
				t.Fatalf("Fehler enthält %q: %s", secret, text)
			}
		}
	}
	g2 := newGateway(t, &kern{})
	if _, err := g2.Werkzeuge(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{fmt.Sprintf("%v", g2), fmt.Sprintf("%+v", g2), fmt.Sprintf("%#v", g2), fmt.Sprintf("%v", g2.session)} {
		if strings.Contains(text, "jnptpc_") || strings.Contains(text, "SECRET") {
			t.Fatalf("Gateway/Sitzung druckt das Token: %s", text)
		}
	}
}

func TestGatewayParallelUndRennenfrei(t *testing.T) {
	var next atomic.Int64
	k := &kern{mcpBody: func(string, json.RawMessage, string) string { next.Add(1); return `{"tools":[]}` }}
	g := newGateway(t, k)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := g.Werkzeuge(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if next.Load() != 8 || k.tokenFetch != 1 {
		t.Fatalf("%d Aufrufe, %d Tokenabrufe", next.Load(), k.tokenFetch)
	}
}

type fakeAppAPI struct {
	tokenErr, rpcErr error
	tokens, rpcs     int
}

func (f *fakeAppAPI) AppToken(context.Context, string) (client.AppSession, error) {
	f.tokens++
	return client.AppSession{}, f.tokenErr
}

func (f *fakeAppAPI) AppRPC(context.Context, client.AppSession, int64, string, any) (json.RawMessage, error) {
	f.rpcs++
	return nil, f.rpcErr
}

// SEC-R4 F3 gw-revoked-code: Ein widerrufenes Gerät ist „nicht angemeldet“, ohne Wiederholung.
func TestGatewayWiderrufIstNichtAngemeldetOhneWiederholung(t *testing.T) {
	revoked := fmt.Errorf("wrap: %w", client.ErrRevoked)
	f := &fakeAppAPI{tokenErr: revoked}
	if _, err := NewGateway(f, "demo").Werkzeuge(context.Background()); !errors.Is(err, fenster.ErrNichtAngemeldet) || f.tokens != 1 {
		t.Fatalf("Token-Weg: %v, %d Abrufe", err, f.tokens)
	}
}

func TestGatewayFehlerabbildungAmFake(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rpcErr  error
		want    error
		retries int
	}{
		{"widerruf", fmt.Errorf("wrap: %w", client.ErrRevoked), fenster.ErrNichtAngemeldet, 0},
		{"unauthorizedEinmalNeu", &client.HTTPError{Status: 401, Class: "unauthorized"}, fenster.ErrNichtAngemeldet, 1},
		{"fremdeKlasse401", &client.HTTPError{Status: 401, Class: "anders"}, fenster.ErrNichtAngemeldet, 0},
		{"rpcFehler", &client.RPCError{Code: -32602}, fenster.ErrAbgelehnt, 0},
		{"unbekannt", errors.New("C:/geheim/pfad"), fenster.ErrFehler, 0},
		{"protokoll", client.ErrProtocol, fenster.ErrFehler, 0},
		{"zeit", context.DeadlineExceeded, fenster.ErrNichtErreichbar, 0},
		{"abbruch", context.Canceled, fenster.ErrNichtErreichbar, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeAppAPI{rpcErr: tc.rpcErr}
			g := NewGateway(f, "demo")
			g.session = client.AppSession{}
			_, err := g.Werkzeuge(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), "geheim") {
				t.Fatal("Fehlertext trägt Rohdaten")
			}
			if f.rpcs != 1+tc.retries {
				t.Fatalf("%d RPC-Aufrufe, erwartet %d", f.rpcs, 1+tc.retries)
			}
		})
	}
}
