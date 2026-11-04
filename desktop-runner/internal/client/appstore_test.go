package client

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
)

func appHTML(body string) []byte {
	return []byte("<!doctype html><html><head><meta charset=\"utf-8\">" + fenster.CSPMeta() + "<title>t</title><script src=\"app.js\"></script></head><body>" + body + "</body></html>")
}

func goodApp() map[string][]byte {
	return map[string][]byte{
		"index.html":    appHTML("<p>hallo</p>"),
		"app.js":        []byte("console.log(1)"),
		"style.css":     []byte("body{margin:0}"),
		"img/logo.svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		"jnpt.app.json": []byte(`{"name":"demo"}`),
	}
}

func buildApp(t *testing.T, files map[string][]byte, mode string) (App, []byte) {
	t.Helper()
	commit, raw := testfixture.PackageAt("apps/demo", files, mode)
	return App{Name: "demo", Commit: commit, Path: "apps/demo"}, raw
}

func newStore(t *testing.T) (*AppStore, *State) {
	t.Helper()
	st, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	return NewAppStore(st), st
}

func TestAppPaketGuterFallLegtPrivatAbUndVersiegelt(t *testing.T) {
	store, st := newStore(t)
	app, raw := buildApp(t, goodApp(), "")
	if err := store.Prepare(raw, app); err != nil {
		t.Fatal(err)
	}
	dir, err := store.Open(app)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(st.Root, "apps", "demo", app.Commit)
	if dir != want {
		t.Fatalf("Ablage %s, erwartet %s", dir, want)
	}
	for _, name := range []string{"index.html", "app.js", "style.css", "img/logo.svg"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s fehlt: %v", name, err)
		}
	}
	// Der Kern-Manifestzeuge gehört nicht ins ausgelieferte Fenster.
	if _, err := os.Stat(filepath.Join(dir, "jnpt.app.json")); err == nil {
		t.Error("jnpt.app.json wurde abgelegt")
	}
	if runtime.GOOS != "windows" {
		_ = filepath.WalkDir(filepath.Join(st.Root, "apps"), func(p string, d os.DirEntry, err error) error {
			info, _ := d.Info()
			if info != nil && info.Mode().Perm()&0o077 != 0 {
				t.Errorf("%s nicht privat: %v", p, info.Mode())
			}
			return nil
		})
	}
	// Das Siegel liegt im verschlüsselten Zustand, nie neben den Dateien.
	if _, err := os.Stat(filepath.Join(st.Root, "app-demo-"+app.Commit)); err != nil {
		t.Fatalf("Siegel fehlt: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(st.Root, "apps", "demo"))
	if len(entries) != 1 {
		t.Fatalf("Reste im Appordner: %v", entries)
	}
}

func TestAppSiegelVorJedemOeffnen(t *testing.T) {
	tamper := map[string]func(t *testing.T, st *State, dir string){
		"dateiGeaendert": func(t *testing.T, st *State, dir string) {
			os.WriteFile(filepath.Join(dir, "app.js"), []byte("alert(1)"), 0o600)
		},
		"dateiGleicheLaengeGeaendert": func(t *testing.T, st *State, dir string) {
			os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(2)"), 0o600)
		},
		"dateiGeloescht": func(t *testing.T, st *State, dir string) { os.Remove(filepath.Join(dir, "app.js")) },
		"dateiZusaetzlich": func(t *testing.T, st *State, dir string) {
			os.WriteFile(filepath.Join(dir, "evil.js"), []byte("1"), 0o600)
		},
		"unterordnerZusaetzlich": func(t *testing.T, st *State, dir string) {
			os.MkdirAll(filepath.Join(dir, "x"), 0o700)
			os.WriteFile(filepath.Join(dir, "x", "evil.js"), []byte("1"), 0o600)
		},
		"indexAustausch": func(t *testing.T, st *State, dir string) {
			os.WriteFile(filepath.Join(dir, "index.html"), []byte("<script>1</script>"), 0o600)
		},
		"siegelGeloescht": func(t *testing.T, st *State, dir string) {
			os.Remove(filepath.Join(st.Root, "app-demo-"+filepath.Base(dir)))
		},
		"verweis": func(t *testing.T, st *State, dir string) {
			os.Remove(filepath.Join(dir, "app.js"))
			if err := os.Symlink(filepath.Join(dir, "style.css"), filepath.Join(dir, "app.js")); err != nil {
				t.Skip("Symlink nicht möglich:", err)
			}
		},
	}
	for name, fn := range tamper {
		t.Run(name, func(t *testing.T) {
			store, st := newStore(t)
			app, raw := buildApp(t, goodApp(), "")
			if err := store.Prepare(raw, app); err != nil {
				t.Fatal(err)
			}
			dir, err := store.Open(app)
			if err != nil {
				t.Fatal(err)
			}
			fn(t, st, dir)
			if _, err := store.Open(app); err == nil {
				t.Fatal("Siegelbruch beim Öffnen nicht erkannt")
			}
		})
	}
}

func TestAppOpenOhneAblage(t *testing.T) {
	store, _ := newStore(t)
	app, _ := buildApp(t, goodApp(), "")
	if _, err := store.Open(app); err == nil {
		t.Fatal("nie abgelegte App geöffnet")
	}
	for _, bad := range []App{{Name: "../x", Commit: app.Commit, Path: "apps/../x"}, {Name: "demo", Commit: "zz", Path: "apps/demo"}, {Name: "demo", Commit: app.Commit, Path: "apps/andere"}, {Name: "Demo", Commit: app.Commit, Path: "apps/Demo"}} {
		if _, err := store.Open(bad); err == nil {
			t.Errorf("ungültiger Eintrag %+v geöffnet", bad)
		}
		if err := store.Prepare([]byte("x"), bad); err == nil {
			t.Errorf("ungültiger Eintrag %+v abgelegt", bad)
		}
	}
}

func TestAppPaketAblehnungenLegenNichtsAb(t *testing.T) {
	with := func(mut func(map[string][]byte)) map[string][]byte {
		f := goodApp()
		mut(f)
		return f
	}
	for _, tc := range []struct {
		name  string
		files map[string][]byte
		mode  string
	}{
		{"ohneIndex", with(func(f map[string][]byte) { delete(f, "index.html") }), ""},
		{"ausfuehrbar", goodApp(), "100755"},
		{"verweisModus", goodApp(), "120000"},
		{"unbekannteEndung", with(func(f map[string][]byte) { f["tool.exe"] = []byte("MZ") }), ""},
		{"wasm", with(func(f map[string][]byte) { f["m.wasm"] = []byte("\x00asm") }), ""},
		{"inlineSkript", with(func(f map[string][]byte) {
			f["index.html"] = []byte("<html><head>" + fenster.CSPMeta() + "</head><body><script>alert(1)</script></body></html>")
		}), ""},
		{"onClick", with(func(f map[string][]byte) {
			f["index.html"] = []byte("<html><head>" + fenster.CSPMeta() + "</head><body><a onclick=\"x()\">a</a></body></html>")
		}), ""},
		{"javascriptLink", with(func(f map[string][]byte) {
			f["index.html"] = []byte("<html><head>" + fenster.CSPMeta() + "</head><body><a href=\"JaVa\tscript:x()\">a</a></body></html>")
		}), ""},
		{"geheimnisImSkript", with(func(f map[string][]byte) { f["app.js"] = []byte("const t='jnptpc_ABCDEFGH.IJKLMNOP'") }), ""},
		{"privaterSchluessel", with(func(f map[string][]byte) { f["app.js"] = []byte("-----BEGIN PRIVATE KEY-----") }), ""},
		{"zuGross", with(func(f map[string][]byte) { f["big.txt"] = []byte(strings.Repeat("a", 4<<20+1)) }), ""},
		{"zuvieleDateien", with(func(f map[string][]byte) {
			for i := 0; i < 1001; i++ {
				f["d/"+strings.Repeat("a", 3)+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676))+".txt"] = []byte{byte(i), byte(i >> 8)}
			}
		}), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, st := newStore(t)
			app, raw := buildApp(t, tc.files, tc.mode)
			if err := store.Prepare(raw, app); err == nil {
				t.Fatal("Paket angenommen")
			}
			assertNichtsAbgelegt(t, st, app)
		})
	}
}

