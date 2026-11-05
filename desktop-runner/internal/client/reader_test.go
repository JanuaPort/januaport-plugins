package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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

// Testwert aus Teilstrings: kein Token-Muster im Quelltext.
func syntheticToken() string { return "jn" + "pt_" + strings.Repeat("0123456789abcdef", 2) }

type readerBed struct {
	root string
	st   *State
	cred Credential
	pub  ed25519.PublicKey
	key  ed25519.PrivateKey
	app  App
	raw  []byte
}

// newReaderBed schreibt Zustand wie der Läufer: Zugangsdaten, signiertes Manifest mit einer App, App-Ablage.
func newReaderBed(t *testing.T, testDevice bool) *readerBed {
	t.Helper()
	b := &readerBed{root: t.TempDir()}
	var err error
	if b.st, err = OpenState(b.root, testCipher{}); err != nil {
		t.Fatal(err)
	}
	b.pub, b.key, _ = ed25519.GenerateKey(nil)
	b.cred = Credential{Trust: Trust{URL: "http://127.0.0.1:1"}, DeviceID: "aaaaaaaaaaaaaaaa", Token: syntheticToken(), ExpiresAt: "2099-01-01T00:00:00Z", Pubkey: b.pub}
	if err = b.st.Save("credential", b.cred); err != nil {
		t.Fatal(err)
	}
	b.app, b.raw = buildApp(t, goodApp(), "")
	if err = NewAppStore(b.st).Prepare(b.raw, b.app); err != nil {
		t.Fatal(err)
	}
	b.saveManifest(t, Manifest{Apps: &[]App{b.app}, TestDevice: testDevice, Contract: WindowsContract, DeviceID: b.cred.DeviceID, Seq: 7, IssuedAt: "2026-11-04T10:00:00Z", GraceHours: 24, Pins: []Pin{}})
	return b
}

func (b *readerBed) sign(m Manifest) SignedManifest {
	raw := encode(m)
	can, _ := canonical(raw)
	return SignedManifest{Manifest: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(b.key, can))}
}

func (b *readerBed) saveManifest(t *testing.T, m Manifest) {
	t.Helper()
	bundle := Bundle{Manifest: ManifestState{Envelope: b.sign(m), LastSync: time.Now().UTC()}, Environments: map[string]Environment{}}
	if err := b.st.Save("manifest", bundle); err != nil {
		t.Fatal(err)
	}
}

