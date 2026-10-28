//go:build linux

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
)

func TestLinuxKonfigurationUndDienst(t *testing.T) {
	good := `{"mode":"container","docker_path":"/usr/bin/docker","state_dir":"/var/lib/jnpt-runner/state","secrets_dir":"/etc/jnpt-runner/secrets"}`
	for _, raw := range []string{good, strings.Replace(good, `"container"`, `"python"`, 1), strings.Replace(good, `/usr/bin/docker`, `docker`, 1), strings.Replace(good, `/etc/jnpt-runner/secrets`, `/var/lib/jnpt-runner/state/secrets`, 1), strings.Replace(good, `/var/lib/jnpt-runner/state`, `/`, 1), strings.Replace(good, `"mode":`, `"unknown":1,"mode":`, 1), good + `{}`, strings.Replace(good, `/var/lib/jnpt-runner/state`, `/var/..`, 1)} {
		path := filepath.Join(t.TempDir(), "runner.json")
		os.WriteFile(path, []byte(raw), 0600)
		_, err := loadServerConfig(path)
		if (err == nil) != (raw == good) {
			t.Fatal(raw, err)
		}
	}
	b, e := os.ReadFile("systemd/jnpt-runner.service")
	if e != nil {
		t.Fatal(e)
	}
	for _, required := range []string{"User=jnpt-runner", "Group=jnpt-runner", "ProtectSystem=strict", "NoNewPrivileges=yes", "UMask=0077", "StateDirectoryMode=0700", "CapabilityBoundingSet=\n", "ReadWritePaths=/var/lib/jnpt-runner"} {
		if !strings.Contains(string(b), required) {
			t.Fatal("Diensthärtung fehlt", required)
		}
	}
	if os.Geteuid() == 0 {
		if runServer([]string{"run"}, io.Discard) != client.ErrProtocol {
			t.Fatal("Root-Läufer akzeptiert")
		}
	}
}
