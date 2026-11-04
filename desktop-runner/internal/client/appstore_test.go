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
	return []byte("<!doctype html><html><head><meta charset=\"utf-8\"><title>t</title><script src=\"app.js\"></script></head><body>" + body + "</body></html>")
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
			_ = os.WriteFile(filepath.Join(dir, "app.js"), []byte("alert(1)"), 0o600)
		},
		"dateiGleicheLaengeGeaendert": func(t *testing.T, st *State, dir string) {
			_ = os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(2)"), 0o600)
		},
		"dateiGeloescht": func(t *testing.T, st *State, dir string) { _ = os.Remove(filepath.Join(dir, "app.js")) },
		"dateiZusaetzlich": func(t *testing.T, st *State, dir string) {
			_ = os.WriteFile(filepath.Join(dir, "evil.js"), []byte("1"), 0o600)
		},
		"unterordnerZusaetzlich": func(t *testing.T, st *State, dir string) {
			_ = os.MkdirAll(filepath.Join(dir, "x"), 0o700)
			_ = os.WriteFile(filepath.Join(dir, "x", "evil.js"), []byte("1"), 0o600)
		},
		"indexAustausch": func(t *testing.T, st *State, dir string) {
			_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<script>1</script>"), 0o600)
		},
		"siegelGeloescht": func(t *testing.T, st *State, dir string) {
			_ = os.Remove(filepath.Join(st.Root, "app-demo-"+filepath.Base(dir)))
		},
		"verweis": func(t *testing.T, st *State, dir string) {
			_ = os.Remove(filepath.Join(dir, "app.js"))
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
			f["index.html"] = []byte("<html><head></head><body><script>alert(1)</script></body></html>")
		}), ""},
		{"onClick", with(func(f map[string][]byte) {
			f["index.html"] = []byte("<html><head></head><body><a onclick=\"x()\">a</a></body></html>")
		}), ""},
		{"javascriptLink", with(func(f map[string][]byte) {
			f["index.html"] = []byte("<html><head></head><body><a href=\"JaVa\tscript:x()\">a</a></body></html>")
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

// Das Literal setzt der Läufer beim Ablegen selbst ein (LEAD c-6637272308): als erstes Element in
// <head>, nach der Paketprüfung und vor dem Siegel. Pakete mit eigener CSP-Meta oder ohne sauberes
// <head> werden abgewiesen.
func TestAppCSPWirdEingesetzt(t *testing.T) {
	csp := fenster.HeadPrefix()
	for _, tc := range []struct {
		name, html, want string
	}{
		{"einfach", "<html><head><title>x</title></head><body></body></html>", "<html><head>" + csp + "<title>x</title></head><body></body></html>"},
		{"mitDoctype", "<!doctype html><html><head><meta charset=\"utf-8\"></head></html>", "<!doctype html><html><head>" + csp + "<meta charset=\"utf-8\"></head></html>"},
		{"headMitAttribut", "<html><head lang=\"de\"><title>x</title></head></html>", "<html><head lang=\"de\">" + csp + "<title>x</title></head></html>"},
		{"grossgeschrieben", "<HTML><HEAD><TITLE>x</TITLE></HEAD></HTML>", "<HTML><HEAD>" + csp + "<TITLE>x</TITLE></HEAD></HTML>"},
		{"kommentarDavor", "<!-- c --><html>\n<head>\n<title>x</title></head></html>", "<!-- c --><html>\n<head>" + csp + "\n<title>x</title></head></html>"},
		{"ohneHtmlTag", "<head><title>x</title></head><body></body>", "<head>" + csp + "<title>x</title></head><body></body>"},
		{"umlaute", "<html><head><title>Grüße</title></head></html>", "<html><head>" + csp + "<title>Grüße</title></head></html>"},
		{"crlf", "<html>\r\n<head>\r\n<title>x</title></head></html>", "<html>\r\n<head>" + csp + "\r\n<title>x</title></head></html>"},
		{"tabsUndUmbruch", "\t<!doctype html>\n\t<html>\n\t<HEAD>\n</HEAD></html>", "\t<!doctype html>\n\t<html>\n\t<HEAD>" + csp + "\n</HEAD></html>"},
		{"headMitBody", "<html><head></head><body><p>x</p></body></html>", "<html><head>" + csp + "</head><body><p>x</p></body></html>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := injectCSP([]byte(tc.html))
			if err != nil || string(got) != tc.want {
				t.Fatalf("injectCSP = %q, %v", got, err)
			}
			if !leadingCSP(got) {
				t.Fatal("eingesetztes Literal steht nicht an erster Stelle")
			}
		})
	}
}

func TestAppCSPAbweisungen(t *testing.T) {
	csp := fenster.CSPMeta()
	for name, html := range map[string]string{
		"eigenesCSP":              "<html><head>" + csp + "</head></html>",
		"eigenesCSPWeiter":        "<html><head><title>x</title>" + csp + "</head></html>",
		"anderesCSP":              `<html><head><meta http-equiv="Content-Security-Policy" content="default-src *"></head></html>`,
		"gelockertesCSP":          `<html><head><meta http-equiv="Content-Security-Policy" content="` + fenster.CSP + `; script-src 'unsafe-inline'"></head></html>`,
		"leereContentCSP":         `<html><head><meta http-equiv="Content-Security-Policy" content=""></head></html>`,
		"grossKleinCSP":           `<html><head><META HTTP-EQUIV="content-security-policy" content="x"></head></html>`,
		"cspImBody":               "<html><head></head><body>" + csp + "</body></html>",
		"keinHead":                "<html><body><p>x</p></body></html>",
		"nurText":                 "hallo",
		"leer":                    "",
		"headNachBody":            "<html><body></body><head></head></html>",
		"zweitesHead":             "<html><head></head><head></head></html>",
		"selbstschliessendesHead": "<html><head/><body></body></html>",
		"skriptVorHead":           "<html><script src=\"a.js\"></script><head></head></html>",
		"metaVorHead":             "<html><meta charset=\"utf-8\"><head></head></html>",
		"textVorHead":             "<html>Text<head></head></html>",
		"headNurImKommentar":      "<html><!-- <head> --><body></body></html>",
		"headImTitel":             "<html><title><head></title><body></body></html>",
		// SEC-R4 F1: Text oder End-Tag vor dem <head> beendet den Head für den Parser; der Tokenizer sieht das nicht.
		"textVorHeadOhneHtml":         "x<head><title>y</title></head>",
		"endTagBrVorHead":             "</br><head></head>",
		"endTagBrNachHtml":            "<html></br><head></head></html>",
		"endTagPVorHead":              "<html></p><head></head></html>",
		"absatzVorHead":               "<p><head></head></p>",
		"headNurImSkript":             "<html><script>var a = \"<head>\"</script></html>",
		"stilVorHead":                 "<style>a{}</style><head></head>",
		"bomVorDoctype":               "\ufeff<!doctype html><html><head></head></html>",
		"nbspVorHead":                 "\u00a0<html><head></head></html>",
		"nelVorHead":                  "\u0085<head></head>",
		"nulVorHead":                  "\x00<head></head>",
		"headMitInhaltInTitelUndText": "<html><title>a</title>x<head></head></html>",
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := injectCSP([]byte(html)); err == nil {
				t.Fatalf("angenommen: %q", out)
			}
			files := goodApp()
			files["index.html"] = []byte(html)
			app, raw := buildApp(t, files, "")
			if _, err := loadAppPackage(raw, app); err == nil {
				t.Fatal("Ladeprüfung nahm HTML an")
			}
		})
	}
}