func assertNichtsAbgelegt(t *testing.T, st *State, app App) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(st.Root, "apps", "demo", app.Commit)); err == nil {
		t.Error("Ablage vorhanden")
	}
	if _, err := os.Stat(filepath.Join(st.Root, "app-demo-"+app.Commit)); err == nil {
		t.Error("Siegel vorhanden")
	}
	if entries, _ := os.ReadDir(filepath.Join(st.Root, "apps", "demo")); len(entries) != 0 {
		t.Errorf("Staging-Reste: %v", entries)
	}
}

func TestAppPaketIntegritaet(t *testing.T) {
	app, raw := buildApp(t, goodApp(), "")
	other, _ := buildApp(t, with1(goodApp(), "app.js", "x"), "")
	for name, tc := range map[string]struct {
		app App
		raw []byte
	}{
		"fremderCommit":     {other, raw},
		"bytegeaendert":     {app, flip(raw, len(raw)-3)},
		"abgeschnitten":     {app, raw[:len(raw)-5]},
		"folgedaten":        {app, append(append([]byte(nil), raw...), 0)},
		"leer":              {app, nil},
		"andererPfadImKopf": {App{Name: "demo", Commit: app.Commit, Path: "apps/demo2"}, raw},
	} {
		t.Run(name, func(t *testing.T) {
			store, st := newStore(t)
			if err := store.Prepare(tc.raw, tc.app); err == nil {
				t.Fatal("Paket angenommen")
			}
			assertNichtsAbgelegt(t, st, tc.app)
		})
	}
}

