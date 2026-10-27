package client

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVertrag2AmHTTPAdapterUndBeideStartwege(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	slot := time.Now().UTC().Truncate(time.Minute)
	p := containerPin()
	p.Container.SecretNames = []string{}
	schedule := Schedule{Name: "minute", Cron: "* * * * *", TZ: "UTC", Input: json.RawMessage(`{"synthetic":true}`)}
	p.Schedules = []Schedule{schedule}
	m := Manifest{Contract: 2, DeviceID: "aaaaaaaaaaaaaaaa", Seq: 42, IssuedAt: slot.Format(time.RFC3339), GraceHours: 24, Pins: []Pin{p}}
	raw, _ := canonical(encode(m))
	envelope := SignedManifest{raw, base64.StdEncoding.EncodeToString(ed25519.Sign(key, raw))}
	cfg := Config{Contract: 2, HeartbeatS: 60, PollHoldS: 25}
	cfg.Device.ID = m.DeviceID
	cfg.Device.Kind = "server"
	cfg.Device.Name = "synthetic"
	cfg.StartPolicy.Mode = "always"
	job := Job{JobID: "11111111-1111-4111-8111-111111111111", Pin: p.Pin, Commit: p.Commit, Algo: p.Algo, ManifestSeq: 42, Input: json.RawMessage(`{}`), StartBy: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}
	job.Limits.WallS = 30
	starts, reports := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Jnpt-Desktop-Contract") != "2" {
			t.Error("Header", r.Header)
		}
		if strings.HasSuffix(r.URL.Path, "pair") {
			if r.Header.Get("Authorization") != "" {
				t.Error("Pair mit Gerätetoken")
			}
		} else if r.Header.Get("Authorization") != "Bearer jnpt_synthetic" {
			t.Error("Authorization fehlt")
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/device/v1/pair":
			body, _ := io.ReadAll(r.Body)
			var v map[string]any
			json.Unmarshal(body, &v)
			if v["contract"] != float64(2) || v["container_runtime"] != "rootless" {
				t.Error(string(body))
			}
			json.NewEncoder(w).Encode(PairReply{m.DeviceID, "jnpt_synthetic", "2027-01-01T00:00:00Z", base64.StdEncoding.EncodeToString(pub)})
		case "/api/device/v1/config":
			json.NewEncoder(w).Encode(cfg)
		case "/api/device/v1/manifest":
			json.NewEncoder(w).Encode(envelope)
		case "/api/device/v1/heartbeat":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"container_runtime":"rootless"`) || !strings.Contains(string(body), `"contract":2`) {
				t.Error(string(body))
			}
			io.WriteString(w, `{"cancel":[],"manifest_seq_current":42}`)
		case "/api/device/v1/jobs/claim":
			json.NewEncoder(w).Encode(map[string]any{"job": job})
		case "/api/device/v1/jobs/" + job.JobID + "/start":
			starts++
			json.NewEncoder(w).Encode(StartReply{"SYNTHETIC_RUN_TOKEN", "2027-01-01T00:00:00Z", "/api/device/v1/mcp"})
		case "/api/device/v1/runs/result":
			var result Result
			b, _ := io.ReadAll(r.Body)
			if strict(b, &result) != nil || result.MountedSecretNames == nil || *result.MountedSecretNames == nil || result.End != "ok" {
				t.Error("Ergebnis", string(b))
			}
			reports++
			w.WriteHeader(204)
		default:
			t.Error("Unerlaubter Endpunkt (Paket oder werkzeugloser Start)", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	api, err := NewAPI(Trust{URL: srv.URL}, "jnpt_synthetic", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if err = api.Server("rootless"); err != nil {
		t.Fatal(err)
	}
	if _, err = api.Pair(context.Background(), m.DeviceID+".ABCD-EFGH-JKMN", "test"); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	d, f := fakeExecutor()
	engine := Engine{State: state, API: api, Credential: Credential{DeviceID: m.DeviceID, Pubkey: pub}, Container: d}
	if err = engine.Sync(context.Background()); err != nil || !engine.isReady() {
		t.Fatal("Sync/ready", err)
	}
	if _, err = api.Heartbeat(context.Background(), Heartbeat{2, true, "test", 42}); err != nil {
		t.Fatal(err)
	}
	j, err := api.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.adhoc(context.Background(), *j); err != nil {
		t.Fatal(err)
	}
	if err = engine.scheduled(context.Background(), p, schedule, slot); err != nil {
		t.Fatal(err)
	}
	if err = state.Deliver(func(r Result) error { return api.Report(context.Background(), r) }); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || reports != 2 || !f.has("run") {
		t.Fatal(starts, reports, f.calls)
	}
	// A signed contract downgrade must preserve the old synchronization time.
	old := engine.view()
	m.Contract = 1
	m.Seq++
	m.Pins = []Pin{}
	raw, _ = canonical(encode(m))
	envelope = SignedManifest{raw, base64.StdEncoding.EncodeToString(ed25519.Sign(key, raw))}
	if err = engine.Sync(context.Background()); err != ErrManifest || !engine.view().Manifest.LastSync.Equal(old.Manifest.LastSync) {
		t.Fatal("Vertragswechsel akzeptiert", err)
	}
}

func TestVertrag2VerweigertFremdeArtUndGeheimnisfelder(t *testing.T) {
	response := `{}`
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, response)
	}))
	defer srv.Close()
	api, _ := NewAPI(Trust{URL: srv.URL}, "jnpt_synthetic", nil)
	defer api.Close()
	for _, runtime := range []string{"", "unknown", "rootless\n", "ROOTLESS"} {
		if api.Server(runtime) == nil {
			t.Fatal("Runtime akzeptiert", runtime)
		}
	}
	api.Server("rootless")
	for _, kind := range []string{"dedicated", "workstation", "unknown"} {
		c := Config{Contract: 2, HeartbeatS: 60, PollHoldS: 25}
		c.Device.ID = "synthetic"
		c.Device.Kind = kind
		c.ExpectedAccount = "synthetic"
		c.StartPolicy.Mode = "always"
		response = string(encode(c))
		if _, e := api.Config(context.Background()); e == nil {
			t.Fatal("Geräteart akzeptiert", kind)
		}
	}
	for _, names := range []string{`null`, `["../bad"]`, `["x","x"]`, `["--privileged"]`} {
		response = `{"job":{"allowed_secret_names":` + names + `,"job_id":"11111111-1111-4111-8111-111111111111","pin":"probe","commit":"` + strings.Repeat("a", 64) + `","path":"","algo":"sha256","manifest_seq":42,"input":{},"limits":{"wall_s":1,"tool_calls":0},"start_by":"2027-01-01T00:00:00Z"}}`
		if _, e := api.Claim(context.Background()); e == nil {
			t.Fatal("Geheimnisnamen akzeptiert", names)
		}
	}
	before := hits
	if api.Report(context.Background(), Result{}) == nil {
		t.Fatal("Container-Ergebnis ohne Namen")
	}
	if _, e := api.Heartbeat(context.Background(), Heartbeat{1, true, "test", 0}); e == nil {
		t.Fatal("Heartbeat-Downgrade")
	}
	if hits != before {
		t.Fatal("Ungültiger Body versandt")
	}
}

func TestContainerHeartbeatAbbruchAmHTTPAdapter(t *testing.T) {
	d, f := fakeExecutor()
	f.wait = true
	f.started = make(chan struct{})
	st, _ := OpenState(t.TempDir(), testCipher{})
	engine := Engine{State: st, Container: d}
	result := make(chan Result, 1)
	go func() {
		result <- engine.executeContainer(context.Background(), Result{JobID: "probe", Commit: containerPin().Commit}, containerPin(), nil, nil, "synthetic", 30)
	}()
	<-f.started
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"cancel":["probe"],"manifest_seq_current":42}`)
	}))
	defer srv.Close()
	api, _ := NewAPI(Trust{URL: srv.URL}, "jnpt_synthetic", nil)
	defer api.Close()
	api.Server("rootless")
	h, err := api.Heartbeat(context.Background(), Heartbeat{2, true, "test", 42})
	if err != nil {
		t.Fatal(err)
	}
	engine.applyCancellations(h)
	r := <-result
	if r.End != "interrupted" || !f.has("kill") || !f.has("rm") {
		t.Fatal("Heartbeat-Abbruch erreichte Container nicht", r)
	}
}