func TestLeadingCSP(t *testing.T) {
	csp := fenster.HeadPrefix()
	for _, tc := range []struct {
		name, html string
		ok         bool
	}{
		{"erstes", "<html><head>" + csp + "<title>x</title></head></html>", true},
		{"nichtErstes", "<html><head><title>x</title>" + csp + "</head></html>", false},
		{"fehlt", "<html><head><title>x</title></head></html>", false},
		{"zweimal", "<html><head>" + csp + csp + "</head></html>", false},
		{"zusaetzlichesAnderes", `<html><head>` + csp + `<meta http-equiv="Content-Security-Policy" content="default-src *"></head></html>`, false},
		{"abgeaendertesLiteral", `<html><head><meta http-equiv="Content-Security-Policy" content="` + fenster.CSP + `;"></head></html>`, false},
		{"keinHead", "<html><body>" + csp + "</body></html>", false},
		{"leer", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := leadingCSP([]byte(tc.html)); got != tc.ok {
				t.Fatalf("leadingCSP = %v", got)
			}
		})
	}
}

// Jede HTML-Datei, nicht nur index.html, bekommt das Literal; eine ohne sauberes <head> kippt das Paket.
func TestAppCSPJedeHTMLDatei(t *testing.T) {
	store, st := newStore(t)
	files := goodApp()
	files["seite2.html"] = []byte("<html><body></body></html>")
	app, raw := buildApp(t, files, "")
	if err := store.Prepare(raw, app); err == nil {
		t.Fatal("zweite HTML-Datei ohne <head> angenommen")
	}
	assertNichtsAbgelegt(t, st, app)
	files["seite2.html"] = appHTML("")
	files["unter/seite3.html"] = appHTML("<p>drei</p>")
	app, raw = buildApp(t, files, "")
	if err := store.Prepare(raw, app); err != nil {
		t.Fatal(err)
	}
	dir, err := store.Open(app)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "seite2.html", "unter/seite3.html"} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil || !leadingCSP(b) || strings.Count(string(b), fenster.CSPMeta()) != 1 {
			t.Fatalf("%s ohne genau ein führendes Literal: %v", name, err)
		}
	}
	// Nur HTML wird verändert; andere Dateien bleiben bytegleich zum Paket.
	js, _ := os.ReadFile(filepath.Join(dir, "app.js"))
	if string(js) != "console.log(1)" {
		t.Fatal("Nicht-HTML verändert")
	}
}

