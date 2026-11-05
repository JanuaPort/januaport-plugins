package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kernreferenzen K6a und K6c P2 (ea50aa7a): Spiegelung aus JanuaPort/januaport, internal/desktop/testdata.
// Manifest seit #1321 aus jpwt-1321 mit optionalem mode; übrige Spiegel unverändert.
// Die Bytes wurden kopiert, nicht neu serialisiert (D0-Muster, contract/golden-v5).
var v5Golden = map[string]string{
	"manifest_apps_canonical.bin": "7cfb70018cb3265f39245b43de2e0c64f2859b8be366c4b34509454058656ae6",
	"contract5_routes.json":       "d77886162de998051fe75a191b7f0163b7315f543938f43f9db4a50efdd43c28",
	"contract5_login_routes.json": "516c7896c791d481ebb7f7747b498819b91d64a62e1ca0627d1aecf508dd8bb5",
}

func TestGoldenV5AusKern(t *testing.T) {
	for name, want := range v5Golden {
		b, err := os.ReadFile(filepath.Join("../../contract/golden-v5", name))
		if err != nil || sum(b) != want {
			t.Fatalf("Kern-Bytes %s geändert: %v", name, err)
		}
	}
	// SHA256SUMS der Spiegelung stimmt mit den Dateien überein.
	sums, err := os.ReadFile("../../contract/golden-v5/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	if len(lines) != len(v5Golden) {
		t.Fatal("SHA256SUMS unvollständig")
	}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 2 || v5Golden[strings.TrimPrefix(f[1], "*")] != f[0] {
			t.Fatal("SHA256SUMS weicht ab", line)
		}
	}
	// Vertrag 5 ändert kein bestehendes Golden: v1 (SHA256SUMS), v2 und v3 bleiben bytegleich.
	oldSums, err := os.ReadFile("../../contract/golden/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(oldSums)), "\n") {
		f := strings.Fields(line)
		raw, err := os.ReadFile(filepath.Join("../../contract/golden", strings.TrimPrefix(f[1], "*")))
		if err != nil || sum(raw) != f[0] {
			t.Fatal("Vertrag-1-Golden geändert", line, err)
		}
	}
	for path, want := range map[string]string{
		"../../contract/golden-v2/manifest_container_canonical.bin": "d376d83bdfa271f653a88de5d682e4cbb75a723ffa3a03119c2ea0ab4af88ead",
		"testdata/golden-v3/contract3_routes.json":                  "3f09b8bfb7cb924d30c3a88211b8803ec22377d614fea04d798910eef554738a",
		"testdata/golden-v3/manifest_setup_canonical.bin":           "f3060fd0110d6fd0e862e02122baef3c14c62c091f67ef25d270d5db8f60279f",
		"testdata/golden-v3/manifest_grace_canonical.bin":           "015f352e7bb53f6f2cd852469a27bcf44e0dc07df8a2e57373dc91a789d319f6",
		"testdata/golden-v3/manifest_test_canonical.bin":            "a78dca63bf2254eeae457159543e444dece6b6b4c9bc996274cf2a04492502c4",
	} {
		raw, err := os.ReadFile(path)
		if err != nil || sum(raw) != want {
			t.Fatal("bestehendes Golden geändert", path, err)
		}
	}
}

func TestManifestV5GoldenRoundtrip(t *testing.T) {
	b, err := os.ReadFile("../../contract/golden-v5/manifest_apps_canonical.bin")
	if err != nil {
		t.Fatal(err)
	}
	signed, c := l1Sign(t, b, "device-example")
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(signed) })
	got, err := a.Manifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m, err := VerifyManifest(got, c.Pubkey, c.DeviceID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.Contract != 5 || m.Apps == nil || len(*m.Apps) != 1 || (*m.Apps)[0] != (App{Name: "demo", Commit: strings.Repeat("a", 64), Path: "apps/demo", Mode: "a"}) {
		t.Fatalf("Apps nicht gelesen: %+v", m)
	}
	can, err := canonical(encode(m))
	if err != nil || !bytes.Equal(can, b) {
		t.Fatal("Roundtrip verändert Kern-Bytes", string(can), err)
	}
}

