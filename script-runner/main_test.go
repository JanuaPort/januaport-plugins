package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitLibsCreatesModesAndPreservesCache(t *testing.T) {
	root := t.TempDir()
	if err := initLibs(root); err != nil { t.Fatal(err) }
	for _, name := range []string{"ready", "staging"} {
		if err := os.Chmod(filepath.Join(root, name), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	kept := filepath.Join(root, "ready", "existing-cache")
	if err := os.WriteFile(kept, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := initLibs(root); err != nil {
			t.Fatal(err)
		}
		for name, mode := range map[string]os.FileMode{"ready": 0o755, "staging": 0o700} {
			info, err := os.Stat(filepath.Join(root, name))
			if err != nil || !info.IsDir() || info.Mode().Perm() != mode {
				t.Fatalf("%s: %v, %v", name, info, err)
			}
		}
	}
	if data, err := os.ReadFile(kept); err != nil || string(data) != "keep" {
		t.Fatal("init changed existing cache")
	}
}

func TestInitLibsFailsOnBlockedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ready"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := initLibs(root); err == nil {
		t.Fatal("init succeeded despite blocked ready directory")
	}
}
