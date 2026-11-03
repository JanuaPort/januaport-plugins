package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func l1API(t *testing.T, handler http.HandlerFunc) *API {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	a, err := NewAPI(Trust{URL: s.URL}, "jnpt_synthetic", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

func l1Manifest() Manifest {
	return Manifest{Contract: 4, DeviceID: "aaaaaaaaaaaaaaaa", Seq: 42, IssuedAt: "2026-11-01T12:00:00Z", GraceHours: 24,
		Pins: []Pin{{Pin: "test_aaaaaaaa", Algo: "sha1", Commit: strings.Repeat("a", 40), Path: "demo", DeclaredTools: []string{}, Schedules: []Schedule{}}}}
}

func l1Sign(t *testing.T, raw []byte, device string) (SignedManifest, Credential) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	return SignedManifest{Manifest: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, c))}, Credential{DeviceID: device, Pubkey: pub}
}

func TestVertragJeGeraeteart(t *testing.T) {
	for _, server := range []bool{false, true} {
		t.Run(fmt.Sprint(server), func(t *testing.T) {
			want := 4
			if server {
				want = 2
			}
			hits := 0
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.Header.Get("X-Jnpt-Desktop-Contract") != fmt.Sprint(want) {
					t.Error("Vertragsheader", r.Header)
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["contract"] != float64(want) {
					t.Error("JSON-Vertrag", body)
				}
				if strings.HasSuffix(r.URL.Path, "pair") {
					if r.Header.Get("Authorization") != "" {
						t.Error("pair mit Bearer")
					}
					json.NewEncoder(w).Encode(PairReply{DeviceID: "aaaaaaaaaaaaaaaa", Token: "jnpt_synthetic", ExpiresAt: "2027-01-01T00:00:00Z", AnlagePubkey: base64.StdEncoding.EncodeToString(make([]byte, 32))})
				} else {
					io.WriteString(w, `{"cancel":[],"manifest_seq_current":42}`)
				}
			})
			if server {
				if err := a.Server("rootless"); err != nil {
					t.Fatal(err)
				}
			}
			if a.Contract() != want {
				t.Fatalf("Contract()=%d, erwartet %d", a.Contract(), want)
			}
			if _, err := a.Pair(context.Background(), "aaaaaaaaaaaaaaaa.ABCD-EFGH-JKMN", "0.3.0"); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Heartbeat(context.Background(), Heartbeat{want, true, "0.3.0", 42}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Heartbeat(context.Background(), Heartbeat{3, true, "0.3.0", 42}); err == nil || hits != 2 {
				t.Fatal("falscher Vertrag gesendet", err, hits)
			}
		})
	}
}

func TestGoldenV3AusKern(t *testing.T) {
	for name, sha := range map[string]string{
		"contract3_routes.json":        "3f09b8bfb7cb924d30c3a88211b8803ec22377d614fea04d798910eef554738a",
		"manifest_setup_canonical.bin": "f3060fd0110d6fd0e862e02122baef3c14c62c091f67ef25d270d5db8f60279f",
		"manifest_grace_canonical.bin": "015f352e7bb53f6f2cd852469a27bcf44e0dc07df8a2e57373dc91a789d319f6",
		"manifest_test_canonical.bin":  "a78dca63bf2254eeae457159543e444dece6b6b4c9bc996274cf2a04492502c4",
	} {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("testdata/golden-v3", name))
			if err != nil {
				t.Fatal(err)
			}
			if sum(b) != sha {
				t.Fatal("Kern-Bytes geändert")
			}
			if strings.HasSuffix(name, ".json") {
				return
			}
			var header struct {
				DeviceID string `json:"device_id"`
			}
			if err = json.Unmarshal(b, &header); err != nil {
				t.Fatal(err)
			}
			signed, c := l1Sign(t, b, header.DeviceID)
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(signed) })
			got, err := a.Manifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			m, err := VerifyManifest(got, c.Pubkey, c.DeviceID, 0)
			if err != nil {
				t.Fatal(err)
			}
			can, err := canonical(encode(m))
			if err != nil || !bytes.Equal(can, b) {
				t.Fatal("Roundtrip verändert Kern-Bytes", string(can), err)
			}
		})
	}
	for _, dir := range []string{"golden", "golden-v2"} {
		b, err := os.ReadFile(filepath.Join("../../contract", dir, "SHA256SUMS"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			fields := strings.Fields(line)
			raw, err := os.ReadFile(filepath.Join("../../contract", dir, strings.TrimPrefix(fields[1], "*")))
			if err != nil || sum(raw) != fields[0] {
				t.Fatal("Altes Golden geändert", line, err)
			}
		}
	}
}

