package client

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
)

// #1391: Ein Pin ohne Umgebung sperrt das Manifest nicht mehr. Fehler auf Manifest-Ebene verwerfen weiter alles
// (Signatur, Seq, Gerät, Contract, ObserveTime, Save, ErrRevoked, Abbruch, offline); jeder andere Fehler im
// Pin-Schritt trifft nur diesen Pin. Ein byte-gleiches Manifest ist kein Fehler (SEC E2), ein offline gescheiterter
// Paketabruf lässt Stand und letzten Sync unverändert (SEC E1).

type syncBed struct {
	t       *testing.T
	e       *Engine
	st      *State
	pub     ed25519.PublicKey
	key     ed25519.PrivateKey
	srv     *httptest.Server
	signed  atomic.Pointer[SignedManifest]
	device  atomic.Value // string: Geräte-ID der Konfiguration
	pkgs    map[string][]byte
	pkgMode atomic.Value // string: "" (liefert), "offline", "revoked", "500", "cancel"
	onPkg   atomic.Pointer[func()]
	fetched atomic.Int32
	spawns  atomic.Int32
	logDir  string
	demo    App
	zweite  App
	dritte  App
	gut     Pin
}

func neueApp(name string, body string) (App, []byte) {
	path := "apps/" + name
	commit, raw := testfixture.PackageAt(path, map[string][]byte{"index.html": appHTML(body)}, "")
	return App{Name: name, Commit: commit, Path: path}, raw
}

