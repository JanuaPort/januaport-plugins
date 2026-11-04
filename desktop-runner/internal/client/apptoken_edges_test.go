package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

// Testwert aus Teilstrings: kein Token-Muster im Quelltext.
func appTokenText() string { return "jn" + "ptpc_ID." + "WERT" }

func appSession(t *testing.T, a *API) AppSession {
	t.Helper()
	s, err := a.AppToken(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// tokenServer liefert ein App-Token und beantwortet MCP-Aufrufe mit reply(id, method).
func tokenServer(t *testing.T, hits *atomic.Int32, reply func(id int64, method string) string) *API {
	return l1API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/device/v1/apps/demo/token" {
			fmt.Fprintf(w, `{"app_token":%q,"expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp"}`, appTokenText())
			return
		}
		hits.Add(1)
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		_, _ = io.WriteString(w, reply(req.ID, req.Method))
	})
}

// SEC-R4 F3 rpc-methode: Nur tools/list und tools/call verlassen den Läufer, sonst kein einziger Byte.
func TestAppRPCNurZweiMethoden(t *testing.T) {
	var hits atomic.Int32
	a := tokenServer(t, &hits, func(id int64, m string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"m":%q}}`, id, m)
	})
	s := appSession(t, a)
	for _, m := range []string{"", "initialize", "tools/", "tools", "tools/cancel", "TOOLS/LIST", "tools/list ", " tools/list", "tools/call\n", "tools/listx", "resources/read", "notifications/initialized", "prompts/list", "tools/list\x00", "tools/list/tools/call"} {
		before := hits.Load()
		if _, err := a.AppRPC(context.Background(), s, 1, m, nil); err == nil || hits.Load() != before {
			t.Errorf("Methode %q erreichte das Netz oder wurde angenommen", m)
		}
	}
	for _, m := range []string{"tools/list", "tools/call"} {
		before := hits.Load()
		if _, err := a.AppRPC(context.Background(), s, 5, m, nil); err != nil || hits.Load() != before+1 {
			t.Errorf("Methode %q abgewiesen: %v", m, err)
		}
	}
}

// SEC-R4 F3 rpc-id und Antwortform: Eine Antwort gilt nur zur gestellten Frage.
func TestAppRPCAntwortMussZurFrageGehoeren(t *testing.T) {
	for name, tc := range map[string]struct {
		reply func(id int64) string
		ok    bool
	}{
		"gut":            {func(id int64) string { return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"x":1}}`, id) }, true},
		"fremdeId":       {func(id int64) string { return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"x":1}}`, id+1) }, false},
		"idNull":         {func(int64) string { return `{"jsonrpc":"2.0","id":0,"result":{"x":1}}` }, false},
		"idFehlt":        {func(int64) string { return `{"jsonrpc":"2.0","result":{"x":1}}` }, false},
		"falscheVersion": {func(id int64) string { return fmt.Sprintf(`{"jsonrpc":"1.0","id":%d,"result":{"x":1}}`, id) }, false},
		"ergebnisUndFehler": {func(id int64) string {
			return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"x":1},"error":{"code":-1}}`, id)
		}, false},
		"nullErgebnis": {func(id int64) string { return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, id) }, false},
		"keinErgebnis": {func(id int64) string { return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d}`, id) }, false},
		"fremdesFeld":  {func(id int64) string { return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"x":1},"extra":1}`, id) }, false},
		"doppelteId": {func(id int64) string {
			return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"id":%d,"result":{"x":1}}`, id, id)
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int32
			a := tokenServer(t, &hits, func(id int64, _ string) string { return tc.reply(id) })
			s := appSession(t, a)
			_, err := a.AppRPC(context.Background(), s, 41, "tools/list", nil)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v", err)
			}
			var rpc *RPCError
			if !tc.ok && errors.As(err, &rpc) {
				t.Fatalf("Fachfehler statt Protokollfehler: %v", err)
			}
		})
	}
}

// SEC-R4 F3 http-beide-traeger / http-app-endpoint: Nie zwei Träger, App-Token nur an mcp.
func TestRequestAppTraegerRegeln(t *testing.T) {
	var hits atomic.Int32
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })
	app := appTokenText()
	for name, tc := range map[string]struct{ endpoint, run, app string }{
		"laufUndApp":          {"mcp", "run-token", app},
		"appAnHeartbeat":      {"heartbeat", "", app},
		"appAnTokenRoute":     {"apps/demo/token", "", app},
		"appAnPaket":          {"packages/x", "", app},
		"appAnManifest":       {"manifest", "", app},
		"appOhnePraefix":      {"mcp", "", "fremd_token"},
		"appMitZeilenumbruch": {"mcp", "", app + "\nX-Evil: 1"},
		"laufAnHeartbeat":     {"heartbeat", "run-token", ""},
	} {
		t.Run(name, func(t *testing.T) {
			before := hits.Load()
			if _, _, err := a.requestApp(context.Background(), "POST", tc.endpoint, nil, 0, tc.run, tc.app, 1<<10); err == nil || hits.Load() != before {
				t.Fatalf("Anfrage erreichte das Netz oder gelang: %v", err)
			}
		})
	}
	// Kontrolle: genau ein Träger an mcp geht durch.
	_, _, _ = a.requestApp(context.Background(), "POST", "mcp", nil, 0, "", app, 1<<10) // leere Antwort: Protokollfehler, entscheidend ist der Treffer
	if hits.Load() != 1 {
		t.Fatalf("Kontrollanfrage erreichte das Netz %d-mal", hits.Load())
	}
}