// Das Siegel deckt die veränderten Bytes, nicht die Paketbytes.
func TestAppSiegelDecktEingesetztesLiteral(t *testing.T) {
	store, st := newStore(t)
	app, raw := buildApp(t, goodApp(), "")
	if err := store.Prepare(raw, app); err != nil {
		t.Fatal(err)
	}
	dir, err := store.Open(app)
	if err != nil {
		t.Fatal(err)
	}
	var seal appSeal
	if err = st.Load(sealName(app), &seal); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	found := false
	for _, f := range seal.Files {
		if f.Path != "index.html" {
			continue
		}
		found = true
		if f.SHA256 != sum(b) || f.SHA256 == sum(goodApp()["index.html"]) {
			t.Fatal("Siegel deckt nicht die veränderten Bytes")
		}
	}
	if !found {
		t.Fatal("index.html nicht versiegelt")
	}
}

// Auch bei einem neu berechneten, in sich stimmigen Siegel (Angreifer mit Zugriff auf den
// Zustand) prüft Open, dass genau das Literal an erster Stelle steht.
func TestAppOpenPrueftFuehrendesLiteralTrotzNeuemSiegel(t *testing.T) {
	store, st := newStore(t)
	app, raw := buildApp(t, goodApp(), "")
	if err := store.Prepare(raw, app); err != nil {
		t.Fatal(err)
	}
	dir, err := store.Open(app)
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	without := strings.Replace(string(orig), fenster.CSPMeta(), "", 1)
	for name, mod := range map[string]string{
		"literalEntfernt":   without,
		"literalVerschoben": strings.Replace(without, "</head>", fenster.CSPMeta()+"</head>", 1),
		"literalGelockert":  strings.Replace(string(orig), "connect-src 'none'", "connect-src *", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(mod), 0o600); err != nil {
				t.Fatal(err)
			}
			files, err := Snapshot(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err = st.Save(sealName(app), appSeal{Name: app.Name, Commit: app.Commit, Files: files}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Open(app); err == nil {
				t.Fatal("Open akzeptiert HTML ohne führendes Literal")
			}
		})
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
	_ = os.WriteFile(filepath.Join(dir, "app.js"), []byte("evil"), 0o600)
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

// SEC-R4 F1: Die Prüfung nach dem Einsetzen ist die Parser-Sicht. Hier ist der Vorfilter (Tokenizer)
// bewusst nachsichtig (Unicode-Leerraum vor dem Head), der Parser verschiebt das <head> aber in den Body:
// Das Literal läge im Body, Chromium verwürfe es. Nur die Parserprüfung fängt das.
func TestAppCSPParserPruefungIstDieEigentlichePruefung(t *testing.T) {
	csp := fenster.HeadPrefix()
	if _, err := injectCSP([]byte("\u00a0<html><head></head></html>")); err == nil {
		t.Fatal("NBSP vor dem Head eingesetzt, obwohl der Parser das Literal in den Body legt")
	}
	for name, html := range map[string]string{
		"literalImBodyBeiNbsp": "\u00a0<html><head>" + csp + "</head></html>",
		"literalNachText":      "x<head>" + csp + "</head>",
		"literalNachEndTag":    "</br><head>" + csp + "</head>",
	} {
		t.Run(name, func(t *testing.T) {
			if leadingCSP([]byte(html)) {
				t.Fatal("Open akzeptiert ein Literal, das der Parser nicht in <head> sieht")
			}
		})
	}
	// Kontrolle: derselbe Aufbau ohne Störzeichen besteht beides.
	out, err := injectCSP([]byte("<html><head></head></html>"))
	if err != nil || !leadingCSP(out) {
		t.Fatal(err)
	}
}

// SEC-R4 F4: Ein Zeichensatz-Trick darf die eingesetzte CSP nie unsichtbar machen. Der Browser dekodiert
// vor dem Parsen: ESC $ B in einem Kommentar vor <head> plus <meta charset=iso-2022-jp> dahinter lässt ihn
// alles bis ESC ( B als Doppelbyte-Text lesen, auch die eingesetzte Meta. Der Läufer setzt deshalb zuerst
// <meta charset="utf-8"> und dann das Literal ein und lehnt ESC-Bytes sowie ungültiges UTF-8 ab.
func TestAppCSPZeichensatzTrick(t *testing.T) {
	esc := "\x1b"
	script := `<script src="http://fremd.example/evil.js"></script>`
	for name, html := range map[string]string{
		"iso2022Charset":    "<!doctype html><!--" + esc + "$B--><head></head>" + esc + "(B-->" + script + "<meta charset=iso-2022-jp>",
		"iso2022HttpEquiv":  "<!doctype html><!--" + esc + "$B--><head></head>" + esc + "(B-->" + script + `<meta http-equiv="content-type" content="text/html; charset=iso-2022-jp">`,
		"escImKommentar":    "<html><!-- " + esc + " --><head></head></html>",
		"escImText":         "<html><head></head><body>a" + esc + "b</body></html>",
		"escAlsDritterTeil": "<html><head><title>x" + esc + "(B</title></head></html>",
		"ungueltigesUTF8":   "<html><head><title>x\xff</title></head></html>",
		"abgeschnittenUTF8": "<html><head><title>x\xc3</title></head></html>",
		"utf16Bom":          "\xff\xfe<\x00h\x00t\x00m\x00l\x00>\x00",
		"latin1Umlaut":      "<html><head><title>Gr\xfc\xdfe</title></head></html>",
		"nulByte":           "<html><head><title>x\x00</title></head></html>",
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := injectCSP([]byte(html)); err == nil {
				t.Fatalf("angenommen: %q", out)
			}
			files := goodApp()
			files["index.html"] = []byte(html)
			app, raw := buildApp(t, files, "")
			if _, err := loadAppPackage(raw, app); err == nil {
				t.Fatal("Ladeprüfung nahm HTML an")
			}
		})
	}
}

