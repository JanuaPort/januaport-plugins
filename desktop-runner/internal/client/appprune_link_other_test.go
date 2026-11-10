//go:build !windows

package client

import (
	"os"
	"path/filepath"
	"testing"
)

func linkDir(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("Symlink nicht anlegbar:", err)
	}
}

// sperreOrdner ersetzt die Windows-Sperre durch ein Rechteproblem: Der Ordner, der eine Datei enthält, wird
// schreibgeschützt, sodass das Entfernen der Datei scheitert. Grenze: Das simuliert nur "Löschen schlägt fehl und
// ist später wieder möglich", nicht das Sperrverhalten eines laufenden WebView2-Prozesses. Als root sperrt das
// nicht; dann wird der Test übersprungen.
func sperreOrdner(t *testing.T, dir string) (freigeben func()) {
	t.Helper()
	var parent string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && parent == "" && d.Type().IsRegular() {
			parent = filepath.Dir(p)
		}
		return nil
	})
	if parent == "" {
		t.Fatal("keine Datei zum Sperren in", dir)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Skip("Chmod nicht möglich:", err)
	}
	release := func() { _ = os.Chmod(parent, 0o700) }
	t.Cleanup(release)
	probe := filepath.Join(parent, ".sperrprobe")
	if f, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
		_ = f.Close()
		_ = os.Remove(probe)
		release()
		t.Skip("Rechteentzug greift nicht (root): Sperre nicht simulierbar")
	}
	return release
}
