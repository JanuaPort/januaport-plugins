package client

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
)

// #1342: Nach jedem erfolgreich geprüften Manifest-Abgleich räumt der Läufer entzogene Apps ab. Alle Tests laufen
// über Engine.Sync gegen einen Fake-Kern; sie kennen keine Hilfsfunktion der Umsetzung.

type pruneBed struct {
	t       *testing.T
	e       *Engine
	st      *State
	pub     ed25519.PublicKey
	key     ed25519.PrivateKey
	srv     *httptest.Server
	signed  atomic.Pointer[SignedManifest]
	pkgs    map[string][]byte
	fetched atomic.Int32
	logDir  string
	demo    App
	zweite  App
	seq     uint64
}

func newPruneBed(t *testing.T) *pruneBed {
	t.Helper()
	b := &pruneBed{t: t, pkgs: map[string][]byte{}, logDir: t.TempDir()}
	b.pub, b.key, _ = ed25519.GenerateKey(nil)
	var raw []byte
	b.demo, raw = buildApp(t, goodApp(), "")
	b.pkgs[b.demo.Commit] = raw
	commit, raw := testfixture.PackageAt("apps/zweite", map[string][]byte{"index.html": appHTML("<p>zwei</p>")}, "")
	b.zweite = App{Name: "zweite", Commit: commit, Path: "apps/zweite"}
	b.pkgs[commit] = raw
	cfg := Config{Contract: WindowsContract, ExpectedAccount: "own", HeartbeatS: 60, PollHoldS: 25}
	cfg.Device.ID, cfg.Device.Kind = "own", "workstation"
	cfg.StartPolicy.Mode = "always"
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/config"):
			_ = json.NewEncoder(w).Encode(cfg)
		case strings.HasSuffix(r.URL.Path, "/manifest"):
			_ = json.NewEncoder(w).Encode(b.signed.Load())
		case strings.HasPrefix(r.URL.Path, "/api/device/v1/packages/"):
			raw, ok := b.pkgs[strings.TrimPrefix(r.URL.Path, "/api/device/v1/packages/")]
			if !ok {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":{"class":"not_found","message":"Der Auftrag oder das Paket wurde nicht gefunden."}}`))
				return
			}
			b.fetched.Add(1)
			w.Header().Set("Content-Type", "application/vnd.jnpt.gitobjects.v1")
			_, _ = w.Write(raw)
		default:
			t.Error("unerwarteter Weg", r.URL.Path)
		}
	}))
	t.Cleanup(b.srv.Close)
	b.st, _ = OpenState(t.TempDir(), testCipher{})
	api, err := NewAPI(Trust{URL: b.srv.URL}, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	b.e = &Engine{State: b.st, API: api, Credential: Credential{DeviceID: "own", Pubkey: b.pub}, Log: &Logger{Root: b.logDir}}
	return b
}

func (b *pruneBed) manifest(seq uint64, apps ...App) Manifest {
	if apps == nil {
		apps = []App{}
	}
	return Manifest{Apps: &apps, Contract: WindowsContract, DeviceID: "own", Seq: seq, IssuedAt: "2026-11-04T10:00:00Z", GraceHours: 24, Pins: []Pin{}}
}

func (b *pruneBed) serve(raw []byte, key ed25519.PrivateKey) {
	can, _ := canonical(raw)
	b.signed.Store(&SignedManifest{Manifest: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, can))})
}

// sync liefert ein gültig signiertes Manifest mit den Apps und gleicht ab.
func (b *pruneBed) sync(seq uint64, apps ...App) error {
	b.t.Helper()
	b.seq = seq
	b.serve(encode(b.manifest(seq, apps...)), b.key)
	return b.e.Sync(context.Background())
}

func (b *pruneBed) path(parts ...string) string {
	return filepath.Join(append([]string{b.st.Root}, parts...)...)
}

func (b *pruneBed) exists(parts ...string) bool {
	_, err := os.Lstat(b.path(parts...))
	return err == nil
}