func TestManifestV4StriktAmAdapter(t *testing.T) {
	base := `{"contract":4,"device_id":"aaaaaaaaaaaaaaaa","seq":42,"issued_at":"2026-11-01T12:00:00Z","grace_hours":24,"pins":[{"pin":"demo","algo":"sha1","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","path":"demo","declared_tools":[],"schedules":[],"setups":[{"name":"raum","label":"LABEL","input":{},"frei":[]}]}]}`
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"positiv", strings.Replace(base, "LABEL", strings.Repeat("ü", 40), 1), true},
		{"apps", strings.Replace(base, `"contract":4`, `"contract":4,"apps":[]`, 1), false},
		{"label41", strings.Replace(base, "LABEL", strings.Repeat("ü", 41), 1), false},
		{"labelLeer", strings.Replace(base, "LABEL", "", 1), false},
		{"labelSteuerzeichen", strings.Replace(base, "LABEL", `a\nb`, 1), false},
		{"unbekanntesSetup", strings.Replace(base, `"frei":[]`, `"frei":[],"token":"x"`, 1), false},
		{"pfad", strings.Replace(base, `"path":"demo"`, `"path":"../escape"`, 1), false},
		{"container", strings.Replace(base, `"path":"demo"`, `"container":{},"path":"demo"`, 1), false},
		{"grace0", strings.Replace(base, `"schedules":[]`, `"schedules":[],"grace_hours":0`, 1), false},
		{"grace73", strings.Replace(base, `"schedules":[]`, `"schedules":[],"grace_hours":73`, 1), false},
		{"freiTyp", strings.Replace(base, `"frei":[]`, `"frei":[{"name":"x","typ":"secret"}]`, 1), false},
		{"freiAuswahl41", strings.Replace(base, `"frei":[]`, `"frei":[{"name":"x","typ":"auswahl","optionen":["`+strings.Repeat("x", 41)+`"]}]`, 1), false},
		{"doppelteSetups", strings.Replace(base, `"setups":[`, `"setups":[],"setups":[`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Duplicate JSON must also be rejected by the actual manifest path.
			signed := SignedManifest{Manifest: json.RawMessage(tc.raw), Sig: "invalid"}
			pub, key, _ := ed25519.GenerateKey(nil)
			if can, err := canonical([]byte(tc.raw)); err == nil {
				signed.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(key, can))
			}
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(signed) })
			got, err := a.Manifest(context.Background())
			if err == nil {
				_, err = VerifyManifest(got, pub, "aaaaaaaaaaaaaaaa", 0)
			}
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestTestgeraeteSynchronisieren1298(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			m := l1Manifest()
			m.Contract = version
			m.TestDevice = true
			if version == 2 {
				m.Pins = []Pin{}
			}
			signed, c := l1Sign(t, encode(m), m.DeviceID)
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(signed) })
			got, err := a.Manifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := VerifyManifest(got, c.Pubkey, c.DeviceID, 0)
			if err != nil || !parsed.TestDevice {
				t.Fatal("Testgerät abgewiesen", err)
			}
			got.Manifest = bytes.Replace(got.Manifest, []byte(`"test_device":true`), []byte(`"test_device":false`), 1)
			if _, err = VerifyManifest(got, c.Pubkey, c.DeviceID, 0); err == nil {
				t.Fatal("unsignierte Änderung akzeptiert")
			}
		})
	}
}

