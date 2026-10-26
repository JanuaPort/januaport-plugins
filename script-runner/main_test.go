package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitLibsCreatesModesAndPreservesCache(t *testing.T) {
	root := t.TempDir()
	if err := initLibs(root); err != nil {
		t.Fatal(err)
	}
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

func TestMaxWallEnv(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  int
	}{{"", 86400}, {"1", 1}, {"300", 300}, {"86400", 86400}} {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("JNPT_SCRIPT_RUNNER_MAX_WALL_S", tt.value)
			got, err := maxWallFromEnv()
			if err != nil || got != tt.want {
				t.Fatalf("got=%d err=%v", got, err)
			}
		})
	}
}

func TestMediatorRejectsInvalidMaxWallEnv(t *testing.T) {
	for _, value := range []string{"0", "-1", "86401", "1.5", "secret-invalid", "999999999999999999999999"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("JNPT_SCRIPT_RUNNER_MAX_WALL_S", value)
			if _, err := maxWallFromEnv(); err == nil || err.Error() != "JNPT_SCRIPT_RUNNER_MAX_WALL_S nicht in 1..86400" {
				t.Fatal(err)
			}
			if code := runMediator(); code != 1 {
				t.Fatalf("invalid env started mediator: %d", code)
			}
		})
	}
}