func TestManifestV5StriktAmAdapter(t *testing.T) {
	commit := strings.Repeat("a", 64)
	base := `{"apps":[{"commit":"` + commit + `","name":"demo","path":"apps/demo"}],"contract":5,"device_id":"aaaaaaaaaaaaaaaa","grace_hours":24,"issued_at":"2026-11-02T12:00:00Z","pins":[],"seq":42}`
	two := `{"apps":[{"commit":"` + commit + `","name":"alpha","path":"apps/alpha"},{"commit":"` + commit + `","name":"beta_2","path":"apps/beta_2"}],"contract":5,"device_id":"aaaaaaaaaaaaaaaa","grace_hours":24,"issued_at":"2026-11-02T12:00:00Z","pins":[],"seq":42}`
	rep := func(old, new string) string { return strings.Replace(base, old, new, 1) }
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"positiv", base, true},
		{"zweiSortiert", two, true},
		{"leereListe", rep(`[{"commit":"`+commit+`","name":"demo","path":"apps/demo"}]`, `[]`), true},
		{"appsFehlt", rep(`"apps":[{"commit":"`+commit+`","name":"demo","path":"apps/demo"}],`, ``), false},
		{"appsNull", rep(`[{"commit":"`+commit+`","name":"demo","path":"apps/demo"}]`, `null`), false},
		{"appsUnterVertrag4", strings.Replace(base, `"contract":5`, `"contract":4`, 1), false},
		{"appsUnterVertrag3", strings.Replace(base, `"contract":5`, `"contract":3`, 1), false},
		{"appsUnterVertrag2", strings.Replace(base, `"contract":5`, `"contract":2`, 1), false},
		{"appsUnterVertrag1", strings.Replace(base, `"contract":5`, `"contract":1`, 1), false},
		{"unsortiert", `{"apps":[{"commit":"` + commit + `","name":"beta_2","path":"apps/beta_2"},{"commit":"` + commit + `","name":"alpha","path":"apps/alpha"}],"contract":5,"device_id":"aaaaaaaaaaaaaaaa","grace_hours":24,"issued_at":"2026-11-02T12:00:00Z","pins":[],"seq":42}`, false},
		{"doppelt", strings.Replace(two, "beta_2", "alpha", 2), false},
		{"unbekanntesFeld", rep(`"name":"demo"`, `"name":"demo","identity":"x"`), false},
		{"tokenFeld", rep(`"name":"demo"`, `"name":"demo","token_id":"abc"`), false},
		{"nameFehlt", rep(`"name":"demo",`, ``), false},
		{"commitFehlt", rep(`"commit":"`+commit+`",`, ``), false},
		{"pathFehlt", rep(`,"path":"apps/demo"`, ``), false},
		{"nameGross", rep(`"name":"demo"`, `"name":"Demo"`), false},
		{"nameBindestrich", rep(`"name":"demo","path":"apps/demo"`, `"name":"de-mo","path":"apps/de-mo"`), false},
		{"nameLeer", rep(`"name":"demo","path":"apps/demo"`, `"name":"","path":"apps/"`), false},
		{"name49", rep(`"name":"demo","path":"apps/demo"`, `"name":"`+strings.Repeat("a", 49)+`","path":"apps/`+strings.Repeat("a", 49)+`"`), false},
		{"pfadAbweichend", rep(`"path":"apps/demo"`, `"path":"apps/andere"`), false},
		{"pfadSlash", rep(`"path":"apps/demo"`, `"path":"apps/demo/"`), false},
		{"pfadOhnePrefix", rep(`"path":"apps/demo"`, `"path":"demo"`), false},
		{"pfadAufwaerts", rep(`"path":"apps/demo"`, `"path":"apps/../demo"`), false},
		{"commitSha1", rep(commit, strings.Repeat("a", 40)), false},
		{"commitKurz", rep(commit, strings.Repeat("a", 63)), false},
		{"commitGross", rep(commit, strings.Repeat("A", 64)), false},
		{"commitNichtHex", rep(commit, strings.Repeat("g", 64)), false},
		{"doppelterSchluessel", rep(`"name":"demo"`, `"name":"demo","name":"x"`), false},
		{"doppeltApps", rep(`"apps":[`, `"apps":[],"apps":[`), false},
		{"appsKeineListe", rep(`[{"commit":"`+commit+`","name":"demo","path":"apps/demo"}]`, `{}`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if strings.HasPrefix(tc.name, "doppel") {
				// Doppelte Schlüssel lassen sich nicht kanonisieren und damit nicht gültig signieren;
				// der Parser selbst muss sie trotzdem ablehnen.
				if _, err := ParseManifest([]byte(tc.raw)); err == nil {
					t.Fatal("doppelter Schlüssel akzeptiert")
				}
				return
			}
			signed, c := l1Sign(t, []byte(tc.raw), "aaaaaaaaaaaaaaaa")
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(signed) })
			got, err := a.Manifest(context.Background())
			if err == nil {
				_, err = VerifyManifest(got, c.Pubkey, c.DeviceID, 0)
			}
			if (err == nil) != tc.valid {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestWindowsSprichVertrag5(t *testing.T) {
	if WindowsContract != 5 {
		t.Fatal(WindowsContract)
	}
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) {})
	if a.Contract() != 5 {
		t.Fatal(a.Contract())
	}
	if err := a.Server("rootless"); err != nil || a.Contract() != 2 {
		t.Fatal("Server bleibt auf Vertrag 2", err)
	}
}