func TestReaderLiestWasDerLaeuferSchreibt(t *testing.T) {
	b := newReaderBed(t, true)
	r, err := NewReader(b.root, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := r.Credential()
	if err != nil || cred.Token != b.cred.Token || cred.DeviceID != b.cred.DeviceID || cred.Trust.URL != b.cred.Trust.URL || !bytes.Equal(cred.Pubkey, b.pub) {
		t.Fatalf("Zugangsdaten: %+v %v", cred, err)
	}
	view, err := r.Apps()
	if err != nil || len(view.Apps) != 1 || view.Apps[0] != b.app || !view.TestDevice {
		t.Fatalf("Apps: %+v %v", view, err)
	}
	dir, err := r.OpenApp(b.app)
	if err != nil || dir != filepath.Join(b.root, "apps", "demo", b.app.Commit) {
		t.Fatalf("OpenApp: %q %v", dir, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Fatal(err)
	}
}

func TestReaderTestgeraetNurAusSigniertemManifest(t *testing.T) {
	b := newReaderBed(t, false)
	r, _ := NewReader(b.root, testCipher{})
	view, err := r.Apps()
	if err != nil || view.TestDevice {
		t.Fatalf("%+v %v", view, err)
	}
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		info, _ := d.Info()
		rel, _ := filepath.Rel(root, p)
		body := ""
		if !d.IsDir() {
			raw, _ := os.ReadFile(p)
			body = sum(raw)
		}
		out[rel] = body + "|" + info.ModTime().String() + "|" + info.Mode().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// B3: Das Bedienfeld schreibt nichts in den Zustand. Der Leser ändert weder Dateien noch Zeiten noch Rechte.
func TestReaderSchreibtNichts(t *testing.T) {
	b := newReaderBed(t, true)
	before := snapshotTree(t, b.root)
	r, _ := NewReader(b.root, testCipher{})
	for i := 0; i < 3; i++ {
		_, _ = r.Credential()
		_, _ = r.Apps()
		_, _ = r.OpenApp(b.app)
		_, _ = r.OpenApp(App{Name: "unbekannt", Commit: b.app.Commit, Path: "apps/unbekannt"})
	}
	after := snapshotTree(t, b.root)
	if len(before) != len(after) {
		t.Fatalf("Dateimenge geändert: %d -> %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s verändert", k)
		}
	}
}

func TestReaderFehlerfaelle(t *testing.T) {
	t.Run("ohneZugangsdaten", func(t *testing.T) {
		r, _ := NewReader(t.TempDir(), testCipher{})
		if _, err := r.Credential(); err == nil {
			t.Fatal("Zugangsdaten aus dem Nichts")
		}
		if _, err := r.Apps(); err == nil {
			t.Fatal("Apps aus dem Nichts")
		}
	})
	t.Run("falscherSchluessel", func(t *testing.T) {
		b := newReaderBed(t, false)
		r, _ := NewReader(b.root, wrongCipher{})
		if _, err := r.Credential(); err == nil {
			t.Fatal("fremde Verschlüsselung gelesen")
		}
	})
	t.Run("manipuliertesManifest", func(t *testing.T) {
		b := newReaderBed(t, false)
		other, _, _ := ed25519.GenerateKey(nil)
		cred := b.cred
		cred.Pubkey = other
		if err := b.st.Save("credential", cred); err != nil {
			t.Fatal(err)
		}
		r, _ := NewReader(b.root, testCipher{})
		if _, err := r.Apps(); !errors.Is(err, ErrManifest) {
			t.Fatalf("fremde Signatur: %v", err)
		}
	})
	t.Run("fremdesGeraet", func(t *testing.T) {
		b := newReaderBed(t, false)
		b.saveManifest(t, Manifest{Apps: &[]App{}, Contract: WindowsContract, DeviceID: "bbbbbbbbbbbbbbbb", Seq: 8, IssuedAt: "2026-11-04T10:00:00Z", GraceHours: 24, Pins: []Pin{}})
		r, _ := NewReader(b.root, testCipher{})
		if _, err := r.Apps(); !errors.Is(err, ErrManifest) {
			t.Fatalf("fremde Geräte-ID: %v", err)
		}
	})
	t.Run("altesManifestOhneApps", func(t *testing.T) {
		b := newReaderBed(t, false)
		b.saveManifest(t, Manifest{Contract: 4, DeviceID: b.cred.DeviceID, Seq: 8, IssuedAt: "2026-11-04T10:00:00Z", GraceHours: 24, Pins: []Pin{}})
		r, _ := NewReader(b.root, testCipher{})
		if _, err := r.Apps(); !errors.Is(err, ErrManifest) {
			t.Fatalf("Vertrag 4 ohne Apps: %v", err)
		}
	})
	t.Run("alterVertragMitApps", func(t *testing.T) {
		b := newReaderBed(t, false)
		b.saveManifest(t, Manifest{Apps: &[]App{}, Contract: 4, DeviceID: b.cred.DeviceID, Seq: 8, IssuedAt: "2026-11-04T10:00:00Z", GraceHours: 24, Pins: []Pin{}})
		r, _ := NewReader(b.root, testCipher{})
		if _, err := r.Apps(); !errors.Is(err, ErrManifest) {
			t.Fatalf("Vertrag 4 mit Apps: %v", err)
		}
	})
	t.Run("verweisAlsZugangsdaten", func(t *testing.T) {
		b := newReaderBed(t, false)
		outside := filepath.Join(t.TempDir(), "credential")
		raw, _ := os.ReadFile(filepath.Join(b.root, "credential"))
		if err := os.WriteFile(outside, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(filepath.Join(b.root, "credential"))
		if err := os.Symlink(outside, filepath.Join(b.root, "credential")); err != nil {
			t.Skip("Symlink nicht möglich:", err)
		}
		r, _ := NewReader(b.root, testCipher{})
		if _, err := r.Credential(); err == nil {
			t.Fatal("Verweis gefolgt")
		}
	})
	t.Run("ungueltigerWurzelpfad", func(t *testing.T) {
		for _, root := range []string{"", ".", "relativ/pfad"} {
			if _, err := NewReader(root, testCipher{}); err == nil {
				t.Errorf("Wurzel %q akzeptiert", root)
			}
		}
	})
}

type wrongCipher struct{}

func (wrongCipher) Seal(b []byte) ([]byte, error) { return b, nil }
func (wrongCipher) Open([]byte) ([]byte, error)   { return nil, errors.New("cipher") }

// Das Siegel gilt auch für den Leser: derselbe Prüfweg wie im Läufer.
func TestReaderOpenAppPrueftSiegel(t *testing.T) {
	b := newReaderBed(t, false)
	dir := filepath.Join(b.root, "apps", "demo", b.app.Commit)
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("evil"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ := NewReader(b.root, testCipher{})
	if _, err := r.OpenApp(b.app); err == nil {
		t.Fatal("Siegelbruch nicht erkannt")
	}
}

// B3, am AST: Der Leser und der Öffnungsweg enthalten keinen schreibenden Aufruf.
func TestReaderOhneSchreibendeAufrufe(t *testing.T) {
	forbidden := map[string]bool{
		"WriteFile": true, "Rename": true, "Remove": true, "RemoveAll": true, "Mkdir": true, "MkdirAll": true,
		"MkdirTemp": true, "CreateTemp": true, "Create": true, "OpenFile": true, "Truncate": true, "Chmod": true,
		"Chtimes": true, "Symlink": true, "Link": true, "Save": true, "Revoke": true, "Append": true, "Use": true,
		"write": true, "saveLocked": true, "removeTree": true, "Prepare": true, "Ensure": true,
	}
	check := func(path string, only map[string]bool) {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || (only != nil && !only[fn.Name.Name]) {
				continue
			}
			seen++
			ast.Inspect(fn, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && forbidden[sel.Sel.Name] {
					t.Errorf("%s: %s ruft %s", path, fn.Name.Name, sel.Sel.Name)
				}
				return true
			})
		}
		if seen == 0 {
			t.Fatalf("%s: keine geprüften Funktionen", path)
		}
	}
	check("reader.go", nil)
	check("appstore.go", map[string]bool{"openApp": true, "validAppEntry": true, "sealName": true})
}

// Engine.Sync legt die Apps des Manifests ab; der Leser öffnet sie danach. Fehler brechen den Sync nie ab.
func TestSyncLegtAppsAb(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	app, raw := buildApp(t, goodApp(), "")
	otherCommit, otherRaw := testfixture.PackageAt("apps/zweite", map[string][]byte{"index.html": appHTML("<p>zwei</p>")}, "")
	apps := []App{app, {Name: "zweite", Commit: otherCommit, Path: "apps/zweite"}}
	m := Manifest{Apps: &apps, Contract: WindowsContract, DeviceID: "own", Seq: 1, IssuedAt: "2026-11-04T10:00:00Z", GraceHours: 24, Pins: []Pin{}}
	rawM := encode(m)
	can, _ := canonical(rawM)
	var signed atomic.Pointer[SignedManifest]
	signed.Store(&SignedManifest{Manifest: rawM, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, can))})
	cfg := Config{Contract: WindowsContract, ExpectedAccount: "own", HeartbeatS: 60, PollHoldS: 25}
	cfg.Device.ID, cfg.Device.Kind = "own", "workstation"
	cfg.StartPolicy.Mode = "always"
	var packages atomic.Int32
	var failSecond atomic.Bool
	failSecond.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/config"):
			_ = json.NewEncoder(w).Encode(cfg)
		case strings.HasSuffix(r.URL.Path, "/manifest"):
			_ = json.NewEncoder(w).Encode(signed.Load())
		case strings.HasPrefix(r.URL.Path, "/api/device/v1/packages/"+app.Commit):
			packages.Add(1)
			w.Header().Set("Content-Type", "application/vnd.jnpt.gitobjects.v1")
			_, _ = w.Write(raw)
		case strings.HasPrefix(r.URL.Path, "/api/device/v1/packages/"+otherCommit):
			packages.Add(1)
			if failSecond.Load() {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":{"class":"not_found","message":"Der Auftrag oder das Paket wurde nicht gefunden."}}`))
				return
			}
			w.Header().Set("Content-Type", "application/vnd.jnpt.gitobjects.v1")
			_, _ = w.Write(otherRaw)
		default:
			t.Error("unerwarteter Weg", r.URL.Path)
		}
	}))
	defer srv.Close()
	st, _ := OpenState(t.TempDir(), testCipher{})
	api, _ := NewAPI(Trust{URL: srv.URL}, "token", st.Revoke)
	defer api.Close()
	logDir := t.TempDir()
	e := Engine{State: st, API: api, Credential: Credential{DeviceID: "own", Pubkey: pub}, Log: &Logger{Root: logDir}}
	if err := e.Sync(context.Background()); err != nil {
		t.Fatalf("App-Fehler brachen den Sync ab: %v", err)
	}
	r, _ := NewReader(st.Root, testCipher{})
	if _, err := r.OpenApp(app); err != nil {
		t.Fatalf("erste App fehlt: %v", err)
	}
	if _, err := r.OpenApp(apps[1]); err == nil {
		t.Fatal("zweite App ohne Paket geöffnet")
	}
	logged, _ := os.ReadFile(filepath.Join(logDir, "runner.log"))
	if !strings.Contains(string(logged), "10 1\n") {
		t.Fatalf("App-Störung nicht protokolliert: %q", logged)
	}
	// Zweiter Sync: bereits abgelegte App wird nicht erneut geholt; die fehlende erneut versucht.
	failSecond.Store(false)
	m.Seq = 2
	rawM = encode(m)
	can, _ = canonical(rawM)
	signed.Store(&SignedManifest{Manifest: rawM, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, can))})
	before := packages.Load()
	if err := e.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := packages.Load() - before; got != 1 {
		t.Fatalf("%d Paketabrufe im zweiten Sync, erwartet 1", got)
	}
	if _, err := r.OpenApp(apps[1]); err != nil {
		t.Fatalf("zweite App fehlt nach Wiederholung: %v", err)
	}
}

