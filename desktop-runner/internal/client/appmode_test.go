package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestManifestAppModeAmHTTPAdapter(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		valid       bool
	}{
		{"fehltAlterKern", "", true},
		{"leer", `,"mode":""`, true},
		{"a", `,"mode":"a"`, true},
		{"b", `,"mode":"b"`, true},
		{"unbekannt", `,"mode":"c"`, false},
		{"gross", `,"mode":"A"`, false},
		{"leerzeichen", `,"mode":" a"`, false},
		{"null", `,"mode":null`, false},
		{"zahl", `,"mode":1`, false},
		{"bool", `,"mode":false`, false},
		{"objekt", `,"mode":{}`, false},
		{"liste", `,"mode":[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"apps":[{"name":"demo","commit":"` + strings.Repeat("a", 64) + `","path":"apps/demo"` + tc.field + `}],"contract":5,"device_id":"aaaaaaaaaaaaaaaa","seq":42,"issued_at":"2026-11-02T12:00:00Z","grace_hours":24,"pins":[]}`
			signed, cred := l1Sign(t, []byte(raw), "aaaaaaaaaaaaaaaa")
			api := l1API(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/device/v1/manifest" || r.Header.Get("X-Jnpt-Desktop-Contract") != "5" {
					t.Error("Manifestweg/Vertrag geändert")
				}
				json.NewEncoder(w).Encode(signed)
			})
			got, err := api.Manifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			m, err := VerifyManifest(got, cred.Pubkey, cred.DeviceID, 0)
			if tc.valid {
				if err != nil || m.Apps == nil || len(*m.Apps) != 1 {
					t.Fatalf("gültiger/alter Modus abgewiesen: %v", err)
				}
			} else if !errors.Is(err, ErrManifest) {
				t.Fatalf("ungültiger Modus nicht als Manifestfehler abgewiesen: %v", err)
			}
		})
	}
}
