package client

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

// errAppBereich: apps\ oder webview\ ist kein echtes Verzeichnis (Verweis, Datei). Darin wird nichts gelöscht.
var errAppBereich = errors.New("app_bereich")

// pruneApps räumt nach einem erfolgreich geprüften Abgleich (#1342) alles ab, was nicht mehr im Manifest steht:
// Ablage unter apps\, WebView-Profil unter webview\ und die App-Siegel. Das Manifest entscheidet, auch bei
// kurz fehlenden Apps; das Profil ist nur ein Zwischenspeicher. Best effort: Ein Fehler (typisch: Fenster
// noch offen, Dateien gesperrt) stoppt weder Abgleich noch Läufe, wird nur als fester Logcode vermerkt, und der
// nächste Abgleich versucht es erneut. Aufgerufen wird nie nach Offline-, Parse-, Signatur- oder Lesefehlern.
func (e *Engine) pruneApps(m Manifest) {
	if m.Apps == nil || e.Container != nil {
		return
	}
	keep := make(map[string]bool, len(*m.Apps))
	for _, app := range *m.Apps {
		keep[app.Name] = true
	}
	// Unter der Zustandssperre wie AppStore.Prepare: kein Rennen mit Prepare oder Revoke.
	err := e.State.Use(func() error { return pruneWithdrawn(e.State.Root, keep) })
	if err != nil && !errors.Is(err, ErrRevoked) {
		e.logCode(LogAppFault, 2)
	}
}

// pruneWithdrawn zählt die Einträge lokal auf. Pfade entstehen nie aus Servernamen: Gelöscht wird nur ein
// lokal gefundener Eintrag mit gültigem App-Namen, der nicht im Manifest steht. Reihenfolge: erst die Ablage,
// dann das Siegel, damit eine zurückkehrende App nie auf einer Ablage ohne Siegel stößt (refused/hash).
func pruneWithdrawn(root string, keep map[string]bool) error {
	var first error
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	note(pruneArea(filepath.Join(root, "apps"), keep))
	note(pruneSeals(root, keep))
	note(pruneArea(filepath.Join(root, "webview"), keep))
	return first
}

// pruneArea löscht die direkten Einträge von dir, deren Name ein gültiger, nicht mehr zugewiesener App-Name ist.
// removeTree folgt keinem Verweis; ein Eintrag, der selbst ein Verweis ist, verliert nur den Verweis.
func pruneArea(dir string, keep map[string]bool) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode().Type() != fs.ModeDir {
		return errAppBereich
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var first error
	for _, entry := range entries {
		name := entry.Name()
		if !fenster.ValidAppName(name) || keep[name] {
			continue
		}
		if err = removeTree(filepath.Join(dir, name)); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// pruneSeals löscht die Siegel app-<name>-<commit> nicht mehr zugewiesener Apps, aber nur, wenn deren Ablage
// weg ist. Bleibt die Ablage gesperrt liegen, bleibt auch das Siegel; der nächste Abgleich löscht beides.
func pruneSeals(root string, keep map[string]bool) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var first error
	for _, entry := range entries {
		name, ok := sealOwner(entry.Name())
		if !ok || keep[name] || !entry.Type().IsRegular() {
			continue
		}
		if _, err = os.Lstat(filepath.Join(root, "apps", name)); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err = removeTree(filepath.Join(root, entry.Name())); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// sealOwner liefert den App-Namen aus einem Siegelnamen app-<name>-<commit> (siehe sealName), sonst false.
func sealOwner(file string) (string, bool) {
	rest, ok := strings.CutPrefix(file, "app-")
	if !ok {
		return "", false
	}
	name, commit, ok := strings.Cut(rest, "-")
	if !ok || !fenster.ValidAppName(name) || !validID("sha256", commit) {
		return "", false
	}
	return name, true
}