func TestManifestV5RetainUntilWieVertrag4(t *testing.T) {
	three := 3
	m := Manifest{Contract: 5, IssuedAt: "2026-11-02T12:00:00Z", GraceHours: 24, Pins: []Pin{{GraceHours: &three}, {}}}
	got, err := m.RetainUntil()
	if err != nil {
		t.Fatal(err)
	}
	m4 := m
	m4.Contract = 4
	want, _ := m4.RetainUntil()
	if !got.Equal(want) {
		t.Fatal("Pinfrist unter Vertrag 5 anders als unter 4", got, want)
	}
}

// Vertrag 5 enthält Setups, Pin-Fristen und test_device unverändert aus 4.
func TestManifestV5TraegtSetupsFristenUndTestgeraet(t *testing.T) {
	raw := `{"apps":[],"contract":5,"device_id":"aaaaaaaaaaaaaaaa","grace_hours":24,"issued_at":"2026-11-02T12:00:00Z","pins":[{"algo":"sha1","commit":"` + strings.Repeat("a", 40) + `","declared_tools":[],"grace_hours":6,"path":"demo","pin":"demo","schedules":[],"setups":[{"frei":[],"input":{},"label":"Raum","name":"raum"}]}],"seq":42,"test_device":true}`
	m, err := ParseManifest([]byte(raw))
	if err != nil || !m.TestDevice || m.Pins[0].GraceHours == nil || len(m.Pins[0].Setups) != 1 {
		t.Fatal(m, err)
	}
}

func TestAppTokenAmHTTPAdapter(t *testing.T) {
	var routes []struct {
		Path     string
		Body     string
		Status   int
		Response map[string]json.RawMessage
	}
	b, err := os.ReadFile("../../contract/golden-v5/contract5_routes.json")
	if err != nil || json.Unmarshal(b, &routes) != nil || len(routes) != 2 {
		t.Fatal("Routen-Golden", err)
	}
	hits := 0
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		raw, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.Header.Get("X-Jnpt-Desktop-Contract") != "5" || r.Header.Get("Authorization") != "Bearer jnpt_synthetic" || len(raw) != 0 || r.URL.RawQuery != "" {
			t.Error("Anfrageform", r.Method, r.Header, string(raw))
		}
		if len(r.Header.Values("X-Jnpt-App-Token")) != 0 || len(r.Header.Values("X-Jnpt-Run-Token")) != 0 || r.Header.Get("Content-Type") != "" {
			t.Error("Ausstellung trägt keinen weiteren Träger")
		}
		for _, route := range routes {
			if r.URL.Path != "/api/device/v1/"+route.Path {
				continue
			}
			out := route.Response
			if route.Status == 200 {
				out["app_token"] = encode("jnptpc_ID.SECRET")
				out["expires_at"] = encode("2099-01-01T00:00:00Z")
			}
			w.WriteHeader(route.Status)
			json.NewEncoder(w).Encode(out)
			return
		}
		t.Error("unbekannte Route", r.URL.Path)
	})
	s, err := a.AppToken(context.Background(), "demo")
	if err != nil || hits != 1 || s.IsZero() {
		t.Fatal(s, err, hits)
	}
	_, err = a.AppToken(context.Background(), "unknown")
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 401 || he.Class != "unauthorized" {
		t.Fatal("401 nicht als unauthorized", err)
	}
	for _, name := range []string{"", "Demo", "a-b", "../x", "a/b", "a b", strings.Repeat("a", 49), "demo?x=1", "demo\n"} {
		before := hits
		if _, err := a.AppToken(context.Background(), name); err == nil || hits != before {
			t.Errorf("Appname %q ging ins Netz", name)
		}
	}
}

