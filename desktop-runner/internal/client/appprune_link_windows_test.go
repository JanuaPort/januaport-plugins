//go:build windows

package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// linkDir legt eine Verzeichnis-Junction an (braucht keine Symlink-Berechtigung).
func linkDir(t *testing.T, link, target string) {
	t.Helper()
	if err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).Run(); err != nil {
		t.Fatal("Test-Junction", err)
	}
}

// sperreOrdner hält eine Datei im Ordner offen. Go öffnet unter Windows ohne FILE_SHARE_DELETE; das Löschen
// scheitert dann wie bei einem Prozess, der Dateien des Profils offen hält. Grenze: Das ist kein laufendes
// msedgewebview2.exe, sondern derselbe Mechanismus (Sharing-Verletzung) mit einem Testhandle.
func sperreOrdner(t *testing.T, dir string) (freigeben func()) {
	t.Helper()
	var held *os.File
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && held == nil && d.Type().IsRegular() {
			held, err = os.Open(p)
			if err != nil {
				held = nil
			}
		}
		return nil
	})
	if held == nil {
		t.Fatal("keine Datei zum Sperren in", dir)
	}
	once := false
	release := func() {
		if !once {
			once = true
			_ = held.Close()
		}
	}
	t.Cleanup(release)
	return release
}
