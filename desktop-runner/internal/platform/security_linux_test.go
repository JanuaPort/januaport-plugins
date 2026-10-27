//go:build linux

package platform

import (
	"bytes"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	raw[len(raw)-2] ^= 1
	os.WriteFile(path, raw, 0600)
	var v map[string]string
	if st.Load("credential", &v) == nil {
		t.Fatal("Manipulation akzeptiert")
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