func with1(m map[string][]byte, k, v string) map[string][]byte { m[k] = []byte(v); return m }
func flip(b []byte, i int) []byte {
	c := append([]byte(nil), b...)
	c[i] ^= 0xff
	return c
}

// Ladeprüfung: Das CSP steht als Meta-Element in jeder HTML-Datei (kein Header
// am virtuellen Host), exakt als Literal und vor allem, was Ressourcen lädt.
func TestAppLadepruefungCSPLiteral(t *testing.T) {
	csp := fenster.CSPMeta()
	for _, tc := range []struct {
		name, html string
		ok         bool
	}{
		{"exakt", "<!doctype html><html><head><meta charset=\"utf-8\">" + csp + "<script src=\"a.js\"></script></head><body></body></html>", true},
		{"ohneHeadTags", csp + "<p>x</p>", true},
		{"grossKleinAttribut", `<html><head><META HTTP-EQUIV="content-security-policy" content="` + fenster.CSP + `"></head></html>`, true},
		{"titelDavor", "<html><head><title>x</title>" + csp + "</head></html>", true},
		{"fehlt", "<html><head><title>x</title></head><body></body></html>", false},
		{"nurImKommentar", "<html><head><!-- " + csp + " --></head></html>", false},
		{"nurImText", "<html><body><p>" + strings.ReplaceAll(csp, "<", "&lt;") + "</p></body></html>", false},
		{"gelockert", `<html><head><meta http-equiv="Content-Security-Policy" content="` + fenster.CSP + `; script-src 'unsafe-inline'"></head></html>`, false},
		{"anderesCSP", `<html><head><meta http-equiv="Content-Security-Policy" content="default-src *"></head></html>`, false},
		{"leereContent", `<html><head><meta http-equiv="Content-Security-Policy" content=""></head></html>`, false},
		{"leerzeichenAmEnde", `<html><head><meta http-equiv="Content-Security-Policy" content="` + fenster.CSP + ` "></head></html>`, false},
		{"nameStattHttpEquiv", `<html><head><meta name="Content-Security-Policy" content="` + fenster.CSP + `"></head></html>`, false},
		{"skriptVorCSP", "<html><head><script src=\"a.js\"></script>" + csp + "</head></html>", false},
		{"bildVorCSP", "<html><head><link rel=\"stylesheet\" href=\"a.css\">" + csp + "</head></html>", false},
		{"bodyVorCSP", "<html><body>" + csp + "</body></html>", false},
		{"zweitesCSP", "<html><head>" + csp + csp + "</head></html>", false},
		{"zweitesAnderesCSP", `<html><head>` + csp + `<meta http-equiv="Content-Security-Policy" content="default-src *"></head></html>`, false},
		{"baseVorCSP", `<html><head><base href="https://evil.example/">` + csp + `</head></html>`, false},
		{"leer", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasExactCSPMeta([]byte(tc.html)); got != tc.ok {
				t.Fatalf("hasExactCSPMeta = %v", got)
			}
			// Dieselbe Entscheidung fällt in der Ladeprüfung, nicht nur im Hilfsprüfer.
			files := goodApp()
			files["index.html"] = []byte(tc.html)
			app, raw := buildApp(t, files, "")
			if _, err := loadAppPackage(raw, app); (err == nil) != tc.ok {
				t.Fatalf("Ladeprüfung: err = %v", err)
			}
		})
	}
}

