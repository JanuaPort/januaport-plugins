package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// Lauf-Token-Zweig von mcp (JanuaPort/januaport#1340): Der Kern bedient ihn mit
// go-sdk v1.7.0 stateless per SSE und verlangt `Accept` mit BEIDEN Werten.
// Die Aufnahme testdata/gosdk_v1.7.0_lauf_mcp.json stammt von einem echten
// mcp.NewStreamableHTTPHandler (go-sdk v1.7.0, Stateless, ohne JSONResponse,
// Werkzeug mit Log- und Progress-Benachrichtigung vor dem Ergebnis), erzeugt
// mit dem Wegwerf-Programm jp1340a-golden (außerhalb des Repos, nicht
// eingecheckt). Ein Testdouble des Drahtformats hat den Fehler zweimal
// verdeckt, deshalb spielt der Fake hier nur diese Bytes ab und weist eine
// Anfrage ohne beide Accept-Werte so ab wie das SDK (400).

type gosdkAntwort struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type gosdkAufnahme struct {
	SDK        string       `json:"sdk"`
	RequestID  int64        `json:"request_id"`
	SSE        gosdkAntwort `json:"sse_mit_accept"`
	OhneAccept gosdkAntwort `json:"ohne_accept"`
}

func ladeAufnahme(t *testing.T) gosdkAufnahme {
	t.Helper()
	raw, err := os.ReadFile("testdata/gosdk_v1.7.0_lauf_mcp.json")
	if err != nil {
		t.Fatal(err)
	}
	var g gosdkAufnahme
	if err = json.Unmarshal(raw, &g); err != nil || !strings.Contains(g.SDK, "v1.7.0") {
		t.Fatalf("Aufnahme unlesbar: %v", err)
	}
	return g
}

func (g gosdkAntwort) schreibe(w http.ResponseWriter) {
	for k, v := range g.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(g.Status)
	_, _ = io.WriteString(w, g.Body)
}

