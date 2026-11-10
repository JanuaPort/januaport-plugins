package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
)

// #1240 F-T: Der Läufer liest das Feld tasten aus der jnpt.app.json des geprüften Pakets, parst es streng und
// legt es versiegelt im verschlüsselten Zustand ab, nie im Ordner der WebView. Das Bedienfeld liest es über den
// Reader nur bei gültigem Siegel.

func manifestWithKeys(keys string) []byte {
	return []byte(`{"name":"demo","fenster":"pc","modus":"b","teams":[],"werkzeuge":[],"skripte":["demo"],"tasten":` + keys + `}`)
}

func appWithManifest(manifest []byte) map[string][]byte {
	files := goodApp()
	files["jnpt.app.json"] = manifest
	return files
}

const oneKey = `[{"taste":"Strg+Alt+F5","pin":"demo","setup":"ansicht"}]`

func TestParseHotkeysStreng(t *testing.T) {
	long := strings.Repeat("a", 49)
	tests := []struct {
		name     string
		manifest string
		want     []Hotkey
		bad      bool
	}{
		{name: "Feld fehlt", manifest: `{"name":"demo"}`},
		{name: "leere Liste", manifest: string(manifestWithKeys(`[]`))},
		{name: "null", manifest: string(manifestWithKeys(`null`))},
		{name: "ein Kürzel", manifest: string(manifestWithKeys(oneKey)), want: []Hotkey{{Key: "Strg+Alt+F5", Pin: "demo", Setup: "ansicht"}}},
		{name: "zwölf Kürzel", manifest: string(manifestWithKeys(twelveKeys())), want: twelveHotkeys()},
		{name: "Strg+Alt+7", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+7","pin":"demo","setup":"a"}]`))},
		{name: "Buchstabe", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+Q","pin":"demo","setup":"a"}]`))},
		{name: "F13", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+F13","pin":"demo","setup":"a"}]`))},
		{name: "Kleinschreibung", bad: true, manifest: string(manifestWithKeys(`[{"taste":"strg+alt+f5","pin":"demo","setup":"a"}]`))},
		{name: "Doppel", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+F5","pin":"a","setup":"b"},{"taste":"Strg+Alt+F5","pin":"c","setup":"d"}]`))},
		{name: "Pin außerhalb des Musters", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+F5","pin":"../x","setup":"a"}]`))},
		{name: "Pin zu lang", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+F5","pin":"` + long + `","setup":"a"}]`))},
		{name: "Setup außerhalb des Musters", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+F5","pin":"demo","setup":"a b"}]`))},
		{name: "Setup leer", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+F5","pin":"demo","setup":""}]`))},
		{name: "unbekanntes Feld im Eintrag", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+F5","pin":"demo","setup":"a","app":"x"}]`))},
		{name: "Feld fehlt im Eintrag", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+F5","pin":"demo"}]`))},
		{name: "doppeltes Feld im Eintrag", bad: true, manifest: string(manifestWithKeys(`[{"taste":"Strg+Alt+F5","taste":"Strg+Alt+F6","pin":"demo","setup":"a"}]`))},
		{name: "kein Array", bad: true, manifest: string(manifestWithKeys(`{"taste":"Strg+Alt+F5","pin":"demo","setup":"a"}`))},
		{name: "Zeichenkette statt Eintrag", bad: true, manifest: string(manifestWithKeys(`["Strg+Alt+F5"]`))},
		{name: "doppeltes Feld tasten", bad: true, manifest: `{"tasten":[],"tasten":` + oneKey + `}`},
		{name: "Manifest kein Objekt", bad: true, manifest: `[]`},
		{name: "Manifest kein JSON", bad: true, manifest: `{"tasten":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHotkeys([]byte(tt.manifest))
			if tt.bad {
				if err == nil || len(got) != 0 {
					t.Fatalf("ungültig akzeptiert: %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("%v, erwartet %v", got, tt.want)
			}
		})
	}
}

func twelveKeys() string {
	var parts []string
	for i := 1; i <= 12; i++ {
		parts = append(parts, fmt.Sprintf(`{"taste":"Strg+Alt+F%d","pin":"demo","setup":"s%d"}`, i, i))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func twelveHotkeys() []Hotkey {
	var out []Hotkey
	for i := 1; i <= 12; i++ {
		out = append(out, Hotkey{Key: fmt.Sprintf("Strg+Alt+F%d", i), Pin: "demo", Setup: fmt.Sprintf("s%d", i)})
	}
	return out
}

func prepared(t *testing.T, files map[string][]byte) (*AppStore, *State, App, *Reader) {
	t.Helper()
	store, st := newStore(t)
	app, raw := buildApp(t, files, "")
	if err := store.Prepare(raw, app); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(st.Root, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	return store, st, app, r
}

// Das Kürzel liegt im Siegel, nicht im Ordner der WebView.
func TestTastenVersiegeltUndNichtImWebViewOrdner(t *testing.T) {
	store, st, app, r := prepared(t, appWithManifest(manifestWithKeys(oneKey)))
	got := r.Hotkeys(app)
	if len(got) != 1 || got[0] != (Hotkey{Key: "Strg+Alt+F5", Pin: "demo", Setup: "ansicht"}) {
		t.Fatalf("Kürzel: %v", got)
	}
	dir, err := store.Open(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "jnpt.app.json")); err == nil {
		t.Error("jnpt.app.json im WebView-Ordner")
	}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "Strg+Alt") || strings.Contains(string(b), "tasten") {
			t.Errorf("Kürzel in %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Das Siegel liegt im Zustand der Wurzel, nicht im App-Ordner.
	if _, err = os.Stat(filepath.Join(st.Root, sealName(app))); err != nil {
		t.Fatalf("Siegel fehlt: %v", err)
	}
	// Der Weg über das Siegel liefert dasselbe auch für den Läufer-Zustand.
	var seal appSeal
	if err = st.Load(sealName(app), &seal); err != nil || len(seal.Hotkeys) != 1 {
		t.Fatalf("Siegel trägt die Kürzel nicht: %v %+v", err, seal)
	}
}

func TestAppOhneTastenOderOhneManifestHatKeineKuerzel(t *testing.T) {
	noManifest := goodApp()
	delete(noManifest, "jnpt.app.json")
	nested := goodApp()
	nested["sub/jnpt.app.json"] = manifestWithKeys(oneKey)
	for name, files := range map[string]map[string][]byte{
		"Feld fehlt":                  goodApp(),
		"ohne jnpt.app.json":          noManifest,
		"nur im Unterordner":          nested,
		"leere Liste":                 appWithManifest(manifestWithKeys(`[]`)),
		"Manifest nicht auswertbar":   appWithManifest([]byte(`{"tasten":`)),
		"Kürzel ungültig im Paket":    appWithManifest(manifestWithKeys(`[{"taste":"Strg+Alt+Q","pin":"demo","setup":"a"}]`)),
		"Doppel im Paket":             appWithManifest(manifestWithKeys(`[{"taste":"Strg+Alt+F5","pin":"a","setup":"b"},{"taste":"Strg+Alt+F5","pin":"a","setup":"c"}]`)),
		"unbekanntes Feld im Eintrag": appWithManifest(manifestWithKeys(`[{"taste":"Strg+Alt+F5","pin":"demo","setup":"a","x":1}]`)),
	} {
		t.Run(name, func(t *testing.T) {
			store, _, app, r := prepared(t, files)
			// Ein ungültiges Feld sperrt nur die Kürzel; die App öffnet wie bisher.
			if _, err := store.Open(app); err != nil {
				t.Fatalf("App öffnet nicht: %v", err)
			}
			if got := r.Hotkeys(app); len(got) != 0 {
				t.Fatalf("Kürzel: %v", got)
			}
		})
	}
}

// Ein Paket, dessen jnpt.app.json nicht zum Commit passt (manipuliertes tasten), wird abgewiesen:
// Es entsteht weder eine Ablage noch ein Siegel noch ein Kürzel.
func TestManipuliertesTastenImPaketScheitertAmHash(t *testing.T) {
	store, st := newStore(t)
	genuine := appWithManifest(manifestWithKeys(`[]`))
	forged := appWithManifest(manifestWithKeys(oneKey))
	commit, _ := testfixture.PackageAt("apps/demo", genuine, "")
	_, forgedRaw := testfixture.PackageAt("apps/demo", forged, "")
	app := App{Name: "demo", Commit: commit, Path: "apps/demo"}
	if err := store.Prepare(forgedRaw, app); err == nil {
		t.Fatal("manipuliertes Paket akzeptiert")
	}
	r, _ := NewReader(st.Root, testCipher{})
	if got := r.Hotkeys(app); len(got) != 0 {
		t.Fatalf("Kürzel aus manipuliertem Paket: %v", got)
	}
	if _, err := os.Stat(filepath.Join(st.Root, "apps", "demo")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(st.Root, "apps", "demo"))
		if len(entries) != 0 {
			t.Fatalf("Reste: %v", entries)
		}
	}
}

// Das Siegel prüft der Reader vor jedem Kürzel: kaputter Ordner, kaputtes Siegel, fehlendes Siegel.
func TestTastenOhneGueltigesSiegelGebenKeinKuerzel(t *testing.T) {
	tamper := map[string]func(t *testing.T, st *State, app App, dir string){
		"Datei geändert": func(t *testing.T, _ *State, _ App, dir string) {
			_ = os.WriteFile(filepath.Join(dir, "app.js"), []byte("alert(1)"), 0o600)
		},
		"Datei gelöscht": func(t *testing.T, _ *State, _ App, dir string) { _ = os.Remove(filepath.Join(dir, "app.js")) },
		"Datei zusätzlich": func(t *testing.T, _ *State, _ App, dir string) {
			_ = os.WriteFile(filepath.Join(dir, "evil.js"), []byte("1"), 0o600)
		},
		"Führendes CSP-Literal entfernt": func(t *testing.T, _ *State, _ App, dir string) {
			_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html><head></head><body></body></html>"), 0o600)
		},
		"Siegel gelöscht": func(t *testing.T, st *State, app App, _ string) {
			_ = os.Remove(filepath.Join(st.Root, sealName(app)))
		},
		"Siegel mit anderem Namen": func(t *testing.T, st *State, app App, _ string) {
			if err := st.Save(sealName(app), appSeal{Name: "anders", Commit: app.Commit, Hotkeys: []Hotkey{{Key: "Strg+Alt+F5", Pin: "demo", Setup: "ansicht"}}}); err != nil {
				t.Fatal(err)
			}
		},
		"Siegel mit ungültigem Kürzel": func(t *testing.T, st *State, app App, _ string) {
			var seal appSeal
			if err := st.Load(sealName(app), &seal); err != nil {
				t.Fatal(err)
			}
			seal.Hotkeys = []Hotkey{{Key: "Strg+Alt+7", Pin: "demo", Setup: "ansicht"}}
			if err := st.Save(sealName(app), seal); err != nil {
				t.Fatal(err)
			}
		},
		"Siegel mit Doppel": func(t *testing.T, st *State, app App, _ string) {
			var seal appSeal
			if err := st.Load(sealName(app), &seal); err != nil {
				t.Fatal(err)
			}
			seal.Hotkeys = append(seal.Hotkeys, seal.Hotkeys[0])
			if err := st.Save(sealName(app), seal); err != nil {
				t.Fatal(err)
			}
		},
		"Siegel mit Pin außerhalb des Musters": func(t *testing.T, st *State, app App, _ string) {
			var seal appSeal
			if err := st.Load(sealName(app), &seal); err != nil {
				t.Fatal(err)
			}
			seal.Hotkeys[0].Pin = "exit:x"
			if err := st.Save(sealName(app), seal); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, f := range tamper {
		t.Run(name, func(t *testing.T) {
			store, st, app, r := prepared(t, appWithManifest(manifestWithKeys(oneKey)))
			if len(r.Hotkeys(app)) != 1 {
				t.Fatal("Ausgangslage ohne Kürzel")
			}
			dir, err := store.Open(app)
			if err != nil {
				t.Fatal(err)
			}
			f(t, st, app, dir)
			if got := r.Hotkeys(app); len(got) != 0 {
				t.Fatalf("Kürzel trotz kaputtem Siegel: %v", got)
			}
		})
	}
}

// Ein Siegel aus einem älteren Läufer kennt kein tasten: kein Kürzel, kein Fehler.
func TestAltesSiegelOhneTastenGibtKeineKuerzel(t *testing.T) {
	_, st, app, r := prepared(t, goodApp())
	var seal appSeal
	if err := st.Load(sealName(app), &seal); err != nil {
		t.Fatal(err)
	}
	if len(seal.Hotkeys) != 0 || len(r.Hotkeys(app)) != 0 {
		t.Fatal("Kürzel ohne Feld")
	}
}

// Ungültige App-Einträge bekommen nie Kürzel.
func TestHotkeysUngueltigerAppEintrag(t *testing.T) {
	_, _, app, r := prepared(t, appWithManifest(manifestWithKeys(oneKey)))
	for _, bad := range []App{{}, {Name: "../x", Commit: app.Commit, Path: "apps/../x"}, {Name: "demo", Commit: "abc", Path: "apps/demo"}, {Name: "demo", Commit: app.Commit, Path: "apps/other"}} {
		if got := r.Hotkeys(bad); len(got) != 0 {
			t.Errorf("%+v: %v", bad, got)
		}
	}
}
