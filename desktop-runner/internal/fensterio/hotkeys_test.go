package fensterio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/statefixture"
)

// #1240 F-T: Die App-Liste trägt die Kürzel nur aus einem gültigen Siegel. Sie bleibt ohne Token und ohne Pfad.

func hotkeyManifest(keys string) []byte {
	return []byte(`{"name":"demo","fenster":"pc","modus":"b","teams":[],"werkzeuge":[],"skripte":[],"tasten":` + keys + `}`)
}

func hotkeyBed(t *testing.T, keys string) (*statefixture.Bed, *Runtime, client.App) {
	t.Helper()
	bed := statefixture.New(t, t.TempDir(), "http://127.0.0.1:1")
	app := bed.AddApp(t, "demo", map[string][]byte{"index.html": statefixture.Page("<p>x</p>"), "app.js": []byte("1"), "jnpt.app.json": hotkeyManifest(keys)})
	bed.SaveManifest(t, bed.Apps, 1, false)
	ex := NewPanelExchange(func(pipe.Message) bool { return false })
	rt, err := NewRuntime(RuntimeConfig{Root: bed.Root, Cipher: statefixture.Cipher{}, Exchange: ex.Exchange, Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	return bed, rt, app
}

const demoKeys = `[{"taste":"Strg+Alt+F5","pin":"demo","setup":"ansicht"},{"taste":"Strg+Alt+F6","pin":"demo","setup":"drucken"}]`

func TestAppListeTraegtKuerzelNurBeiGueltigemSiegel(t *testing.T) {
	bed, rt, app := hotkeyBed(t, demoKeys)
	list, err := rt.Apps()
	if err != nil || len(list.Names) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	want := []client.Hotkey{{Key: "Strg+Alt+F5", Pin: "demo", Setup: "ansicht"}, {Key: "Strg+Alt+F6", Pin: "demo", Setup: "drucken"}}
	if len(list.Hotkeys) != 2 || list.Hotkeys[0] != want[0] || list.Hotkeys[1] != want[1] {
		t.Fatalf("Kürzel: %v", list.Hotkeys)
	}
	// Ein veränderter Ordner bricht das Siegel: Die App bleibt gelistet, die Kürzel entfallen.
	if err = os.WriteFile(filepath.Join(bed.Root, "apps", "demo", app.Commit, "app.js"), []byte("2"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err = rt.Apps()
	if err != nil || len(list.Names) != 1 || len(list.Hotkeys) != 0 {
		t.Fatalf("Kürzel trotz kaputtem Siegel: %+v %v", list, err)
	}
}

func TestAppListeOhneSiegelOderOhneTastenKeineKuerzel(t *testing.T) {
	_, empty, _ := hotkeyBed(t, `[]`)
	if list, err := empty.Apps(); err != nil || len(list.Hotkeys) != 0 {
		t.Fatalf("%+v %v", list, err)
	}
	bed, rt, app := hotkeyBed(t, demoKeys)
	if err := os.Remove(filepath.Join(bed.Root, "app-demo-"+app.Commit)); err != nil {
		t.Fatal(err)
	}
	if list, err := rt.Apps(); err != nil || len(list.Names) != 1 || len(list.Hotkeys) != 0 {
		t.Fatalf("Kürzel ohne Siegel: %+v %v", list, err)
	}
}

// Fällt die App aus dem geprüften Manifest (Entzug), entfallen ihre Kürzel mit ihr.
func TestAppListeKuerzelFallenMitDerAppWeg(t *testing.T) {
	bed, rt, _ := hotkeyBed(t, demoKeys)
	bed.SaveManifest(t, nil, 2, false)
	list, err := rt.Apps()
	if err != nil || len(list.Names) != 0 || len(list.Hotkeys) != 0 {
		t.Fatalf("Kürzel ohne App: %+v %v", list, err)
	}
}

// Die Liste bleibt ohne Token und ohne Pfad, auch mit Kürzeln.
func TestAppListeMitKuerzelnOhneTraeger(t *testing.T) {
	bed, rt, _ := hotkeyBed(t, demoKeys)
	list, err := rt.Apps()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(strings.Join(list.Names, " "))
	for _, h := range list.Hotkeys {
		text += " " + strings.ToLower(h.Key+" "+h.Pin+" "+h.Setup)
	}
	for _, bad := range []string{strings.ToLower(statefixture.Token()), strings.ToLower(bed.Root), "token"} {
		if strings.Contains(text, bad) {
			t.Fatalf("Liste trägt %q", bad)
		}
	}
}

// Eine Änderung der Kürzel allein löst eine neue Meldung aus: Das Bedienfeld meldet dann alles ab und neu an.
func TestWatchMeldetAenderungDerKuerzel(t *testing.T) {
	first := AppList{Names: []string{"demo"}, Modes: map[string]string{"demo": ""}, ManifestSeq: 1, Hotkeys: []client.Hotkey{{Key: "Strg+Alt+F5", Pin: "demo", Setup: "a"}}}
	second := first
	second.Hotkeys = []client.Hotkey{{Key: "Strg+Alt+F5", Pin: "demo", Setup: "b"}}
	third := first
	third.Hotkeys = nil
	reads := []AppList{first, first, second, second, third}
	var mu sync.Mutex
	i := 0
	read := func() (AppList, error) {
		mu.Lock()
		defer mu.Unlock()
		l := reads[min(i, len(reads)-1)]
		i++
		return l, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan AppList, 10)
	go Watch(ctx, 5*time.Millisecond, read, func(l AppList) { got <- l })
	var seen []AppList
	deadline := time.After(3 * time.Second)
	for len(seen) < 3 {
		select {
		case l := <-got:
			seen = append(seen, l)
		case <-deadline:
			t.Fatalf("nur %d Meldungen: %+v", len(seen), seen)
		}
	}
	if seen[0].Hotkeys[0].Setup != "a" || seen[1].Hotkeys[0].Setup != "b" || len(seen[2].Hotkeys) != 0 {
		t.Fatalf("Meldungen: %+v", seen)
	}
	// Unveränderte Stände melden nichts doppelt.
	select {
	case l := <-got:
		t.Fatalf("zusätzliche Meldung: %+v", l)
	case <-time.After(100 * time.Millisecond):
	}
}