// rohAPI schreibt keine Standardheader vor: Jeder Test setzt Content-Type und
// Cache-Control selbst und sieht so, was der Läufer wirklich verlangt.
func rohAPI(t *testing.T, handler http.HandlerFunc) *API {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	a, err := NewAPI(Trust{URL: s.URL}, "jnpt_synthetic", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

func acceptHatBeide(h string) bool {
	return strings.Contains(h, "application/json") && strings.Contains(h, "text/event-stream")
}

func TestLaufMCPGegenAufnahmeDesEchtenSDK(t *testing.T) {
	g := ladeAufnahme(t)
	var accept, runToken, path, ct string
	var body []byte
	a := rohAPI(t, func(w http.ResponseWriter, r *http.Request) {
		accept, runToken, path, ct = r.Header.Get("Accept"), r.Header.Get("X-Jnpt-Run-Token"), r.URL.Path, r.Header.Get("Content-Type")
		body, _ = io.ReadAll(r.Body)
		if !acceptHatBeide(accept) {
			g.OhneAccept.schreibe(w)
			return
		}
		g.SSE.schreibe(w)
	})
	got, err := a.MCP(context.Background(), "run-token-test", g.RequestID, "demo_echo", json.RawMessage(`{"text":"hallo"}`))
	if err != nil {
		t.Fatalf("SSE-Antwort des echten SDK abgewiesen (Accept=%q): %v", accept, err)
	}
	if want := `{"content":[{"type":"text","text":"{\"echo\":\"hallo\"}"}],"structuredContent":{"echo":"hallo"}}`; !l1JSONEqual(got, []byte(want)) {
		t.Fatalf("Ergebnis: %s", got)
	}
	if !acceptHatBeide(accept) || path != "/api/device/v1/mcp" || runToken != "run-token-test" || ct != "application/json" {
		t.Fatalf("Anfrage: accept=%q path=%q token=%q ct=%q", accept, path, runToken, ct)
	}
	if !bytes.Contains(body, []byte(`"method":"tools/call"`)) || !bytes.Contains(body, []byte(`"id":7`)) {
		t.Fatalf("Körper: %s", body)
	}
}

// Bestätigt am Befund: Ohne Accept lehnt das SDK mit 400 ab; der Läufer muss
// ihn also selbst setzen (nicht der Kern).
func TestLaufMCPOhneAcceptWareDerBefund(t *testing.T) {
	g := ladeAufnahme(t)
	if g.OhneAccept.Status != 400 || !strings.Contains(g.OhneAccept.Body, "Accept must contain both") {
		t.Fatalf("Aufnahme bestätigt den Befund nicht: %+v", g.OhneAccept)
	}
}

func sseEreignis(daten string) string { return "event: message\ndata: " + daten + "\n\n" }

const (
	antwort7    = `{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`
	antwort8    = `{"jsonrpc":"2.0","id":8,"result":{"ok":true}}`
	fehler7     = `{"jsonrpc":"2.0","id":7,"error":{"code":-32603,"message":"x"}}`
	nachricht   = `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"x"}}`
	fortschritt = `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"p","progress":1}}`
)

func sseAntwort(w http.ResponseWriter, cache, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", cache)
	_, _ = io.WriteString(w, body)
}

const sdkCache = "no-cache, no-transform"

func TestLaufMCPStromGueltigeFormen(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"nurAntwort":                      {sseEreignis(antwort7), `{"ok":true}`},
		"benachrichtigungenZuerst":        {sseEreignis(nachricht) + sseEreignis(fortschritt) + sseEreignis(antwort7), `{"ok":true}`},
		"erstesGiltDanachEgal":            {sseEreignis(antwort7) + sseEreignis(antwort8) + "kaputt", `{"ok":true}`},
		"crlf":                            {"event: message\r\ndata: " + antwort7 + "\r\n\r\n", `{"ok":true}`},
		"kommentarUndIdZeilen":            {": ping\nid: 3\nretry: 100\nevent: message\ndata: " + antwort7 + "\n\n", `{"ok":true}`},
		"datenOhneLeerzeichen":            {"data:" + antwort7 + "\n\n", `{"ok":true}`},
		"datenUeberMehrereZeilen":         {"data: {\"jsonrpc\":\"2.0\",\ndata: \"id\":7,\"result\":{\"ok\":true}}\n\n", `{"ok":true}`},
		"leeresEreignisWirdUebersprungen": {"\n\n" + sseEreignis(antwort7), `{"ok":true}`},
	} {
		t.Run(name, func(t *testing.T) {
			a := rohAPI(t, func(w http.ResponseWriter, r *http.Request) { sseAntwort(w, sdkCache, tc.body) })
			got, err := a.MCP(context.Background(), "run-token-test", 7, "demo_echo", json.RawMessage(`{}`))
			if err != nil || !l1JSONEqual(got, []byte(tc.want)) {
				t.Fatalf("got %s err %v", got, err)
			}
		})
	}
}

func TestLaufMCPStromProtokollfehler(t *testing.T) {
	gross := strings.Repeat(": fuellung................................................................\n", (9<<20)/80)
	for name, tc := range map[string]struct{ cache, body string }{
		"fremdeIdAllein":            {sdkCache, sseEreignis(antwort8)},
		"fremdeIdVorEigener":        {sdkCache, sseEreignis(antwort8) + sseEreignis(antwort7)},
		"keineAntwort":              {sdkCache, ""},
		"nurBenachrichtigungen":     {sdkCache, sseEreignis(nachricht) + sseEreignis(fortschritt)},
		"ereignisOhneAbschluss":     {sdkCache, "data: " + antwort7 + "\n"},
		"keinJSON":                  {sdkCache, sseEreignis("kein json")},
		"fehlerAntwort":             {sdkCache, sseEreignis(fehler7)},
		"anfrageDesServersMitId":    {sdkCache, sseEreignis(`{"jsonrpc":"2.0","id":7,"method":"sampling/createMessage"}`)},
		"unbekanntesFeld":           {sdkCache, sseEreignis(`{"jsonrpc":"2.0","id":7,"result":{"ok":true},"extra":1}`)},
		"falscheJSONRPCVersion":     {sdkCache, sseEreignis(`{"jsonrpc":"1.0","id":7,"result":{"ok":true}}`)},
		"ueber8MiBVorAntwort":       {sdkCache, gross + sseEreignis(antwort7)},
		"ueber8MiBOhneAntwort":      {sdkCache, gross},
		"noStoreAnStelleVonNoCache": {"no-store", sseEreignis(antwort7)},
		"nurNoCache":                {"no-cache", sseEreignis(antwort7)},
		"noCacheOhneNoTransform":    {"no-cache, no-store", sseEreignis(antwort7)},
		"keinCacheControl":          {"", sseEreignis(antwort7)},
	} {
		t.Run(name, func(t *testing.T) {
			a := rohAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.cache == "" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, tc.body)
					return
				}
				sseAntwort(w, tc.cache, tc.body)
			})
			if _, err := a.MCP(context.Background(), "run-token-test", 7, "demo_echo", json.RawMessage(`{}`)); !errors.Is(err, ErrProtocol) {
				t.Fatalf("erwartet ErrProtocol, bekam %v", err)
			}
		})
	}
}