func l1Order(t *testing.T, test bool, pin string) (Credential, TestOrder) {
	t.Helper()
	m := l1Manifest()
	m.TestDevice = test
	m.Pins[0].Pin = pin
	signed, c := l1Sign(t, encode(m), m.DeviceID)
	p := m.Pins[0]
	return c, TestOrder{Envelope: signed, Job: Job{JobID: "12345678-1234-4234-8234-123456789abc", Pin: pin, Commit: p.Commit, Path: p.Path, Algo: p.Algo, ManifestSeq: m.Seq}}
}

func TestScreenshotNurTestgeraet(t *testing.T) {
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0}
	for _, tc := range []struct {
		name  string
		test  bool
		pin   string
		valid bool
	}{
		{"testauftrag", true, "test_aaaaaaaa", true},
		{"ohneTestfreigabe", false, "test_aaaaaaaa", false},
		{"normalerPin", true, "demo", false},
		{"fremderTestpin", true, "test_bbbbbbbb", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, order := l1Order(t, tc.test, tc.pin)
			hits := 0
			a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
				hits++
				b, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.Path != "/api/device/v1/jobs/"+order.Job.JobID+"/screenshot" || r.Header.Get("Content-Type") != "image/png" || !bytes.Equal(b, png) {
					t.Error("Screenshot-Draht", r.URL.Path)
				}
				w.WriteHeader(204)
			})
			err := a.Screenshot(context.Background(), c, order, png)
			if (err == nil) != tc.valid || (hits == 1) != tc.valid {
				t.Fatal("Screenshot-Sperre", err, hits)
			}
		})
	}
}

func TestErgebnisNurTestauftrag(t *testing.T) {
	for _, tc := range []struct {
		name  string
		test  bool
		pin   string
		valid bool
	}{
		{"testauftrag", true, "test_aaaaaaaa", true},
		{"ohneTestfreigabe", false, "test_aaaaaaaa", false},
		{"normalerPin", true, "demo", false},
		{"fremderTestpin", true, "test_bbbbbbbb", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, order := l1Order(t, tc.test, tc.pin)
			hits := 0
			r := Result{JobID: order.Job.JobID, ManifestSeq: order.Job.ManifestSeq, Commit: order.Job.Commit, End: "ok", FinishedAt: "2026-11-01T12:01:00Z", Result: json.RawMessage(`{"wert":42}`)}
			a := l1API(t, func(w http.ResponseWriter, req *http.Request) {
				hits++
				var got Result
				if json.NewDecoder(req.Body).Decode(&got) != nil || !bytes.Equal(got.Result, r.Result) || req.URL.Path != "/api/device/v1/runs/result" {
					t.Error("result-Draht")
				}
				w.WriteHeader(204)
			})
			if err := a.Report(context.Background(), r); err == nil || hits != 0 {
				t.Fatal("zweiter Ergebnisweg umgeht Testprüfung")
			}
			err := a.ReportTest(context.Background(), r, c, order)
			if (err == nil) != tc.valid || (hits == 1) != tc.valid {
				t.Fatal("result-Sperre", err, hits)
			}
		})
	}
}