func TestAppTokenAntwortStrikt(t *testing.T) {
	good := `{"app_token":"jnptpc_ID.SECRET","expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp"}`
	for name, tc := range map[string]struct {
		body  string
		valid bool
	}{
		"gut":              {good, true},
		"fremdesPraefix":   {strings.Replace(good, "jnptpc_", "jnptapp_", 1), false},
		"geraetetokenForm": {strings.Replace(good, "jnptpc_", "jnpt_", 1), false},
		"leer":             {strings.Replace(good, "jnptpc_ID.SECRET", "", 1), false},
		"zeilenumbruch":    {strings.Replace(good, "ID.SECRET", `ID\nSECRET`, 1), false},
		"andererPfad":      {strings.Replace(good, "/api/device/v1/mcp", "/mcp", 1), false},
		"keinUTC":          {strings.Replace(good, "Z\"", "+01:00\"", 1), false},
		"fremdesFeld":      {strings.Replace(good, "}", `,"person":"x"}`, 1), false},
		"feldFehlt":        {`{"app_token":"jnptpc_ID.SECRET","expires_at":"2099-01-01T00:00:00Z"}`, false},
		"doppelt":          {strings.Replace(good, `"mcp_path"`, `"app_token":"jnptpc_X.Y","mcp_path"`, 1), false},
	} {
		t.Run(name, func(t *testing.T) {
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.body) })
			s, err := a.AppToken(context.Background(), "demo")
			if (err == nil) != tc.valid {
				t.Fatalf("err = %v", err)
			}
			if err != nil && !s.IsZero() {
				t.Fatal("Fehler liefert trotzdem eine Sitzung")
			}
		})
	}
}

func TestAppTokenOhneNoStoreIstProtokollfehler(t *testing.T) {
	srvHits := 0
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
		srvHits++
		w.Header().Set("Cache-Control", "max-age=60")
		io.WriteString(w, `{"app_token":"jnptpc_ID.SECRET","expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp"}`)
	})
	if _, err := a.AppToken(context.Background(), "demo"); err == nil {
		t.Fatal("ohne no-store angenommen")
	}
}

func TestAppSitzungDruckenTraegtKeinToken(t *testing.T) {
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"app_token":"jnptpc_ID.SECRETWERT","expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp"}`)
	})
	s, err := a.AppToken(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s %q", s, s, s, s, s), fmt.Sprint(&s)} {
		if strings.Contains(text, "SECRETWERT") || strings.Contains(text, "jnptpc_") {
			t.Fatalf("Ausgabe enthält das Token: %s", text)
		}
	}
}

func TestAppRPCAmHTTPAdapter(t *testing.T) {
	var sawApp string
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/device/v1/apps/demo/token" {
			io.WriteString(w, `{"app_token":"jnptpc_ID.SECRETWERT","expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp"}`)
			return
		}
		sawApp = r.Header.Get("X-Jnpt-App-Token")
		h := r.Header
		if r.URL.Path != "/api/device/v1/mcp" || h.Get("X-Jnpt-Desktop-Contract") != "5" || h.Get("Authorization") != "Bearer jnpt_synthetic" || len(h.Values("X-Jnpt-App-Token")) != 1 || len(h.Values("X-Jnpt-Run-Token")) != 0 {
			t.Error("Kopfzeilen", h)
		}
		var req struct {
			JSONRPC string `json:"jsonrpc"`
			ID      int64  `json:"id"`
			Method  string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[]}}`, req.ID)
		case "tools/call":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32602,"message":"RAWTEXT jnptpc_ID.SECRETWERT"}}`, req.ID)
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"x":1}}`, req.ID+1)
		}
	})
	s, err := a.AppToken(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.AppRPC(context.Background(), s, 7, "tools/list", nil)
	if err != nil || string(res) != `{"tools":[]}` || sawApp != "jnptpc_ID.SECRETWERT" {
		t.Fatal(string(res), err, sawApp)
	}
	_, err = a.AppRPC(context.Background(), s, 8, "tools/call", map[string]any{"name": "x", "arguments": map[string]any{}})
	var rpc *RPCError
	if !errors.As(err, &rpc) || rpc.Code != -32602 || strings.Contains(err.Error(), "RAWTEXT") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("JSON-RPC-Fehler trägt Rohtext: %v", err)
	}
	if _, err = a.AppRPC(context.Background(), s, 9, "initialize", nil); err == nil {
		t.Fatal("nur tools/list und tools/call")
	}
	if _, err = a.AppRPC(context.Background(), AppSession{}, 9, "tools/list", nil); err == nil {
		t.Fatal("leere Sitzung gesendet")
	}
}