// Jede HTML-Datei, nicht nur index.html, braucht das Literal.
func TestAppLadepruefungCSPJedeHTMLDatei(t *testing.T) {
	store, st := newStore(t)
	files := goodApp()
	files["seite2.html"] = []byte("<html><head><title>x</title></head><body></body></html>")
	app, raw := buildApp(t, files, "")
	if err := store.Prepare(raw, app); err == nil {
		t.Fatal("zweite HTML-Datei ohne CSP angenommen")
	}
	assertNichtsAbgelegt(t, st, app)
	files["seite2.html"] = appHTML("")
	app, raw = buildApp(t, files, "")
	if err := store.Prepare(raw, app); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureHoltPaketUeberHTTPNurEinmal(t *testing.T) {
	store, st := newStore(t)
	app, raw := buildApp(t, goodApp(), "")
	var hits atomic.Int32
	api := l1API(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/device/v1/packages/"+app.Commit || r.URL.Query().Get("path") != "apps/demo" || r.Header.Get("X-Jnpt-Desktop-Contract") != "5" {
			t.Error("Anfrage", r.Method, r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "application/vnd.jnpt.gitobjects.v1")
		w.Write(raw)
	})
	if err := store.Ensure(context.Background(), api, app); err != nil {
		t.Fatal(err)
	}
	if err := store.Ensure(context.Background(), api, app); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("%d Paketabrufe statt 1", hits.Load())
	}
	// Eine manipulierte Ablage wird nie stillschweigend repariert.
	dir, _ := store.Open(app)
	os.WriteFile(filepath.Join(dir, "app.js"), []byte("evil"), 0o600)
	if err := store.Ensure(context.Background(), api, app); err == nil {
		t.Fatal("manipulierte Ablage durch Ensure überdeckt")
	}
	if hits.Load() != 1 {
		t.Fatal("Ensure holte bei Siegelbruch erneut")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "app.js"))
	if string(b) != "evil" {
		t.Fatal("Ablage verändert")
	}
	_ = st
}

func TestEnsureFehlerLegtNichtsAb(t *testing.T) {
	store, st := newStore(t)
	app, raw := buildApp(t, goodApp(), "")
	for name, h := range map[string]http.HandlerFunc{
		"404": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"class":"not_found","message":"Der Auftrag oder das Paket wurde nicht gefunden."}}`))
		},
		"falscherMedientyp": func(w http.ResponseWriter, r *http.Request) { w.Write(raw) },
		"manipuliert": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/vnd.jnpt.gitobjects.v1")
			w.Write(flip(raw, len(raw)-3))
		},
	} {
		t.Run(name, func(t *testing.T) {
			api := l1API(t, h)
			if err := store.Ensure(context.Background(), api, app); err == nil {
				t.Fatal("kein Fehler")
			}
			assertNichtsAbgelegt(t, st, app)
		})
	}
}

func TestAppAblageVerschwindetBeimWiderruf(t *testing.T) {
	store, st := newStore(t)
	app, raw := buildApp(t, goodApp(), "")
	if err := store.Prepare(raw, app); err != nil {
		t.Fatal(err)
	}
	if err := st.Revoke(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(st.Root, "apps")); err == nil {
		t.Fatal("App-Ablage überlebt den Widerruf")
	}
}