// Gute Fälle: eigene Zeichensatz-Meta des Pakets bleibt erlaubt, die des Läufers steht davor und gewinnt.
func TestAppCSPZeichensatzMetaVorn(t *testing.T) {
	prefix := fenster.HeadPrefix()
	if prefix != fenster.CharsetMeta()+fenster.CSPMeta() || fenster.CharsetMeta() != `<meta charset="utf-8">` {
		t.Fatalf("Präfix: %q", prefix)
	}
	for name, tc := range map[string]struct{ in, want string }{
		"eigenesUTF8":      {"<html><head><meta charset=utf-8><title>x</title></head></html>", "<html><head>" + prefix + "<meta charset=utf-8><title>x</title></head></html>"},
		"eigenesUTF8Gross": {`<html><head><meta charset="UTF-8"></head></html>`, "<html><head>" + prefix + `<meta charset="UTF-8"></head></html>`},
		// Ohne ESC ist auch eine fremde Deklaration harmlos: Die erste Deklaration (unsere) gewinnt.
		"eigenesIso":    {"<html><head><meta charset=iso-2022-jp></head></html>", "<html><head>" + prefix + "<meta charset=iso-2022-jp></head></html>"},
		"eigenesLatin1": {`<html><head><meta http-equiv="content-type" content="text/html; charset=iso-8859-1"></head></html>`, "<html><head>" + prefix + `<meta http-equiv="content-type" content="text/html; charset=iso-8859-1"></head></html>`},
		"umlauteUTF8":   {"<html><head><title>Grüße</title></head></html>", "<html><head>" + prefix + "<title>Grüße</title></head></html>"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := injectCSP([]byte(tc.in))
			if err != nil || string(got) != tc.want || !leadingCSP(got) {
				t.Fatalf("injectCSP = %q, %v", got, err)
			}
		})
	}
}

