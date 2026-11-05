package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Lokales Adapter-Golden nach k6-vertrag5/P2 mit konkreten Client-Anfragen.
// Den bytegleichen Kernspiegel prüft zusätzlich TestGoldenV5AusKern.
func TestPCLoginAdapterGolden(t *testing.T) {
	b, e := os.ReadFile("testdata/contract5_login_adapter.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Path     string          `json:"path"`
		Request  json.RawMessage `json:"request"`
		Status   int             `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if e = json.Unmarshal(b, &rows); e != nil || len(rows) != 5 {
		t.Fatal("Golden", e)
	}
	i := 0
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
		if i >= len(rows) {
			t.Error("zusaetzliche Route")
			w.WriteHeader(500)
			return
		}
		row := rows[i]
		i++
		if r.Method != "POST" || r.URL.Path != "/api/device/v1/"+row.Path || r.URL.RawQuery != "" {
			t.Error("Route", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer jnpt_synthetic" || r.Header.Get("X-Jnpt-Desktop-Contract") != "5" || len(r.Header.Values("X-Jnpt-App-Token")) != 0 || len(r.Header.Values("X-Jnpt-Run-Token")) != 0 {
			t.Error("Traegerrollen")
		}
		raw, _ := io.ReadAll(r.Body)
		if string(row.Request) == "null" {
			if len(raw) != 0 {
				t.Error("Token-Body")
			}
		} else {
			want, _ := canonical(row.Request)
			got, e := canonical(raw)
			if e != nil || !bytes.Equal(want, got) {
				t.Error("Request-Golden", string(raw))
			}
		}
		w.WriteHeader(row.Status)
		_, _ = w.Write(row.Response)
	})
	if _, e := a.AppToken(context.Background(), "demo"); e == nil {
		t.Fatal("Modus b stellt Token aus")
	}
	if _, e := a.AppLogin(context.Background(), "demo", strings.Repeat("A", 43), 12345, strings.Repeat("s", 43)); e != nil {
		t.Fatal(e)
	}
	if p, e := a.AppSession(context.Background(), "demo", "synthetic-code", strings.Repeat("v", 43)); e != nil || p.Session.IsZero() || !p.Changed {
		t.Fatal("Session", e)
	}
	for _, class := range []string{"kein_zugriff", "platz_gesperrt"} {
		_, e := a.AppSession(context.Background(), "demo", "synthetic-code", strings.Repeat("v", 43))
		var he *HTTPError
		if !errors.As(e, &he) || he.Status != 403 || he.Class != class {
			t.Fatal("feste Klasse", e)
		}
	}
	if i != len(rows) {
		t.Fatal("Golden unvollstaendig", i)
	}
}