func newSyncBed(t *testing.T) *syncBed {
	t.Helper()
	b := &syncBed{t: t, pkgs: map[string][]byte{}, logDir: t.TempDir()}
	b.pub, b.key, _ = ed25519.GenerateKey(nil)
	b.device.Store("own")
	b.pkgMode.Store("")
	var raw []byte
	b.demo, raw = buildApp(t, goodApp(), "")
	b.pkgs[b.demo.Commit] = raw
	b.zweite, raw = neueApp("zweite", "<p>zwei</p>")
	b.pkgs[b.zweite.Commit] = raw
	b.dritte, raw = neueApp("dritte", "<p>drei</p>")
	b.pkgs[b.dritte.Commit] = raw
	b.srv = httptest.NewServer(http.HandlerFunc(b.handle))
	t.Cleanup(b.srv.Close)
	b.st, _ = OpenState(t.TempDir(), testCipher{})
	api, err := NewAPI(Trust{URL: b.srv.URL}, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	b.e = &Engine{State: b.st, API: api, Credential: Credential{DeviceID: "own", Pubkey: b.pub}, Log: &Logger{Root: b.logDir},
		Session: func() (Session, error) { return Session{Active: true, Account: "own"}, nil },
		Spawn:   b.spawn, ChildEnv: []string{"SystemRoot=C:\\Windows"}}
	b.gut = b.pinMitPaket("gut")
	b.seedEnv(b.gut, nil)
	return b
}

func (b *syncBed) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/config"):
		cfg := Config{Contract: WindowsContract, ExpectedAccount: "own", HeartbeatS: 60, PollHoldS: 25}
		cfg.Device.ID, cfg.Device.Kind = b.device.Load().(string), "workstation"
		cfg.StartPolicy.Mode = "always"
		_ = json.NewEncoder(w).Encode(cfg)
	case strings.HasSuffix(r.URL.Path, "/manifest"):
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false) // wie ein Kern, der die Hülle ohne HTML-Escapes ausliefert
		_ = enc.Encode(b.signed.Load())
	case strings.HasPrefix(r.URL.Path, "/api/device/v1/packages/"):
		b.fetched.Add(1)
		switch b.pkgMode.Load().(string) {
		case "offline":
			c, _, _ := w.(http.Hijacker).Hijack()
			_ = c.Close()
			return
		case "revoked":
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"class":"revoked"}}`))
			return
		case "cancel": // der Abbruch trifft den Läufer, bevor die Antwort bei ihm ankommt; sie selbst ist gültig
			if f := b.onPkg.Load(); f != nil {
				(*f)()
			}
		case "500": // nur für den kaputten Pin, die Apps kommen durch
			if !strings.HasSuffix(r.URL.Path, strings.Repeat("b", 64)) {
				break
			}
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":{"class":"internal","message":"Fehler."}}`))
			return
		}
		raw, ok := b.pkgs[strings.TrimPrefix(r.URL.Path, "/api/device/v1/packages/")]
		if !ok {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"class":"not_found","message":"Das Paket wurde nicht gefunden."}}`))
			return
		}
		w.Header().Set("Content-Type", "application/vnd.jnpt.gitobjects.v1")
		_, _ = w.Write(raw)
	default:
		b.t.Error("unerwarteter Weg", r.URL.Path)
	}
}

func (b *syncBed) spawn(context.Context, Launch) (Process, error) {
	b.spawns.Add(1)
	r, w := io.Pipe()
	go func() { _ = WriteFrame(w, map[string]any{"type": "exited", "code": 0}) }()
	return &fakeProc{r: r, w: w}, nil
}

func desktopYaml(name string) string {
	return "name: " + name + "\ndescription: test\nentry: main.py\ninput_schema: {type: object}\ntools: []\ntimeout_s: 10\npython: python.zip\n"
}

// pinMitPaket baut einen Pin mit gültigem Gitpaket (Manifest und Skript). Prepare scheitert daran, weil python.zip
// nicht in der Allowlist steht; die Umgebung kommt nur über seedEnv in den Cache.
func (b *syncBed) pinMitPaket(name string) Pin {
	commit, raw := testfixture.PackageAt(name, map[string][]byte{"jnpt-desktop.yaml": []byte(desktopYaml(name)), "main.py": []byte("pass"), "python.zip": []byte("evil")}, "")
	b.pkgs[commit] = raw
	return b.pin(name, commit)
}

func (b *syncBed) pin(name, commit string) Pin {
	return Pin{Pin: name, Commit: commit, Path: name, Algo: "sha256", DeclaredTools: []string{},
		Schedules: []Schedule{{Name: "daily", Cron: "* * * * *", TZ: "UTC", Input: json.RawMessage(`{}`)}}}
}

// seedEnv legt eine versiegelte Umgebung im Cache ab (wie nach einem früheren Prepare). Ein Paket mit echtem Python
// braucht die kuratierte ZIP; Prepare selbst deckt TestWindowsPythonEchterStartUndManipulation ab.
func (b *syncBed) seedEnv(p Pin, mutate func(*Environment)) Environment {
	b.t.Helper()
	dir := envKey(p)
	envDir := filepath.Join(b.st.Root, "envs", dir)
	if err := os.MkdirAll(filepath.Join(envDir, "runtime"), 0o700); err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "runtime", "python.exe"), []byte("x"), 0o600); err != nil {
		b.t.Fatal(err)
	}
	seal, err := Snapshot(envDir)
	if err != nil {
		b.t.Fatal(err)
	}
	if raw, ok := b.pkgs[p.Commit]; ok {
		if err = os.MkdirAll(filepath.Join(b.st.Root, "packages"), 0o700); err != nil {
			b.t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(b.st.Root, "packages", dir), raw, 0o600); err != nil {
			b.t.Fatal(err)
		}
	}
	env := Environment{Origin: p, Dir: dir, Files: seal, Entry: "main.py", Timeout: 10, Tools: []string{}}
	if mutate != nil {
		mutate(&env)
	}
	if err = b.st.Save("env-"+dir, env); err != nil {
		b.t.Fatal(err)
	}
	return env
}

func (b *syncBed) manifest(seq uint64, apps []App, pins ...Pin) Manifest {
	if apps == nil {
		apps = []App{}
	}
	if pins == nil {
		pins = []Pin{}
	}
	return Manifest{Apps: &apps, Contract: WindowsContract, DeviceID: "own", Seq: seq, IssuedAt: "2026-11-17T10:00:00Z", GraceHours: 24, Pins: pins}
}

func (b *syncBed) serve(m Manifest) { b.serveRaw(encode(m), b.key) }

func (b *syncBed) serveRaw(raw []byte, key ed25519.PrivateKey) {
	can, _ := canonical(raw)
	b.signed.Store(&SignedManifest{Manifest: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, can))})
}

// sync arbeitet wie die Sync-Punkte in Run: das Ergebnis landet in syncErr.
func (b *syncBed) sync() error {
	b.t.Helper()
	err := b.e.Sync(context.Background())
	b.e.syncErr = err
	return err
}

func (b *syncBed) storedSeq() uint64 {
	b.t.Helper()
	m, err := ParseManifest(b.e.view().Manifest.Envelope.Manifest)
	if err != nil {
		return 0
	}
	return m.Seq
}

func (b *syncBed) persistedSeq() uint64 {
	b.t.Helper()
	var c Bundle
	if err := b.st.Load("manifest", &c); err != nil {
		return 0
	}
	m, err := ParseManifest(c.Manifest.Envelope.Manifest)
	if err != nil {
		return 0
	}
	return m.Seq
}

func (b *syncBed) log() string {
	raw, _ := os.ReadFile(filepath.Join(b.logDir, "runner.log"))
	return string(raw)
}

func (b *syncBed) exists(parts ...string) bool {
	_, err := os.Lstat(filepath.Join(append([]string{b.st.Root}, parts...)...))
	return err == nil
}

// slot führt den geplanten Lauf des Pins aus und liefert sein journalisiertes Ergebnis.
func (b *syncBed) slot(p Pin) Result {
	b.t.Helper()
	if err := b.e.scheduled(context.Background(), p, p.Schedules[0], time.Now().UTC().Truncate(time.Minute)); err != nil {
		b.t.Fatal(err)
	}
	for _, r := range b.st.Journal().Results {
		if strings.HasPrefix(r.RunKey, p.Pin+"/") {
			return r
		}
	}
	b.t.Fatalf("kein Ergebnis für %s", p.Pin)
	return Result{}
}

func (b *syncBed) hatUmgebung(p Pin) bool {
	_, ok := b.e.view().Environments[envKey(p)]
	return ok
}

// Ausgangslage N=1: Apps demo und zweite, der gute Pin mit Setup S, Umgebung aus dem Cache.
func syncBedNachN(t *testing.T) *syncBed {
	t.Helper()
	b := newSyncBed(t)
	gut := b.gut
	gut.Setups = []Setup{{Name: "s", Label: "S", Input: json.RawMessage(`{}`), Frei: []FreeField{}}}
	b.serve(b.manifest(1, []App{b.demo, b.zweite}, gut))
	if err := b.sync(); err != nil {
		t.Fatal("Ausgangslage:", err)
	}
	if !b.exists("apps", "zweite", b.zweite.Commit) || !b.hatUmgebung(b.gut) {
		t.Fatal("Ausgangslage: App oder Umgebung fehlt")
	}
	return b
}

// kaputterPin liefert einen Pin, dessen Umgebung in diesem Sync nicht entsteht, samt erwarteter Fehlerklasse im Log.
type kaputterPin struct {
	name   string
	klasse string // 1 hash, 2 runtime, 3 manifest, 4 sonst
	bau    func(b *syncBed) Pin
}

func kaputtePins() []kaputterPin {
	yaml := func(name, extra string) string { return desktopYaml(name) + extra }
	paket := func(b *syncBed, name string, files map[string][]byte) Pin {
		commit, raw := testfixture.PackageAt(name, files, "")
		b.pkgs[commit] = raw
		return b.pin(name, commit)
	}
	return []kaputterPin{
		{"Paket mit falschen Git-Objekten", "1", func(b *syncBed) Pin {
			p := b.pin("kaputt", strings.Repeat("b", 64))
			b.pkgs[p.Commit] = []byte("kein Paket")
			return p
		}},
		{"Python-ZIP nicht in der Allowlist", "2", func(b *syncBed) Pin {
			return paket(b, "kaputt", map[string][]byte{"jnpt-desktop.yaml": []byte(yaml("kaputt", "")), "main.py": []byte("pass"), "python.zip": []byte("evil")})
		}},
		{"kaputte yaml", "3", func(b *syncBed) Pin {
			return paket(b, "kaputt", map[string][]byte{"jnpt-desktop.yaml": []byte(yaml("kaputt", "unbekannt: 1\n")), "main.py": []byte("pass"), "python.zip": []byte("evil")})
		}},
		{"Paketabruf 404", "4", func(b *syncBed) Pin { return b.pin("kaputt", strings.Repeat("b", 64)) }},
		{"Paketabruf 500", "4", func(b *syncBed) Pin {
			b.pkgMode.Store("500")
			return b.pin("kaputt", strings.Repeat("b", 64))
		}},
		{"Cache-Umgebung eines anderen Pins (Origin)", "3", func(b *syncBed) Pin {
			p := b.pin("kaputt", strings.Repeat("b", 64))
			b.seedEnv(p, func(e *Environment) { e.Origin.Pin = "anders" })
			return p
		}},
		{"Cache-Umgebung mit anderem Algorithmus (Origin)", "3", func(b *syncBed) Pin {
			p := b.pin("kaputt", strings.Repeat("b", 64))
			b.seedEnv(p, func(e *Environment) { e.Origin.Algo = "sha1" })
			return p
		}},
		{"Cache-Umgebung mit anderen Werkzeugen", "3", func(b *syncBed) Pin {
			p := b.pin("kaputt", strings.Repeat("b", 64))
			b.seedEnv(p, func(e *Environment) { e.Tools = []string{"fremd"} })
			return p
		}},
		{"Cache-Umgebung manipuliert", "1", func(b *syncBed) Pin {
			p := b.pin("kaputt", strings.Repeat("b", 64))
			env := b.seedEnv(p, nil)
			if err := os.WriteFile(filepath.Join(b.st.Root, "envs", env.Dir, "runtime", "python.exe"), []byte("manipuliert"), 0o600); err != nil {
				b.t.Fatal(err)
			}
			return p
		}},
	}
}

// Erster roter Test (Plan c-6943318410, Befund aus dem Code): Ein unverändertes Manifest bei Presync darf den
// Lauf nicht ablehnen. Der Kern behält die Seq bei gleichem Inhalt, VerifyManifest verlangt aber Seq > minSeq.
func TestPresyncUnveraendertesManifestLehntDenLaufNichtAb(t *testing.T) {
	b := newSyncBed(t)
	b.serve(b.manifest(1, []App{b.demo}, b.gut))
	if err := b.sync(); err != nil {
		t.Fatal("erster Abgleich:", err)
	}
	if err := b.sync(); err != nil {
		t.Errorf("Presync mit unverändertem Manifest scheitert: %v", err)
	}
	r := b.slot(b.gut)
	if r.End == "refused" || r.End != "ok" || b.spawns.Load() != 1 {
		t.Fatalf("Lauf nach Presync: %+v, Starts %d", r, b.spawns.Load())
	}
}

// Der Kernfall: Ein kaputter Pin sperrt weder Entzug noch neue Apps noch den Lauf des guten Pins.
func TestKaputterPinTrifftNurDiesenPin(t *testing.T) {
	for _, kp := range kaputtePins() {
		t.Run(kp.name, func(t *testing.T) {
			b := syncBedNachN(t)
			kaputt := kp.bau(b)
			b.serve(b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt))
			if err := b.sync(); err != nil {
				t.Fatalf("Abgleich scheitert am Pin: %v", err)
			}
			if b.storedSeq() != 2 || b.persistedSeq() != 2 {
				t.Fatalf("Seq im Speicher %d, auf der Platte %d", b.storedSeq(), b.persistedSeq())
			}
			if !b.exists("apps", "dritte", b.dritte.Commit) || b.exists("apps", "zweite") {
				t.Error("neue App fehlt oder entzogene App blieb liegen")
			}
			m, _ := ParseManifest(b.e.view().Manifest.Envelope.Manifest)
			if len(m.Pins) != 2 || len(m.Pins[0].Setups) != 0 {
				t.Errorf("Setup S steht noch im gespeicherten Manifest: %+v", m.Pins)
			}
			if !b.hatUmgebung(b.gut) || b.hatUmgebung(kaputt) {
				t.Fatalf("Umgebungen: gut %v, kaputt %v", b.hatUmgebung(b.gut), b.hatUmgebung(kaputt))
			}
			if r := b.slot(b.gut); r.End != "ok" || b.spawns.Load() != 1 {
				t.Fatalf("guter Pin: %+v, Starts %d", r, b.spawns.Load())
			}
			if r := b.slot(kaputt); r.End != "refused" || r.Detail != "runtime" || b.spawns.Load() != 1 {
				t.Fatalf("kaputter Pin: %+v, Starts %d", r, b.spawns.Load())
			}
			if got := b.log(); got != "2 "+kp.klasse+"\n" {
				t.Fatalf("runner.log = %q, erwartet nur den Code 2 mit Klasse %s", got, kp.klasse)
			}
		})
	}
}

// Unbekannte Fehlerklasse im Pin-Schritt: Der kaputte Pin trifft auch dann nur sich selbst, wenn er in N schon stand.
func TestKaputterPinImGespeichertenStandBleibtOhneUmgebung(t *testing.T) {
	b := newSyncBed(t)
	kaputt := b.pin("kaputt", strings.Repeat("b", 64))
	b.serve(b.manifest(1, []App{b.demo}, b.gut, kaputt))
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	if b.hatUmgebung(kaputt) || !b.hatUmgebung(b.gut) {
		t.Fatal("Umgebungen nach dem ersten Abgleich")
	}
	// byte-gleich, Paket weiter nicht da: wieder versucht, wieder nur dieser Pin, ein Logeintrag je Versuch.
	before := b.fetched.Load()
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	if b.fetched.Load() != before+1 {
		t.Fatalf("kaputter Pin nicht genau einmal neu versucht: %d -> %d", before, b.fetched.Load())
	}
	if b.log() != "2 4\n2 4\n" {
		t.Fatalf("runner.log = %q", b.log())
	}
	if r := b.slot(kaputt); r.End != "refused" || r.Detail != "runtime" || b.spawns.Load() != 0 {
		t.Fatalf("%+v, Starts %d", r, b.spawns.Load())
	}
}

// Byte-gleiches Manifest mit jetzt gutem Paket: Der Pin wird bereit, der gute Pin wird nicht neu geholt.
func TestByteGleichesManifestMitJetztGutemPaketMachtDenPinBereit(t *testing.T) {
	b := newSyncBed(t)
	spaet := b.pinMitPaket("spaet")
	b.serve(b.manifest(1, []App{b.demo}, b.gut, spaet))
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	if b.hatUmgebung(spaet) {
		t.Fatal("Umgebung ohne Paket")
	}
	if r := b.slot(spaet); r.End != "refused" || b.spawns.Load() != 0 {
		t.Fatalf("%+v", r)
	}
	// Das Paket ist jetzt da; der Cache steht für ein erfolgreiches Prepare (echtes Python braucht die ZIP).
	b.seedEnv(spaet, nil)
	before := b.fetched.Load()
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	if !b.hatUmgebung(spaet) || !b.hatUmgebung(b.gut) {
		t.Fatal("Pin wurde nicht bereit")
	}
	if b.fetched.Load() != before {
		t.Fatalf("Paket trotz Cache neu geholt: %d -> %d", before, b.fetched.Load())
	}
	if err := b.e.scheduled(context.Background(), spaet, Schedule{Name: "nochmal", Input: json.RawMessage(`{}`)}, time.Now().UTC().Truncate(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if b.spawns.Load() != 1 {
		t.Fatalf("Starts %d", b.spawns.Load())
	}
}

// Wie nach einem Neustart: Der Stand kommt aus dem Speicher; json.Marshal hat die Hülle dort mit HTML-Escapes
// neu serialisiert (aus & wird die Escape-Folge u0026), der Kern liefert sie ohne. Dasselbe Manifest mit & im Text ist trotzdem kein Fehler:
// verglichen wird, was die Signatur deckt (kanonische Bytes), nicht die rohe Serialisierung.
func TestByteGleichesManifestNachNeustartIstKeinFehler(t *testing.T) {
	b := newSyncBed(t)
	gut := b.gut
	gut.Setups = []Setup{{Name: "s", Label: "A & B <1>", Input: json.RawMessage(`{}`), Frei: []FreeField{}}}
	raw := strings.NewReplacer(`\u0026`, "&", `\u003c`, "<", `\u003e`, ">").Replace(string(encode(b.manifest(1, []App{b.demo}, gut))))
	if !strings.Contains(raw, "A & B <1>") {
		t.Fatal("Fixture trägt keine unescapten Zeichen")
	}
	b.serveRaw([]byte(raw), b.key)
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	var cached Bundle
	if err := b.st.Load("manifest", &cached); err != nil {
		t.Fatal(err)
	}
	b.e.mu.Lock()
	b.e.bundle = cached
	b.e.mu.Unlock()
	if err := b.sync(); err != nil {
		t.Fatalf("nach Neustart: %v", err)
	}
}

// SEC E2: Nur die Seq-Prüfung ist gelockert. Alles andere läuft wie im normalen Weg, und jede andere Hülle mit
// gleicher oder kleinerer Seq bleibt ErrManifest. Der Stand und der letzte Sync bleiben.
func TestGleicheSeqAndereHuelleBleibtErrManifest(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func(b *syncBed)
		want error
	}{
		{"anderer Inhalt, gültig signiert", func(b *syncBed) { b.serve(b.manifest(1, []App{b.demo, b.zweite}, b.gut)) }, ErrManifest},
		{"gleicher Inhalt, andere Reihenfolge der Apps", func(b *syncBed) { b.serve(b.manifest(1, []App{b.zweite, b.demo}, b.gut)) }, ErrManifest},
		{"gleiche Bytes, Signatur mit einer Byte-Abweichung", func(b *syncBed) {
			s := *b.signed.Load()
			sig := []byte(s.Sig)
			if sig[3] == 'A' {
				sig[3] = 'B'
			} else {
				sig[3] = 'A'
			}
			b.signed.Store(&SignedManifest{Manifest: s.Manifest, Sig: string(sig)})
		}, ErrManifest},
		{"gleiche Bytes, Signatur von fremdem Schlüssel", func(b *syncBed) {
			_, other, _ := ed25519.GenerateKey(nil)
			b.serveRaw(b.signed.Load().Manifest, other)
		}, ErrManifest},
		{"ältere Seq", func(b *syncBed) { b.serve(b.manifest(0, []App{b.demo}, b.gut)) }, ErrManifest},
		{"gleiche Bytes bei anderer Geräte-ID", func(b *syncBed) {
			b.device.Store("fremd")
			b.e.Credential.DeviceID = "fremd"
		}, ErrManifest},
		{"gleiche Bytes bei anderem Vertrag (Server-Läufer)", func(b *syncBed) { b.e.Container = struct{ ContainerExecutor }{} }, ErrManifest},
		{"gleiche Bytes bei defekter Uhrmarke", func(b *syncBed) {
			if err := os.WriteFile(filepath.Join(b.st.Root, "clock"), []byte("defekt"), 0o600); err != nil {
				b.t.Fatal(err)
			}
		}, nil},
		{"gleiche Bytes, Speichern scheitert", func(b *syncBed) {
			p := filepath.Join(b.st.Root, "manifest")
			if err := os.Remove(p); err != nil {
				b.t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(p, "im-weg"), 0o700); err != nil {
				b.t.Fatal(err)
			}
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syncBedNachN(t)
			if _, err := b.st.ObserveTime(time.Now()); err != nil {
				t.Fatal(err)
			}
			vorher := b.e.view().Manifest
			tc.do(b)
			err := b.sync()
			if err == nil || (tc.want != nil && err != tc.want) {
				t.Fatalf("Fehler = %v, erwartet %v", err, tc.want)
			}
			nachher := b.e.view().Manifest
			if string(nachher.Envelope.Manifest) != string(vorher.Envelope.Manifest) || !nachher.LastSync.Equal(vorher.LastSync) {
				t.Fatal("Stand oder letzter Sync verändert")
			}
			if !b.exists("apps", "zweite") {
				t.Fatal("App abgeräumt")
			}
		})
	}
}

// spaeterAbbruch meldet Err() = Canceled, sobald abgebrochen ist, schließt Done() aber nie.
type spaeterAbbruch struct {
	context.Context
	abgebrochen atomic.Bool
}

func (c *spaeterAbbruch) Err() error {
	if c.abgebrochen.Load() {
		return context.Canceled
	}
	return nil
}

// Fehler auf Manifest-Ebene verwerfen alles und behalten N, auch wenn das Manifest N+1 einen Entzug und einen
// kaputten Pin enthält.
func TestFehlerAufManifestEbeneVerwirftAlles(t *testing.T) {
	kaputt := func(b *syncBed) Pin { return b.pin("kaputt", strings.Repeat("b", 64)) }
	for _, tc := range []struct {
		name  string
		do    func(b *syncBed) error
		check func(t *testing.T, err error)
	}{
		{"Signatur von fremdem Schlüssel", func(b *syncBed) error {
			_, other, _ := ed25519.GenerateKey(nil)
			b.serveRaw(encode(b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt(b))), other)
			return b.sync()
		}, nil},
		{"ältere Seq", func(b *syncBed) error { b.serve(b.manifest(0, []App{b.demo}, b.gut, kaputt(b))); return b.sync() }, nil},
		{"fremdes Gerät im Manifest", func(b *syncBed) error {
			m := b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt(b))
			m.DeviceID = "fremd"
			b.serve(m)
			return b.sync()
		}, nil},
		{"fremdes Gerät in der Konfiguration", func(b *syncBed) error {
			b.serve(b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt(b)))
			b.device.Store("fremd")
			return b.sync()
		}, nil},
		{"Vertrag passt nicht", func(b *syncBed) error {
			b.serve(b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt(b)))
			b.e.Container = struct{ ContainerExecutor }{}
			return b.sync()
		}, nil},
		{"Uhrmarke defekt (ObserveTime)", func(b *syncBed) error {
			b.serve(b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt(b)))
			if err := os.WriteFile(filepath.Join(b.st.Root, "clock"), []byte("defekt"), 0o600); err != nil {
				b.t.Fatal(err)
			}
			return b.sync()
		}, nil},
		{"Speichern des Manifests scheitert", func(b *syncBed) error {
			b.serve(b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt(b)))
			p := filepath.Join(b.st.Root, "manifest")
			if err := os.Remove(p); err != nil {
				b.t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(p, "im-weg"), 0o700); err != nil {
				b.t.Fatal(err)
			}
			return b.sync()
		}, nil},
		{"Widerruf beim Paketabruf", func(b *syncBed) error {
			b.serve(b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt(b)))
			b.pkgMode.Store("revoked")
			return b.sync()
		}, func(t *testing.T, err error) {
			if err != ErrRevoked {
				t.Fatalf("Fehler = %v, erwartet ErrRevoked", err)
			}
		}},
		{"offline beim Paketabruf", func(b *syncBed) error {
			b.serve(b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt(b)))
			b.pkgMode.Store("offline")
			return b.sync()
		}, func(t *testing.T, err error) {
			if !IsOffline(err) {
				t.Fatalf("Fehler = %v, erwartet offline", err)
			}
		}},
		// Der Pin scheitert in Prepare (kein Netzfehler), während der Kontext abgebrochen wird: Der Abbruch ist kein
		// Fehler dieses Pins. Der Kontext meldet Err() erst, wenn der Kern das Paket ausliefert; Done() bleibt offen,
		// damit der Abruf selbst durchläuft und der Kontextzweig allein den Ausschlag gibt.
		{"Abbruch während Prepare", func(b *syncBed) error {
			pin := b.pinMitPaket("kaputt")
			b.serve(b.manifest(2, []App{b.demo, b.dritte}, b.gut, pin))
			b.pkgMode.Store("cancel")
			ctx := &spaeterAbbruch{Context: context.Background()}
			stop := func() { ctx.abgebrochen.Store(true) }
			b.onPkg.Store(&stop)
			b.e.syncErr = b.e.Sync(ctx)
			return b.e.syncErr
		}, func(t *testing.T, err error) {
			if IsOffline(err) || err == context.Canceled {
				t.Fatalf("Fehler = %v: erwartet der Fehler des Pins (Prepare), nicht Netz oder Abbruch", err)
			}
		}},
		{"abgebrochener Kontext", func(b *syncBed) error {
			b.serve(b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt(b)))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			b.e.syncErr = b.e.Sync(ctx)
			return b.e.syncErr
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syncBedNachN(t)
			vorher := b.e.view().Manifest
			err := tc.do(b)
			if err == nil {
				t.Fatal("Abgleich hätte scheitern müssen")
			}
			if tc.check != nil {
				tc.check(t, err)
			}
			if b.storedSeq() != 1 || (!strings.Contains(tc.name, "Speichern") && b.persistedSeq() != 1) || !b.e.view().Manifest.LastSync.Equal(vorher.LastSync) {
				t.Fatalf("N nicht behalten: Seq %d/%d", b.storedSeq(), b.persistedSeq())
			}
			if !b.exists("apps", "zweite", b.zweite.Commit) || b.exists("apps", "dritte") {
				t.Error("Entzug oder neue App trotz verworfenem Abgleich")
			}
			m, _ := ParseManifest(b.e.view().Manifest.Envelope.Manifest)
			if len(m.Pins) != 1 || len(m.Pins[0].Setups) != 1 {
				t.Errorf("Setup S oder Pin verändert: %+v", m.Pins)
			}
		})
	}
}

// SEC E1: Offline beim Paketabruf lässt N und den letzten Sync unverändert, damit die Offline-Kulanz weiter ab dem
// letzten echten Kontakt zählt. Der Entzug wirkt, sobald ein Sync mit Netz gelingt.
func TestOfflinePaketabrufVerzoegertNurDenEntzug(t *testing.T) {
	b := syncBedNachN(t)
	vorher := b.e.view().Manifest.LastSync
	kaputt := b.pin("kaputt", strings.Repeat("b", 64))
	b.serve(b.manifest(2, []App{b.demo, b.dritte}, b.gut, kaputt))

	b.pkgMode.Store("offline")
	err := b.sync()
	if !IsOffline(err) {
		t.Fatalf("Fehler = %v, erwartet offline", err)
	}
	if b.storedSeq() != 1 || !b.e.view().Manifest.LastSync.Equal(vorher) {
		t.Fatalf("N oder letzter Sync verändert: Seq %d, LastSync %v -> %v", b.storedSeq(), vorher, b.e.view().Manifest.LastSync)
	}
	var disk Bundle
	if err = b.st.Load("manifest", &disk); err != nil || !disk.Manifest.LastSync.Equal(vorher) {
		t.Fatalf("Platte: %v, %v", err, disk.Manifest.LastSync)
	}
	// Der Heartbeat sieht weiter die höhere Seq (stored < current) und löst den Abgleich erneut aus.
	if b.storedSeq() >= 2 {
		t.Fatal("gespeicherte Seq darf nicht fortschreiten")
	}
	// Ohne Netz laufen die alten, versiegelten Umgebungen weiter.
	if r := b.slot(b.gut); r.End != "ok" || b.spawns.Load() != 1 {
		t.Fatalf("alter Pin offline: %+v, Starts %d", r, b.spawns.Load())
	}

	b.pkgMode.Store("")
	if err = b.sync(); err != nil {
		t.Fatalf("nächster Abgleich mit Netz: %v", err)
	}
	if b.storedSeq() != 2 || b.exists("apps", "zweite") || !b.exists("apps", "dritte", b.dritte.Commit) {
		t.Fatalf("Entzug wirkt nicht: Seq %d", b.storedSeq())
	}
	if !b.e.view().Manifest.LastSync.After(vorher) {
		t.Fatal("letzter Sync nicht fortgeschrieben")
	}
}

// Kein Logeintrag nach Widerruf (der Widerruf löscht den Zustand) und keiner beim Abbruch des Prozesses.
func TestWiderrufUndAbbruchSchreibenKeinenSyncFehlerCode(t *testing.T) {
	b := syncBedNachN(t)
	b.serve(b.manifest(2, []App{b.demo}, b.gut, b.pin("kaputt", strings.Repeat("b", 64))))
	b.pkgMode.Store("revoked")
	if err := b.sync(); err != ErrRevoked {
		t.Fatal(err)
	}
	b.pkgMode.Store("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.e.Sync(ctx); err == nil {
		t.Fatal("Abbruch ohne Fehler")
	}
	if b.log() != "" {
		t.Fatalf("runner.log = %q", b.log())
	}
}

// Fehler auf Manifest-Ebene schreiben ebenfalls den Code 2, mit Klasse: 3 manifest, 4 sonst (offline, Speichern).
func TestSyncFehlerAufManifestEbeneSchreibtCodeMitKlasse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		do     func(b *syncBed)
		klasse string
	}{
		{"ältere Seq", func(b *syncBed) { b.serve(b.manifest(0, []App{b.demo}, b.gut)) }, "3"},
		{"offline", func(b *syncBed) {
			b.serve(b.manifest(2, []App{b.demo}, b.gut, b.pin("kaputt", strings.Repeat("b", 64))))
			b.pkgMode.Store("offline")
		}, "4"},
		{"Uhrmarke defekt", func(b *syncBed) {
			b.serve(b.manifest(2, []App{b.demo}, b.gut))
			_ = os.WriteFile(filepath.Join(b.st.Root, "clock"), []byte("defekt"), 0o600)
		}, "4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syncBedNachN(t)
			tc.do(b)
			if err := b.sync(); err == nil {
				t.Fatal("Abgleich hätte scheitern müssen")
			}
			if b.log() != "2 "+tc.klasse+"\n" {
				t.Fatalf("runner.log = %q", b.log())
			}
		})
	}
}
