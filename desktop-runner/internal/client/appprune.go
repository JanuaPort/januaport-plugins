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

// pruneStored räumt gegen das zuletzt erfolgreich geprüfte, gespeicherte Manifest ab (#1342): nach jedem Sync und im
// Takt des Heartbeats, auch bei unveränderter Sequenz. Der Entzug kommt mit einem neuen Manifest, das Fenster
// schließt das Bedienfeld aber erst danach; zum Zeitpunkt des Sync ist das Profil deshalb oft noch gesperrt und der
// Takt löscht es später. Keine Netzanfrage: Quelle ist dieselbe wie bei den Läufen (e.view()). Ist das Manifest
// leer oder nicht prüfbar (Signatur, Gerät, Format), wird nichts gelöscht.
func (e *Engine) pruneStored() {
	if e.Container != nil {
		return
	}
	// Das Manifest wird unter der Zustandssperre gelesen: Ein Prepare derselben Sperre sieht damit nie ein älteres
	// Manifest als das, gegen das es abgelegt hat, und der Takt löscht keine frisch abgelegte App.
	err := e.State.Use(func() error {
		m, ok := e.storedManifest()
		if !ok || m.Apps == nil {
			return nil
		}
		keep := make(map[string]bool, len(*m.Apps))
		for _, app := range *m.Apps {
			keep[app.Name] = true
		}
		return pruneWithdrawn(e.State.Root, keep)
	})
	e.notePruneResult(err)
}

func (e *Engine) storedManifest() (Manifest, bool) {
	b := e.view()
	if len(b.Manifest.Envelope.Manifest) == 0 {
		return Manifest{}, false
	}
	m, err := VerifyManifest(b.Manifest.Envelope, e.Credential.Pubkey, e.Credential.DeviceID, 0)
	return m, err == nil
}

// notePruneResult loggt den festen Code `10 2` nur beim ersten Fehlschlag in Folge, nicht bei jedem Takt. Ein
// Widerruf ist kein App-Fehler.
func (e *Engine) notePruneResult(err error) {
	if errors.Is(err, ErrRevoked) {
		return
	}
	e.mu.Lock()
	first := err != nil && !e.pruneFault
	e.pruneFault = err != nil
	e.mu.Unlock()
	if first {
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
