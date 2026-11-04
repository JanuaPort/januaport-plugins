package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

const (
	appTokenPrefix = "jnptpc_"
	appTokenHeader = "X-Jnpt-App-Token" //nolint:gosec // Headername, kein Credential.
)

// AppSession hält ein gerätegebundenes App-Token (Vertrag 5) ausschließlich im
// RAM. Das Token hat keinen Getter und druckt sich nie (fmt.Formatter), weder
// in Fehlern noch in Logs.
type AppSession struct {
	token   string
	expires time.Time
}

func (s AppSession) IsZero() bool { return s.token == "" }

// Expires ist der vom Kern genannte Ablauf; der Kern bleibt maßgeblich (401).
func (s AppSession) Expires() time.Time { return s.expires }

func (s AppSession) String() string { return "AppSession(***)" }

// Format verhindert, dass %v, %+v, %#v oder %q das Token ausgeben.
func (s AppSession) Format(f fmt.State, _ rune) { _, _ = fmt.Fprint(f, "AppSession(***)") }

type appTokenReply struct {
	AppToken  string `json:"app_token"`
	ExpiresAt string `json:"expires_at"`
	MCPPath   string `json:"mcp_path"`
}

// AppToken holt ein App-Token (POST apps/{name}/token, leerer Körper). Der Kern
// nennt Unbekanntes, Nichtzugewiesenes und Gestopptes gleich mit 401.
func (a *API) AppToken(ctx context.Context, name string) (AppSession, error) {
	if !fenster.ValidAppName(name) || a.Contract() != WindowsContract {
		return AppSession{}, ErrProtocol
	}
	b, status, err := a.request(ctx, "POST", "apps/"+name+"/token", nil, 0, "", 16<<10)
	if err != nil {
		return AppSession{}, err
	}
	var out appTokenReply
	if status != 200 || strict(b, &out) != nil || !strings.HasPrefix(out.AppToken, appTokenPrefix) || len(out.AppToken) == len(appTokenPrefix) ||
		strings.ContainsAny(out.AppToken, " \r\n\t") || !utc(out.ExpiresAt) || out.MCPPath != "/api/device/v1/mcp" {
		return AppSession{}, ErrProtocol
	}
	expires, _ := time.Parse(time.RFC3339, out.ExpiresAt)
	return AppSession{token: out.AppToken, expires: expires}, nil
}

// RPCError ist ein JSON-RPC-Fachfehler des Kerns. Er trägt nur den Code, nie den
// Text, damit nichts davon bis zur Seite durchgereicht werden kann.
type RPCError struct{ Code int }

func (e *RPCError) Error() string { return "rpc_error" }

// AppRPC sendet tools/list oder tools/call mit Gerätetoken und genau einem
// App-Token an den Geräte-MCP-Weg.
func (a *API) AppRPC(ctx context.Context, s AppSession, id int64, method string, params any) (json.RawMessage, error) {
	if s.IsZero() || (method != "tools/list" && method != "tools/call") {
		return nil, ErrProtocol
	}
	body := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", id, method, params}
	b, _, err := a.requestApp(ctx, "POST", "mcp", body, 0, "", s.token, 8<<20)
	if err != nil {
		return nil, err
	}
	var reply struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int64           `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   json.RawMessage `json:"error,omitempty"`
	}
	if strict(b, &reply) != nil || reply.JSONRPC != "2.0" || reply.ID != id {
		return nil, ErrProtocol
	}
	if len(reply.Error) != 0 {
		var e struct {
			Code int `json:"code"`
		}
		if len(reply.Result) != 0 || json.Unmarshal(reply.Error, &e) != nil {
			return nil, ErrProtocol
		}
		return nil, &RPCError{Code: e.Code}
	}
	if len(reply.Result) == 0 || string(reply.Result) == "null" {
		return nil, ErrProtocol
	}
	return reply.Result, nil
}
