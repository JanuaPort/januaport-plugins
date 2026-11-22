package client

import (
	"os"
	"path/filepath"
	"strings"
)

// pruneEnvironments räumt nach einem erfolgreichen, geprüften Sync (#1537) Umgebungen, Pakete und Siegel ab, deren
// Schlüssel nicht im geprüften Manifest steht: envs\<key>, packages\<key> und env-<key>. Der Schlüssel enthält den
// Commit, ein neuer Commit lässt den alten also unbenutzbar zurück; ohne das wüchse die Platte mit jeder
// Pin-Version. m ist das Manifest dieses Syncs, nicht die Platte: Der Aufruf kommt aus Sync selbst und damit nie
// gegen pinEnvironment, das ihre Ablagen anlegt. Einen Heartbeat-Takt wie bei den Apps gibt es deshalb bewusst nicht;
// eine gesperrte Umgebung bleibt bis zum nächsten Sync (Manifestwechsel, Presync, Start) liegen.
//
// Der Schlüssel eines laufenden Jobs bleibt immer (holdEnv). Ein Pin im Manifest zählt auch ohne Umgebung als
// zugewiesen: Seine Reste (Abbruch vor dem Siegel) ersetzt der nächste Pin-Schritt (Invariante 25).
func (e *Engine) pruneEnvironments(m Manifest) {
	if e.Container != nil {
		return
	}
	keep := make(map[string]bool, len(m.Pins))
	for _, p := range m.Pins {
		if p.Container == nil {
			keep[envKey(p)] = true
		}
	}
	err := e.State.Use(func() error {
		if key := e.heldEnv(); key != "" {
			keep[key] = true
		}
		return pruneEnvStore(e.State.Root, keep)
	})
	e.noteFault(&e.envPruneFault, err)
}

// pruneEnvStore zählt die Einträge lokal auf (nie aus Servernamen) und löscht nur gültige, nicht gehaltene
// Schlüssel. Reihenfolge: erst das Siegel, dann Paket und Umgebung. Jeder Zwischenstand heilt sich selbst: Ohne
// Siegel baut der nächste Pin-Schritt die Umgebung neu (ersetzt ein vorhandenes Verzeichnis, Invariante 25), und
// ein Siegel ohne Verzeichnis, das ihn dauerhaft sperrte, entsteht nie.
func pruneEnvStore(root string, keep map[string]bool) error {
	var first error
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	stale := func(name string) bool { return validEnvKey(name) && !keep[name] }
	note(pruneEnvSeals(root, stale))
	note(pruneArea(filepath.Join(root, "packages"), stale))
	note(pruneArea(filepath.Join(root, "envs"), stale))
	return first
}

// pruneEnvSeals löscht die Siegel env-<key> der Schlüssel, für die stale gilt.
func pruneEnvSeals(root string, stale func(key string) bool) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var first error
	for _, entry := range entries {
		key, ok := strings.CutPrefix(entry.Name(), "env-")
		if !ok || !stale(key) || !entry.Type().IsRegular() {
			continue
		}
		if err = removeTree(filepath.Join(root, entry.Name())); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// validEnvKey: envKey(p) ist <Commit>-<SHA-256 des Pfads>, der Commit 40 (SHA-1) oder 64 (SHA-256) kleine Hex-Zeichen.
func validEnvKey(name string) bool {
	commit, path, ok := strings.Cut(name, "-")
	return ok && (validID("sha1", commit) || validID("sha256", commit)) && validID("sha256", path)
}
