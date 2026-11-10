package client

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// #1343: Ist das lokale Manifest älter als der Auftrag, holt der Läufer es genau einmal nach, liest den Stand neu
// und prüft dann mit denselben Regeln. Der Lauf selbst wird hier nicht ausgeführt: Der Start-Aufruf an den Kern
// beweist, dass die Prüfung bestanden ist; er antwortet 500 und endet als `internal`.

const nachholenJob = "11111111-1111-4111-8111-111111111111"

type nachholenFall struct {
	name       string
	localSeq   uint64
	localPin   bool
	jobSeq     uint64
	serverSeq  uint64 // 0: Der Manifest-Weg antwortet 500
	serverPin  bool
	ablaufBeim bool // StartBy läuft während des Nachholens ab
	wantRefuse bool // refused/manifest
	wantSyncs  int32
	wantStarts int32
}

func nachholenBed(t *testing.T, tc nachholenFall) (*Engine, *atomic.Int32, *atomic.Int32, Job) {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(nil)
	p := Pin{Pin: "demo", Algo: "sha256", Commit: strings.Repeat("a", 64), Path: "demo", DeclaredTools: []string{}, Schedules: []Schedule{}}
	manifest := func(seq uint64, withPin bool) SignedManifest {
		m := Manifest{Apps: &[]App{}, Contract: WindowsContract, DeviceID: "own", Seq: seq, IssuedAt: time.Now().UTC().Format(time.RFC3339), GraceHours: 24, Pins: []Pin{}}
		if withPin {
			m.Pins = []Pin{p}
		}
		raw := encode(m)
		can, _ := canonical(raw)
		return SignedManifest{Manifest: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, can))}
	}
	st, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	dir := envKey(p)
	if err = os.MkdirAll(filepath.Join(st.Root, "envs", dir), 0o700); err != nil {
		t.Fatal(err)
	}
	env := Environment{Origin: p, Dir: dir, Files: []FileSeal{}, Tools: []string{}}
	cfg := Config{Contract: WindowsContract, ExpectedAccount: "own", HeartbeatS: 60, PollHoldS: 25}
	cfg.Device.ID, cfg.Device.Kind = "own", "workstation"
	cfg.StartPolicy.Mode = "always"
	job := Job{JobID: nachholenJob, Pin: p.Pin, Commit: p.Commit, Path: p.Path, Algo: p.Algo, ManifestSeq: tc.jobSeq, Input: json.RawMessage(`{}`),
		StartBy: time.Now().Add(1500 * time.Millisecond).UTC().Format(time.RFC3339)}
	deadline, _ := time.Parse(time.RFC3339, job.StartBy)
	var syncs, starts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/device/v1/config":
			syncs.Add(1)
			_ = json.NewEncoder(w).Encode(cfg)
		case "/api/device/v1/manifest":
			if tc.ablaufBeim {
				for time.Now().Before(deadline.Add(50 * time.Millisecond)) {
					time.Sleep(20 * time.Millisecond)
				}
			}
			if tc.serverSeq == 0 {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"error":{"class":"internal","message":"Interner Fehler."}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(manifest(tc.serverSeq, tc.serverPin))
		case "/api/device/v1/jobs/" + nachholenJob + "/start":
			starts.Add(1)
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":{"class":"internal","message":"Interner Fehler."}}`))
		default:
			t.Error("unerwarteter Weg", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	api, err := NewAPI(Trust{URL: srv.URL}, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	e := &Engine{State: st, API: api, Credential: Credential{DeviceID: "own", Pubkey: pub}, Config: cfg,
		bundle: Bundle{ManifestState{manifest(tc.localSeq, tc.localPin), time.Now().UTC()}, cfg, map[string]Environment{dir: env}}}
	return e, &syncs, &starts, job
}

func TestAdhocHoltVeraltetesManifestEinmalNach(t *testing.T) {
	for _, tc := range []nachholenFall{
		{name: "Sequenz zu alt, Nachholen bringt das Manifest", localSeq: 1, localPin: true, jobSeq: 2, serverSeq: 2, serverPin: true, wantSyncs: 1, wantStarts: 1},
		{name: "Pin steht erst im nachgeholten Manifest", localSeq: 1, localPin: false, jobSeq: 2, serverSeq: 2, serverPin: true, wantSyncs: 1, wantStarts: 1},
		{name: "Nachholen scheitert", localSeq: 1, localPin: true, jobSeq: 2, serverSeq: 0, wantRefuse: true, wantSyncs: 1},
		{name: "Nachholen liefert keine neuere Sequenz", localSeq: 1, localPin: true, jobSeq: 2, serverSeq: 1, serverPin: true, wantRefuse: true, wantSyncs: 1},
		{name: "Nachholen bringt noch immer eine zu alte Sequenz, kein zweiter Versuch", localSeq: 1, localPin: true, jobSeq: 3, serverSeq: 2, serverPin: true, wantRefuse: true, wantSyncs: 1},
		{name: "Sequenz neu genug, Pin fehlt: kein Nachholen", localSeq: 2, localPin: false, jobSeq: 2, serverSeq: 3, serverPin: true, wantRefuse: true, wantSyncs: 0},
		{name: "Sequenz neu genug, Pin da: kein Nachholen", localSeq: 2, localPin: true, jobSeq: 2, serverSeq: 3, serverPin: true, wantSyncs: 0, wantStarts: 1},
		{name: "Lokal neuer als der Auftrag: kein Nachholen", localSeq: 5, localPin: true, jobSeq: 2, serverSeq: 6, serverPin: true, wantSyncs: 0, wantStarts: 1},
		{name: "StartBy läuft beim Nachholen ab", localSeq: 1, localPin: true, jobSeq: 2, serverSeq: 2, serverPin: true, ablaufBeim: true, wantSyncs: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, syncs, starts, job := nachholenBed(t, tc)
			if err := e.adhoc(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			if got := syncs.Load(); got != tc.wantSyncs {
				t.Fatalf("Sync-Aufrufe: %d, erwartet %d", got, tc.wantSyncs)
			}
			if got := starts.Load(); got != tc.wantStarts {
				t.Fatalf("Start-Aufrufe: %d, erwartet %d", got, tc.wantStarts)
			}
			results := e.State.Journal().Results
			if len(results) != 1 {
				t.Fatalf("Ergebnisse: %+v", results)
			}
			refused := results[0].End == "refused" && results[0].Detail == "manifest"
			if refused != tc.wantRefuse {
				t.Fatalf("Ergebnis %s/%s, refused/manifest erwartet: %v", results[0].End, results[0].Detail, tc.wantRefuse)
			}
			if !tc.wantRefuse && results[0].End == "refused" {
				t.Fatalf("unerwartet abgelehnt: %+v", results[0])
			}
		})
	}
}