// webview legt ein Profil wie das Fenster an.
func (b *pruneBed) webview(name string) {
	b.t.Helper()
	dir := b.path("webview", name, "Default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Cookies"), []byte("profil"), 0o600); err != nil {
		b.t.Fatal(err)
	}
}

func (b *pruneBed) seals(name string) []string {
	m, _ := filepath.Glob(b.path("app-" + name + "-*"))
	return m
}

func (b *pruneBed) log() string {
	raw, _ := os.ReadFile(filepath.Join(b.logDir, "runner.log"))
	return string(raw)
}

// zweiAppsAbgelegt: Zustand nach dem ersten Abgleich mit beiden Apps und je einem Profil.
func zweiAppsAbgelegt(t *testing.T) *pruneBed {
	t.Helper()
	b := newPruneBed(t)
	if err := b.sync(1, b.demo, b.zweite); err != nil {
		t.Fatal(err)
	}
	b.webview("demo")
	b.webview("zweite")
	if !b.exists("apps", "zweite", b.zweite.Commit) || len(b.seals("zweite")) != 1 {
		t.Fatal("Ausgangslage: zweite App nicht abgelegt")
	}
	return b
}

func TestEntzugRaeumtAppProfilUndSiegelAb(t *testing.T) {
	b := zweiAppsAbgelegt(t)
	alt := b.path("apps", "demo", strings.Repeat("e", 64)) // alter Commit einer weiter zugewiesenen App: nicht im Umfang
	if err := os.MkdirAll(alt, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := b.sync(2, b.demo); err != nil {
		t.Fatal(err)
	}
	for _, gone := range [][]string{{"apps", "zweite"}, {"webview", "zweite"}} {
		if b.exists(gone...) {
			t.Errorf("%v blieb liegen", gone)
		}
	}
	if len(b.seals("zweite")) != 0 {
		t.Errorf("Siegel blieb liegen: %v", b.seals("zweite"))
	}
	for _, kept := range [][]string{{"apps", "demo", b.demo.Commit, "index.html"}, {"webview", "demo", "Default", "Cookies"}, {"credential"}, {"manifest"}} {
		if kept[0] == "credential" {
			continue // im Fake-Bett nicht angelegt
		}
		if !b.exists(kept...) {
			t.Errorf("%v fehlt", kept)
		}
	}
	if len(b.seals("demo")) != 1 || !b.exists("apps", "demo", strings.Repeat("e", 64)) {
		t.Error("zugewiesene App oder ihre alten Commits angefasst")
	}
	r, _ := NewReader(b.st.Root, testCipher{})
	if _, err := r.OpenApp(b.demo); err != nil {
		t.Errorf("zugewiesene App nicht mehr offen: %v", err)
	}
	if strings.Contains(b.log(), "10 ") {
		t.Errorf("Störung gemeldet: %q", b.log())
	}
}

func TestRueckkehrLaedtDieAppNeuMitLeeremProfil(t *testing.T) {
	b := zweiAppsAbgelegt(t)
	if err := b.sync(2, b.demo); err != nil {
		t.Fatal(err)
	}
	before := b.fetched.Load()
	if err := b.sync(3, b.demo, b.zweite); err != nil {
		t.Fatal(err)
	}
	if b.fetched.Load() != before+1 {
		t.Fatalf("Paket nicht genau einmal neu geholt: %d -> %d", before, b.fetched.Load())
	}
	r, _ := NewReader(b.st.Root, testCipher{})
	if _, err := r.OpenApp(b.zweite); err != nil {
		t.Fatalf("zurückgekehrte App nicht offen: %v", err)
	}
	if b.exists("webview", "zweite") {
		t.Fatal("altes Profil kam mit zurück")
	}
}

// SEC (1): Pfade entstehen nur aus lokal aufgezählten, gültigen Namen. Alles andere bleibt stehen.
func TestAufraeumenLoeschtNurGueltigeNamen(t *testing.T) {
	b := zweiAppsAbgelegt(t)
	ungueltig := []string{".prepare-abc", "Fremd", "mit-strich", "name.ext", strings.Repeat("a", 49), "ä", "..x"}
	angelegt := map[string]bool{}
	for _, area := range []string{"apps", "webview"} {
		for _, name := range ungueltig {
			if err := os.MkdirAll(b.path(area, name), 0o700); err != nil {
				continue // auf diesem Dateisystem nicht anlegbar
			}
			if err := os.WriteFile(b.path(area, name, "datei"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			angelegt[filepath.Join(area, name)] = true
		}
	}
	if len(angelegt) < 12 {
		t.Fatalf("zu wenige Fremdeinträge angelegt: %v", angelegt)
	}
	fremdeDateien := []string{"app-Fremd-" + b.zweite.Commit, "app-zweite-kein-commit", "app-" + strings.Repeat("a", 49) + "-" + b.zweite.Commit, "app-zweite", "journal-fremd"}
	for _, name := range fremdeDateien {
		if err := os.WriteFile(b.path(name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(b.path("envs", "zweite"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Gültiger Name, nie zugewiesen, nur lokal vorhanden: Das Manifest entscheidet, er geht mit.
	verwaist := "app-verwaist-" + strings.Repeat("b", 64)
	for _, p := range [][]string{{"apps", "verwaist", "x"}, {"webview", "verwaist", "x"}} {
		if err := os.MkdirAll(b.path(p...), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(b.path(verwaist), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.sync(2, b.demo); err != nil {
		t.Fatal(err)
	}
	for _, gone := range [][]string{{"apps", "zweite"}, {"webview", "zweite"}, {"apps", "verwaist"}, {"webview", "verwaist"}, {verwaist}} {
		if b.exists(gone...) {
			t.Errorf("%v blieb liegen", gone)
		}
	}
	for rel := range angelegt {
		if !b.exists(rel, "datei") {
			t.Errorf("ungültiger Eintrag %s angefasst", rel)
		}
	}
	for _, name := range fremdeDateien {
		if !b.exists(name) {
			t.Errorf("fremde Datei %s gelöscht", name)
		}
	}
	if !b.exists("envs", "zweite") {
		t.Error("fremder Bereich envs angefasst")
	}
}

// SEC (2): Ein Verweis (Symlink, unter Windows Junction) unter apps\ oder webview\ wird nur als Verweis entfernt.
func TestVerweisUnterAppsUndWebviewWirdNichtVerfolgt(t *testing.T) {
	b := zweiAppsAbgelegt(t)
	draussen := t.TempDir()
	canary := filepath.Join(draussen, "ziel", "wichtig.txt")
	if err := os.MkdirAll(filepath.Dir(canary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canary, []byte("bleibt"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Die echten Ordner der entzogenen App weichen Verweisen auf ein Ziel außerhalb.
	for _, area := range []string{"apps", "webview"} {
		if err := os.RemoveAll(b.path(area, "zweite")); err != nil {
			t.Fatal(err)
		}
		linkDir(t, b.path(area, "zweite"), filepath.Dir(canary))
	}
	if err := b.sync(2, b.demo); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(canary); err != nil || string(got) != "bleibt" {
		t.Fatalf("Ziel außerhalb angetastet: %q %v", got, err)
	}
	for _, area := range []string{"apps", "webview"} {
		if b.exists(area, "zweite") {
			t.Errorf("Verweis %s/zweite blieb liegen", area)
		}
	}
	if len(b.seals("zweite")) != 0 {
		t.Error("Siegel blieb liegen")
	}
}

// SEC (2), Wurzel: Ist apps\ oder webview\ selbst ein Verweis, wird nichts darin gelöscht und der Verweis bleibt.
func TestVerweisAlsAppsOderWebviewWurzelBleibtUnberuehrt(t *testing.T) {
	for _, area := range []string{"apps", "webview"} {
		t.Run(area, func(t *testing.T) {
			b := newPruneBed(t)
			if err := b.sync(1, b.demo, b.zweite); err != nil {
				t.Fatal(err)
			}
			draussen := t.TempDir()
			canary := filepath.Join(draussen, "zweite", "wichtig.txt")
			if err := os.MkdirAll(filepath.Dir(canary), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(canary, []byte("bleibt"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(b.path(area)); err != nil {
				t.Fatal(err)
			}
			linkDir(t, b.path(area), draussen)
			if err := b.sync(2, b.demo); err != nil {
				t.Fatalf("Sync darf an der Anomalie nicht scheitern: %v", err)
			}
			if got, err := os.ReadFile(canary); err != nil || string(got) != "bleibt" {
				t.Fatalf("Ziel des Wurzelverweises angetastet: %q %v", got, err)
			}
			if !b.exists(area) {
				t.Error("Wurzelverweis entfernt")
			}
			if !strings.Contains(b.log(), "10 2\n") {
				t.Errorf("Anomalie nicht als fester Code vermerkt: %q", b.log())
			}
		})
	}
}

// SEC (3): Nach gescheitertem oder ungeprüftem Abgleich wird nichts gelöscht.
func TestKeinLoeschenBeiGescheitertemOderUngeprueftemAbgleich(t *testing.T) {
	pinOhnePaket := Pin{Pin: "demo", Algo: "sha256", Commit: strings.Repeat("c", 64), Path: "demo", DeclaredTools: []string{}, Schedules: []Schedule{}}
	for _, tc := range []struct {
		name string
		do   func(b *pruneBed) error
	}{
		{"Kern nicht erreichbar", func(b *pruneBed) error { b.srv.Close(); return b.e.Sync(context.Background()) }},
		{"Signatur von fremdem Schlüssel", func(b *pruneBed) error {
			_, other, _ := ed25519.GenerateKey(nil)
			b.serve(encode(b.manifest(2, b.demo)), other)
			return b.e.Sync(context.Background())
		}},
		{"Manifest nicht lesbar", func(b *pruneBed) error {
			b.serve([]byte(`{"unbekannt":1}`), b.key)
			return b.e.Sync(context.Background())
		}},
		{"fremdes Gerät", func(b *pruneBed) error {
			m := b.manifest(2, b.demo)
			m.DeviceID = "fremd"
			b.serve(encode(m), b.key)
			return b.e.Sync(context.Background())
		}},
		{"gleiche Sequenz, kein Fortschritt", func(b *pruneBed) error { return b.sync(1, b.demo) }},
		{"ältere Sequenz", func(b *pruneBed) error { return b.sync(0, b.demo) }},
		{"alter Vertrag ohne apps", func(b *pruneBed) error {
			m := b.manifest(2)
			m.Apps, m.Contract = nil, 4
			b.serve(encode(m), b.key)
			return b.e.Sync(context.Background())
		}},
		{"bösartiger App-Name im Manifest", func(b *pruneBed) error {
			evil := App{Name: "../envs", Commit: b.zweite.Commit, Path: "apps/../envs"}
			b.serve(encode(b.manifest(2, evil)), b.key)
			return b.e.Sync(context.Background())
		}},
		{"Name mit Trenner im Manifest", func(b *pruneBed) error {
			evil := App{Name: "a/b", Commit: b.zweite.Commit, Path: "apps/a/b"}
			b.serve(encode(b.manifest(2, evil)), b.key)
			return b.e.Sync(context.Background())
		}},
		{"Abgleich scheitert nach der Signaturprüfung (Pin-Paket fehlt)", func(b *pruneBed) error {
			m := b.manifest(2, b.demo)
			m.Pins = []Pin{pinOhnePaket}
			b.serve(encode(m), b.key)
			return b.e.Sync(context.Background())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := zweiAppsAbgelegt(t)
			if err := tc.do(b); err == nil {
				t.Fatal("Abgleich hätte scheitern müssen")
			}
			for _, kept := range [][]string{{"apps", "zweite", b.zweite.Commit, "index.html"}, {"webview", "zweite", "Default", "Cookies"}, {"apps", "demo"}, {"webview", "demo"}} {
				if !b.exists(kept...) {
					t.Errorf("%v gelöscht", kept)
				}
			}
			if len(b.seals("zweite")) != 1 || len(b.seals("demo")) != 1 {
				t.Error("Siegel gelöscht")
			}
		})
	}
}

// Ein gesperrter Ordner bleibt liegen, Abgleich und Läufe gehen weiter, der nächste Abgleich löscht ihn.
// Windows: offener Dateigriff ohne FILE_SHARE_DELETE; sonst: Rechteentzug (siehe sperreOrdner). Beides ist eine
// Simulation, kein laufender WebView2-Prozess.
func TestGesperrterOrdnerBleibtUndWirdBeimNaechstenAbgleichGeloescht(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sperre       []string // was gesperrt wird
		wantWeg      [][]string
		wantBleibt   [][]string
		siegelBleibt bool
	}{
		{"Profil gesperrt", []string{"webview", "zweite"}, [][]string{{"apps", "zweite"}}, [][]string{{"webview", "zweite", "Default", "Cookies"}}, false},
		{"Ablage gesperrt, Siegel bleibt mit", []string{"apps", "zweite"}, [][]string{{"webview", "zweite"}}, [][]string{{"apps", "zweite"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := zweiAppsAbgelegt(t)
			freigeben := sperreOrdner(t, b.path(tc.sperre...))
			if err := b.sync(2, b.demo); err != nil {
				t.Fatalf("gesperrter Ordner stoppte den Abgleich: %v", err)
			}
			for _, p := range tc.wantWeg {
				if b.exists(p...) {
					t.Errorf("%v nicht gelöscht", p)
				}
			}
			for _, p := range tc.wantBleibt {
				if !b.exists(p...) {
					t.Errorf("%v trotz Sperre gelöscht", p)
				}
			}
			if got := len(b.seals("zweite")) == 1; got != tc.siegelBleibt {
				t.Errorf("Siegel vorhanden: %v, erwartet %v", got, tc.siegelBleibt)
			}
			if b.log() != "10 2\n" {
				t.Errorf("Log: %q, erwartet genau den festen Code 10 2", b.log())
			}
			r, _ := NewReader(b.st.Root, testCipher{})
			if _, err := r.OpenApp(b.demo); err != nil {
				t.Errorf("zugewiesene App nach Teilfehler nicht offen: %v", err)
			}
			freigeben()
			if err := b.sync(3, b.demo); err != nil {
				t.Fatal(err)
			}
			if b.exists("apps", "zweite") || b.exists("webview", "zweite") || len(b.seals("zweite")) != 0 {
				t.Error("nach Freigabe nicht gelöscht")
			}
			if b.log() != "10 2\n" {
				t.Errorf("zweiter Abgleich meldete erneut: %q", b.log())
			}
		})
	}
}

// Schreibvorgänge laufen unter der Zustandssperre wie AppStore.Prepare; der Läufer ruft kein os.RemoveAll auf.
func TestAufraeumenLaeuftUnterZustandssperreOhneRemoveAll(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "appprune.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var uses, removeAll, removeTreeCalls int
	for _, d := range f.Decls {
		ast.Inspect(d, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "Use":
					uses++
				case "RemoveAll":
					removeAll++
				}
			}
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "removeTree" {
					removeTreeCalls++
				}
			}
			return true
		})
	}
	if uses == 0 || removeAll != 0 || removeTreeCalls == 0 {
		t.Fatalf("State.Use: %d, os.RemoveAll: %d, removeTree: %d", uses, removeAll, removeTreeCalls)
	}
}

// Hauptfall (#1342): Der Entzug kommt mit einem neuen Manifest, der Sync räumt sofort ab, das Fenster ist dann noch
// offen. Der Takt wiederholt das Abräumen gegen das gespeicherte Manifest, ohne neue Sequenz und ohne Netz.
func TestTaktRaeumtGesperrtenOrdnerOhneNeueSequenzAb(t *testing.T) {
	b := zweiAppsAbgelegt(t)
	freigeben := sperreOrdner(t, b.path("webview", "zweite"))
	if err := b.sync(2, b.demo); err != nil {
		t.Fatal(err)
	}
	b.srv.Close() // der Takt braucht kein Netz
	b.e.pruneStored()
	b.e.pruneStored()
	if !b.exists("webview", "zweite", "Default", "Cookies") {
		t.Fatal("trotz Sperre gelöscht")
	}
	if b.log() != "10 2\n" {
		t.Fatalf("Log: %q, erwartet genau eine Zeile (kein Spam bei Wiederholung)", b.log())
	}
	freigeben()
	b.e.pruneStored()
	if b.exists("webview", "zweite") || b.exists("apps", "zweite") || len(b.seals("zweite")) != 0 {
		t.Fatal("nach Freigabe vom Takt nicht gelöscht")
	}
	if b.log() != "10 2\n" {
		t.Fatalf("Log nach Erfolg: %q", b.log())
	}
	if !b.exists("webview", "demo", "Default", "Cookies") || !b.exists("apps", "demo", b.demo.Commit, "index.html") {
		t.Fatal("zugewiesene App angefasst")
	}
}

// Ohne prüfbares gespeichertes Manifest löscht der Takt nichts.
func TestTaktLoeschtNurGegenPruefbaresGespeichertesManifest(t *testing.T) {
	_, fremderKey, _ := ed25519.GenerateKey(nil)
	for _, tc := range []struct {
		name  string
		praep func(b *pruneBed)
	}{
		{"kein Manifest gespeichert", func(b *pruneBed) { b.e.bundle = Bundle{} }},
		{"Signatur von fremdem Schlüssel", func(b *pruneBed) {
			raw := encode(b.manifest(2, b.demo))
			can, _ := canonical(raw)
			b.e.bundle.Manifest.Envelope = SignedManifest{Manifest: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(fremderKey, can))}
		}},
		{"Manifest nicht lesbar", func(b *pruneBed) {
			raw := []byte(`{"unbekannt":1}`)
			can, _ := canonical(raw)
			b.e.bundle.Manifest.Envelope = SignedManifest{Manifest: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(b.key, can))}
		}},
		{"anderes Gerät", func(b *pruneBed) {
			m := b.manifest(2, b.demo)
			m.DeviceID = "fremd"
			raw := encode(m)
			can, _ := canonical(raw)
			b.e.bundle.Manifest.Envelope = SignedManifest{Manifest: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(b.key, can))}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := zweiAppsAbgelegt(t)
			tc.praep(b)
			b.e.pruneStored()
			if !b.exists("apps", "zweite", b.zweite.Commit, "index.html") || !b.exists("webview", "zweite", "Default", "Cookies") || len(b.seals("zweite")) != 1 {
				t.Fatal("ohne prüfbares Manifest gelöscht")
			}
		})
	}
}

// Das gespeicherte Manifest ist dasselbe, das der Sync zuletzt geprüft hat: Ist nichts zu löschen, passiert nichts
// und es wird nichts geloggt.
func TestTaktOhneEntzugLaesstAllesUndLogtNichts(t *testing.T) {
	b := zweiAppsAbgelegt(t)
	b.e.pruneStored()
	if !b.exists("apps", "zweite", b.zweite.Commit, "index.html") || !b.exists("webview", "demo") || b.log() != "" {
		t.Fatalf("Takt ohne Entzug veränderte etwas: log %q", b.log())
	}
}

// Der Takt ist im Heartbeat-Zweig von Run verdrahtet.
func TestRunRuftDenAufraeumtaktAuf(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "engine.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Run" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "pruneStored" {
				found = true
			}
			return true
		})
	}
	if !found {
		t.Fatal("Run ruft pruneStored nicht auf")
	}
}
