package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const personReply = `{"app_token":"jnptpc_synthetic-person","expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp","anzeigename":"Gleich","person_gewechselt":true}`

func TestPCLoginVertrag5Adapter(t *testing.T) {
	calls := 0
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.RawQuery != "" || r.Header.Get("X-Jnpt-Desktop-Contract") != "5" || r.Header.Get("Authorization") != "Bearer jnpt_synthetic" || r.Header.Get("X-Jnpt-App-Token") != "" || r.Header.Get("X-Jnpt-Run-Token") != "" {
			t.Error("Vertrag/Header")
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if json.Unmarshal(raw, &body) != nil {
			t.Error("JSON")
		}
		switch r.URL.Path {
		case "/api/device/v1/apps/demo/login":
			if len(body) != 3 || body["challenge"] != strings.Repeat("A", 43) || body["state"] != strings.Repeat("s", 43) || body["port"] != float64(12345) {
				t.Error("Login-Body", body)
			}
			fmt.Fprint(w, `{"authorize_url":"https://idp.example/authorize?a=b&state=c"}`)
		case "/api/device/v1/apps/demo/session":
			if len(body) != 2 || body["code"] != "synthetic-code" || body["verifier"] != strings.Repeat("v", 43) {
				t.Error("Session-Body")
			}
			fmt.Fprint(w, personReply)
		default:
			t.Error("unerwartete Route", r.URL.Path)
		}
	})
	u, e := a.AppLogin(context.Background(), "demo", strings.Repeat("A", 43), 12345, strings.Repeat("s", 43))
	if e != nil || u != "https://idp.example/authorize?a=b&state=c" {
		t.Fatal(e)
	}
	p, e := a.AppSession(context.Background(), "demo", "synthetic-code", strings.Repeat("v", 43))
	if e != nil || p.Session.IsZero() || !p.Changed || p.Name != "Gleich" {
		t.Fatal("Sitzung", e)
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", p, p), "Gleich") || strings.Contains(fmt.Sprintf("%+v", p), "jnptpc_") {
		t.Fatal("Person/Träger gedruckt")
	}
	for _, v := range []struct {
		name, challenge, state string
		port                   int
	}{{"../x", strings.Repeat("A", 43), strings.Repeat("s", 43), 12345}, {"demo", "x", strings.Repeat("s", 43), 12345}, {"demo", strings.Repeat("A", 43), "x", 12345}, {"demo", strings.Repeat("A", 43), strings.Repeat("s", 43), 1023}, {"demo", strings.Repeat("A", 43), strings.Repeat("s", 43), 65536}} {
		if _, e := a.AppLogin(context.Background(), v.name, v.challenge, v.port, v.state); e == nil {
			t.Fatal("ungültiger Login")
		}
	}
	if calls != 2 {
		t.Fatal("ungültige Anfrage hat Netz erreicht", calls)
	}
}

func TestPCSessionStriktUndWechselImZweifel(t *testing.T) {
	for _, tc := range []struct {
		body           string
		valid, changed bool
	}{
		{personReply, true, true}, {strings.Replace(personReply, `"person_gewechselt":true`, `"person_gewechselt":false`, 1), true, false},
		{strings.Replace(personReply, `,"person_gewechselt":true`, "", 1), true, true}, {strings.Replace(personReply, `"person_gewechselt":true`, `"person_gewechselt":null`, 1), true, true},
		{strings.Replace(personReply, `"Gleich"`, `"A\u202e\nB"`, 1), true, true},
		{strings.Replace(personReply, "jnptpc_synthetic-person", "jwt", 1), false, false}, {strings.Replace(personReply, "/api/device/v1/mcp", "/mcp", 1), false, false},
		{strings.Replace(personReply, "2099-01-01T00:00:00Z", "no", 1), false, false}, {strings.Replace(personReply, `"anzeigename"`, `"unknown"`, 1), false, false},
		{strings.TrimSuffix(personReply, "}") + `,"person_gewechselt":false}`, false, false},
	} {
		a := l1API(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) })
		p, e := a.AppSession(context.Background(), "demo", "synthetic-code", strings.Repeat("v", 43))
		if (e == nil) != tc.valid || (e == nil && p.Changed != tc.changed) {
			t.Fatal(tc.body, e)
		}
		if strings.ContainsAny(p.Name, "\n\u202e") {
			t.Fatal("Name")
		}
	}
	for _, body := range []string{`{"authorize_url":"http://idp.example/"}`, `{"authorize_url":"https://user@idp.example.org/"}`, `{"authorize_url":"https://idp.example/","extra":1}`} {
		a := l1API(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if _, e := a.AppLogin(context.Background(), "demo", strings.Repeat("A", 43), 12345, strings.Repeat("s", 43)); e == nil {
			t.Fatal(body)
		}
	}
}