func TestLogCodeAppStoerung(t *testing.T) {
	l := Logger{Root: t.TempDir()}
	if err := l.Write(LogAppFault, 1); err != nil {
		t.Fatal(err)
	}
	if LogAppFault != 10 || l.Write(LogAppFault+1, 1) != ErrProtocol {
		t.Fatal("Codemenge nicht geschlossen")
	}
}

func TestRevokeLoeschtWebviewOhneVerweiseZuFolgen(t *testing.T) {
	st, _ := OpenState(t.TempDir(), testCipher{})
	outside := t.TempDir()
	keep := filepath.Join(outside, "bleibt.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(st.Root, "webview", "demo")
	if err := os.MkdirAll(web, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "Cookies"), []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(web, "verweis")); err != nil {
		t.Skip("Symlink nicht möglich:", err)
	}
	if err := st.Revoke(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("Widerruf folgte dem Verweis")
	}
	if _, err := os.Stat(filepath.Join(st.Root, "webview")); err == nil {
		t.Fatal("Benutzerdatenverzeichnis überlebt den Widerruf")
	}
}

// Widerruf und Ablegen sind serialisiert: Nach dem Widerruf entsteht kein Rest, auch keine neue Wurzel.
func TestAppPrepareNachWiderrufLegtNichtsAb(t *testing.T) {
	store, st := newStore(t)
	app, raw := buildApp(t, goodApp(), "")
	if err := st.Revoke(); err != nil {
		t.Fatal(err)
	}
	if err := store.Prepare(raw, app); !errors.Is(err, ErrRevoked) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(st.Root); err == nil {
		t.Fatal("Wurzel nach Widerruf neu angelegt")
	}
}

// B4: Das Gerätetoken wird für die Liste der Apps nie in ein Go-Feld dekodiert. Steht im Feld "token" etwas,
// das als Token ungültig wäre, scheitert Credential(), aber Apps() liest weiter.
func TestReaderAppsDekodiertKeinToken(t *testing.T) {
	b := newReaderBed(t, false)
	raw, err := json.Marshal(map[string]any{"trust": b.cred.Trust, "device_id": b.cred.DeviceID, "token": 12345, "expires_at": b.cred.ExpiresAt, "pubkey": b.cred.Pubkey})
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := testCipher{}.Seal(raw)
	if err = os.WriteFile(filepath.Join(b.root, "credential"), sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ := NewReader(b.root, testCipher{})
	if _, err = r.Credential(); err == nil {
		t.Fatal("Credential() hätte scheitern müssen: Testaufbau")
	}
	view, err := r.Apps()
	if err != nil || len(view.Apps) != 1 {
		t.Fatalf("Apps() liest das Token mit: %v", err)
	}
}