func TestTestadapterVerweigernFalscheBindung(t *testing.T) {
	for _, kind := range []string{"signatur", "geraet", "pin", "commit", "pfad", "algo", "seq", "jobID", "server", "png", "pngGross", "resultGross", "resultFremderJob", "resultOffline", "resultRunKey"} {
		t.Run(kind, func(t *testing.T) {
			c, order := l1Order(t, true, "test_aaaaaaaa")
			hits := 0
			png := []byte{137, 80, 78, 71, 13, 10, 26, 10}
			r := Result{JobID: order.Job.JobID, ManifestSeq: order.Job.ManifestSeq, Commit: order.Job.Commit, Result: json.RawMessage(`42`)}
			switch kind {
			case "signatur":
				order.Envelope.Sig = "invalid"
			case "geraet":
				c.DeviceID = "bbbbbbbbbbbbbbbb"
			case "pin":
				order.Job.Pin = "test_bbbbbbbb"
			case "commit":
				order.Job.Commit = strings.Repeat("b", 40)
			case "pfad":
				order.Job.Path = "other"
			case "algo":
				order.Job.Algo = "sha256"
			case "seq":
				order.Job.ManifestSeq++
			case "jobID":
				order.Job.JobID = "../escape"
			case "png":
				png = []byte("not png")
			case "pngGross":
				png = append(png, make([]byte, 4<<20)...)
			case "resultGross":
				r.Result = encode(strings.Repeat("x", 64<<10))
			case "resultFremderJob":
				r.JobID = "22345678-1234-4234-8234-123456789abc"
			case "resultOffline":
				r.RanOffline = true
			case "resultRunKey":
				r.RunKey = "setup/demo/raum/synthetic"
			}
			a := l1API(t, func(w http.ResponseWriter, req *http.Request) { hits++; w.WriteHeader(204) })
			if kind == "server" {
				if err := a.Server("rootless"); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if strings.HasPrefix(kind, "result") {
				err = a.ReportTest(context.Background(), r, c, order)
			} else {
				err = a.Screenshot(context.Background(), c, order, png)
			}
			if err == nil || hits != 0 {
				t.Fatal("verbotene Daten gesendet", err, hits)
			}
		})
	}
}

func TestSetupUndHaltAmHTTPAdapter(t *testing.T) {
	var routes []struct {
		Path     string
		Request  map[string]json.RawMessage
		Status   int
		Response map[string]json.RawMessage
	}
	b, err := os.ReadFile("testdata/golden-v3/contract3_routes.json")
	if err != nil || json.Unmarshal(b, &routes) != nil {
		t.Fatal("Routen-Golden", err)
	}
	m := l1Manifest()
	m.Pins[0].Pin = "demo"
	m.Pins[0].Setups = []Setup{{Name: "raum", Label: "Raumprofil", Input: json.RawMessage(`{"monitor":2}`), Frei: []FreeField{{Name: "titel", Typ: "text", MaxLaenge: 20}}}}
	signed, c := l1Sign(t, encode(m), m.DeviceID)
	nonces := map[string]bool{}
	hits := 0
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("JSON")
		}
		if r.Method != "POST" || r.Header.Get("X-Jnpt-Desktop-Contract") != "4" {
			t.Error("Vertrag/Verb")
		}
		if strings.HasSuffix(r.URL.Path, "runs/start") {
			if r.Header.Get("X-Jnpt-Manifest-Seq") != "42" || len(body) != 4 {
				t.Error("Sequenz/Körper", body)
			}
			for _, key := range []string{"pin", "setup_id", "frei"} {
				if !bytes.Equal(body[key], routes[0].Request[key]) {
					t.Error("Golden-Abweichung", key)
				}
			}
			var nonce string
			json.Unmarshal(body["nonce"], &nonce)
			if !regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`).MatchString(nonce) || nonces[nonce] {
				t.Error("Nonce", nonce)
			}
			nonces[nonce] = true
			out := routes[0].Response
			out["run_key"] = encode("setup/demo/raum/" + nonce)
			out["expires_at"] = encode("2027-01-01T00:00:00Z")
			json.NewEncoder(w).Encode(out)
		} else {
			if r.URL.Path != "/api/device/v1/halt" || string(body["contract"]) != "4" || len(body) != 3 {
				t.Error("Halt-Draht")
			}
			for _, key := range []string{"state", "until"} {
				if !bytes.Equal(body[key], routes[1].Request[key]) {
					t.Error("Halt-Golden", key)
				}
			}
			w.WriteHeader(204)
		}
	})
	for i := 0; i < 2; i++ {
		if _, err := a.StartSetup(context.Background(), signed, c, "demo", "raum", json.RawMessage(`{"titel":"Profil"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Halt(context.Background(), "pausiert", "2026-11-02T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if hits != 3 {
		t.Fatal(hits)
	}
	if _, err := a.StartSetup(context.Background(), signed, c, "demo", "fremd", json.RawMessage(`{}`)); err == nil {
		t.Fatal("Setup außerhalb Manifest")
	}
	for _, h := range [][2]string{{"fremd", ""}, {"arbeitet", "2026-11-02T12:00:00Z"}, {"pausiert", "bad"}} {
		if err := a.Halt(context.Background(), h[0], h[1]); err == nil {
			t.Fatal("ungültiger Halt")
		}
	}
	if hits != 3 {
		t.Fatal("negative Adapteraufrufe gelangten ins Netz", hits)
	}
}

func TestOfflineFristJePinUndUhrMarke(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	st, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	m := l1Manifest()
	two := 2
	m.Pins[0].GraceHours = &two
	p := m.Pins[0]
	if OfflineAllowed(p, m.GraceHours, now.Add(-2*time.Hour), now) {
		t.Fatal("Pinfrist durch Gerätefrist verlängert")
	}
	long := 48
	p.GraceHours = &long
	if !OfflineAllowed(p, m.GraceHours, now.Add(-25*time.Hour), now) {
		t.Fatal("lange Pinfrist gekappt")
	}
	p.GraceHours = &two
	if _, err = st.ObserveTime(now.Add(3 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	root := st.Root
	raw, err := os.ReadFile(filepath.Join(root, "clock"))
	if err != nil || !bytes.HasPrefix(raw, []byte("TEST-CIPHER:")) {
		t.Fatal("Uhrmarke ungeschützt", err)
	}
	st, err = OpenState(root, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	high, err := st.ObserveTime(now)
	if err != nil || !high.Equal(now.Add(3*time.Hour)) {
		t.Fatal("Uhrmarke nach Neustart zurückgesetzt", high, err)
	}
	e := Engine{State: st, Log: &Logger{Root: root}, syncErr: &net.DNSError{IsTimeout: true}, bundle: Bundle{Manifest: ManifestState{Envelope: SignedManifest{Manifest: encode(m)}, LastSync: now}}}
	if err = e.scheduled(ctx, p, Schedule{Name: "daily"}, now.Truncate(time.Minute)); err != nil {
		t.Fatal(err)
	}
	j := st.Journal()
	if len(j.Results) != 1 || j.Results[0].Detail != "offline" || !j.Results[0].RanOffline {
		t.Fatal("Frist nicht verweigert/Offline-Markierung fehlt", j)
	}
	log, err := os.ReadFile(filepath.Join(root, "runner.log"))
	if err != nil || !bytes.Contains(log, []byte("7 1\n")) {
		t.Fatal("fester Frist-Logcode fehlt", string(log), err)
	}
	if err = os.WriteFile(filepath.Join(root, "clock"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ObserveTime(now); err == nil {
		t.Fatal("kaputte Uhrmarke ignoriert")
	}
}

func TestOfflineNachmeldungAmAdapter(t *testing.T) {
	st, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	m := l1Manifest()
	p := m.Pins[0]
	hits := 0
	a := l1API(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		var got Result
		if json.NewDecoder(r.Body).Decode(&got) != nil || !got.RanOffline || got.Detail != "offline" {
			t.Error("Offline-Nachmeldung", got)
		}
		w.WriteHeader(204)
	})
	e := Engine{State: st, API: a, syncErr: &net.DNSError{IsTimeout: true}, bundle: Bundle{Manifest: ManifestState{Envelope: SignedManifest{Manifest: encode(m)}, LastSync: time.Now().Add(-25 * time.Hour)}}}
	if err = e.scheduled(context.Background(), p, Schedule{Name: "daily"}, time.Now().UTC().Truncate(time.Minute)); err != nil {
		t.Fatal(err)
	}
	e.deliverOne(context.Background())
	if hits != 1 || len(st.Journal().Results) != 0 {
		t.Fatal("Nachmeldung nicht bestätigt", hits)
	}
}