func TestLeadingCSPBrauchtZuerstDieZeichensatzMeta(t *testing.T) {
	csp, cs := fenster.CSPMeta(), fenster.CharsetMeta()
	for name, tc := range map[string]struct {
		html string
		ok   bool
	}{
		"beideInOrdnung":     {"<html><head>" + cs + csp + "<title>x</title></head></html>", true},
		"nurCSP":             {"<html><head>" + csp + "</head></html>", false},
		"charsetNachCSP":     {"<html><head>" + csp + cs + "</head></html>", false},
		"andereDeklaration":  {`<html><head><meta charset="iso-2022-jp">` + csp + "</head></html>", false},
		"charsetNichtErstes": {"<html><head><title>x</title>" + cs + csp + "</head></html>", false},
		"zweiCharsetVorCSP":  {"<html><head>" + cs + cs + csp + "</head></html>", false},
		"charsetKommentar":   {"<html><head><!-- " + cs + " -->" + csp + "</head></html>", false},
		"escImDokument":      {"<html><head>" + cs + csp + "\x1b</head></html>", false},
		"ungueltigesUTF8":    {"<html><head>" + cs + csp + "\xff</head></html>", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := leadingCSP([]byte(tc.html)); got != tc.ok {
				t.Fatalf("leadingCSP = %v", got)
			}
		})
	}
}

// Open lehnt auch ein in sich stimmiges Siegel ab, wenn der Zeichensatztrick nachträglich in der Ablage steckt.
func TestAppOpenLehntZeichensatzTrickTrotzNeuemSiegelAb(t *testing.T) {
	store, st := newStore(t)
	app, raw := buildApp(t, goodApp(), "")
	if err := store.Prepare(raw, app); err != nil {
		t.Fatal(err)
	}
	dir, err := store.Open(app)
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	for name, mod := range map[string]string{
		"charsetEntfernt": strings.Replace(string(orig), fenster.CharsetMeta(), "", 1),
		"escEingefuegt":   strings.Replace(string(orig), "<body>", "<body>\x1b$B", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(mod), 0o600); err != nil {
				t.Fatal(err)
			}
			files, err := Snapshot(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err = st.Save(sealName(app), appSeal{Name: app.Name, Commit: app.Commit, Files: files}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Open(app); err == nil {
				t.Fatal("Open akzeptiert die Datei")
			}
		})
	}
}
