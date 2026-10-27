//go:build linux

package platform

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
)

func TestLinuxDateirechteJournalUndIntegritaet(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := SecureRoot(root); err != nil {
		t.Fatal(err)
	}
	cipher, err := NewFileIntegrity(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := client.OpenState(root, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Save("credential", map[string]string{"token": "SYNTHETIC_DEVICE_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	if err = st.Mark(client.Result{JobID: "probe"}, "started"); err != nil {
		t.Fatal(err)
	}
	st, err = client.OpenState(root, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Recover(time.Now()); err != nil || st.Journal().Results[0].End != "interrupted" {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		fi, _ := entry.Info()
		if fi.Mode().Perm() != 0600 {
			t.Fatal(entry.Name(), fi.Mode())
		}
	}
	info, _ := os.Stat(root)
	if info.Mode().Perm() != 0700 {
		t.Fatal(info.Mode())
	}
	path := filepath.Join(root, "credential")
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte("SYNTHETIC_DEVICE_TOKEN")) {
		t.Fatal("Linux-Datei unerwartet verschlüsselt")
	}
	raw[bytes.Index(raw, []byte("SYNTHETIC_DEVICE_TOKEN"))] = 'X'
	os.WriteFile(path, raw, 0600)
	var v map[string]string
	if st.Load("credential", &v) == nil {
		t.Fatal("Manipulation akzeptiert")
	}
}

func TestLinuxDateiUndSperreAmAdapter(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	cipher, err := NewFileIntegrity(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := client.OpenState(root, cipher)
	if err != nil {
		t.Fatal(err)
	}
	st.Save("credential", map[string]string{"token": "synthetic"})
	path := filepath.Join(root, "credential")
	os.Chmod(path, 0644)
	var v map[string]string
	if st.Load("credential", &v) == nil {
		t.Fatal("Öffentliche Credentialdatei akzeptiert")
	}
	os.Chmod(path, 0600)
	target := filepath.Join(root, "target")
	os.Rename(path, target)
	os.Symlink(target, path)
	if st.Load("credential", &v) == nil {
		t.Fatal("Credential-Symlink akzeptiert")
	}
	lockPath := filepath.Join(t.TempDir(), "runner.lock")
	unlock, err := LockFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if unlock2, e := LockFile(lockPath); e == nil {
		unlock2()
		t.Fatal("Zweiter Läufer akzeptiert")
	}
	other := filepath.Join(t.TempDir(), "lock-link")
	os.Symlink(lockPath, other)
	if f, e := LockFile(other); e == nil {
		f()
		t.Fatal("Sperr-Symlink akzeptiert")
	}
}

func TestLinuxPrivatePfadeVerweigernSymlinkUndFremdeRechte(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	os.Mkdir(target, 0700)
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if SecureRoot(link) == nil {
		t.Fatal("Symlink akzeptiert")
	}
	os.Chmod(target, 0755)
	if SecureRoot(target) == nil {
		t.Fatal("Öffentliches Verzeichnis akzeptiert")
	}
}