func TestLaufMCPSessionIdBleibtProtokollfehler(t *testing.T) {
	a := rohAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Mcp-Session-Id", "sitzung")
		sseAntwort(w, sdkCache, sseEreignis(antwort7))
	})
	if _, err := a.MCP(context.Background(), "run-token-test", 7, "demo_echo", json.RawMessage(`{}`)); !errors.Is(err, ErrProtocol) {
		t.Fatalf("Mcp-Session-Id angenommen: %v", err)
	}
}

// JSON mit no-store bleibt auf dem Lauf-Zweig gültig (falls der Kern umstellt),
// no-cache, no-transform gilt nur für text/event-stream.
func TestLaufMCPJSONRegeln(t *testing.T) {
	for name, tc := range map[string]struct {
		cache string
		ok    bool
	}{
		"noStore":         {"no-store", true},
		"noCacheNoTransf": {sdkCache, false},
		"noCache":         {"no-cache", false},
		"fehlt":           {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			a := rohAPI(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.cache != "" {
					w.Header().Set("Cache-Control", tc.cache)
				}
				_, _ = io.WriteString(w, antwort7)
			})
			got, err := a.MCP(context.Background(), "run-token-test", 7, "demo_echo", json.RawMessage(`{}`))
			if tc.ok != (err == nil) || (tc.ok && !l1JSONEqual(got, []byte(`{"ok":true}`))) {
				t.Fatalf("got %s err %v", got, err)
			}
			if !tc.ok && !errors.Is(err, ErrProtocol) {
				t.Fatalf("erwartet ErrProtocol: %v", err)
			}
		})
	}
}

// Die gelockerte Cache-Control-Regel gilt NUR für text/event-stream auf mcp
// mit Lauf-Token, nie für einen anderen Endpunkt.
func TestEventStreamAufAnderenEndpunktenAbgewiesen(t *testing.T) {
	var hits atomic.Int32
	a := rohAPI(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sseAntwort(w, sdkCache, sseEreignis(`{}`))
	})
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"config":    func() error { _, e := a.Config(ctx); return e },
		"manifest":  func() error { _, e := a.Manifest(ctx); return e },
		"heartbeat": func() error { _, e := a.Heartbeat(ctx, Heartbeat{Contract: a.Contract()}); return e },
		"claim":     func() error { _, e := a.Claim(ctx); return e },
		"mcpOhneLauftoken": func() error {
			_, _, e := a.request(ctx, "POST", "mcp", nil, 0, "", 1<<10)
			return e
		},
	} {
		before := hits.Load()
		if err := call(); !errors.Is(err, ErrProtocol) || hits.Load() != before+1 {
			t.Errorf("%s: %v (Treffer %d)", name, err, hits.Load()-before)
		}
	}
}

// App-Zweig (requestApp mit App-Token auf mcp) bleibt unverändert: nur
// application/json mit no-store; kein Accept für den Strom (der Kern setzt es
// dort selbst) und jede text/event-stream-Antwort ist ein Protokollfehler,
// auch mit no-cache, no-transform.
func TestAppZweigNimmtNurJSONMitNoStore(t *testing.T) {
	for name, tc := range map[string]struct {
		ctype, cache string
		ok           bool
	}{
		"jsonNoStore":            {"application/json", "no-store", true},
		"jsonNoCacheNoTransform": {"application/json", sdkCache, false},
		"sseNoCacheNoTransform":  {"text/event-stream", sdkCache, false},
		"sseNoStore":             {"text/event-stream", "no-store", false},
		"sseOhneCacheControl":    {"text/event-stream", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			var accept string
			a := rohAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/device/v1/apps/demo/token" {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Cache-Control", "no-store")
					fmt.Fprintf(w, `{"app_token":%q,"expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp"}`, appTokenText())
					return
				}
				accept = r.Header.Get("Accept")
				w.Header().Set("Content-Type", tc.ctype)
				if tc.cache != "" {
					w.Header().Set("Cache-Control", tc.cache)
				}
				if tc.ctype == "text/event-stream" {
					_, _ = io.WriteString(w, sseEreignis(`{"jsonrpc":"2.0","id":5,"result":{"ok":true}}`))
					return
				}
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":5,"result":{"ok":true}}`)
			})
			s := appSession(t, a)
			got, err := a.AppRPC(context.Background(), s, 5, "tools/call", struct{}{})
			if tc.ok && (err != nil || !l1JSONEqual(got, []byte(`{"ok":true}`))) {
				t.Fatalf("gültige App-Antwort abgewiesen: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrProtocol) {
				t.Fatalf("erwartet ErrProtocol, bekam %v", err)
			}
			if strings.Contains(accept, "text/event-stream") {
				t.Fatalf("App-Zweig bittet um den Strom: Accept=%q", accept)
			}
		})
	}
}
