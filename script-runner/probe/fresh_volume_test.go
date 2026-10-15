//go:build docker

package probe

import (
	"strings"
	"testing"
)

// Unlike setup's `up` of all services, first start ONLY the sandbox and its
// declared dependencies. No mediator may prepopulate or clean the volume.
// Then also exercise the operator's `up -d script-runner` entry point fresh.
func TestFreshLibsVolumeStartup(t *testing.T) {
	proj := prefix + "-fresh"
	for _, entry := range []string{"script-runner-sandbox", "script-runner"} {
		t.Run(entry, func(t *testing.T) {
			if out, err := compose(proj, mainFiles, "down", "-v", "--remove-orphans", "-t", "1"); err != nil {
				t.Fatalf("fresh down: %v %s", err, out)
			}
			defer func() { _, _ = compose(proj, mainFiles, "down", "-v", "--remove-orphans", "-t", "1") }()
			if out := must(t, "docker", "volume", "ls", "-q", "--filter", "name=^"+proj+"_libs$"); strings.TrimSpace(out) != "" {
				t.Fatal("libs volume survived down -v")
			}
			if out, err := compose(proj, mainFiles, "up", "-d", entry); err != nil {
				t.Fatalf("cold compose up %s: %v\n%s", entry, err, out)
			}
			sb := proj + "-script-runner-sandbox-1"
			if state := strings.TrimSpace(must(t, "docker", "inspect", "-f", "{{.State.Running}}", sb)); state != "true" {
				t.Fatalf("sandbox not running: %s", state)
			}
			if out := strings.TrimSpace(must(t, "docker", "exec", sb, "python3", "-I", "-c", "import os,json; print(json.dumps(os.listdir('/libs')))")); out != "[]" {
				t.Fatalf("fresh sandbox /libs is not empty: %s", out)
			}
			if state := strings.TrimSpace(must(t, "docker", "inspect", "-f", "{{.State.Status}} {{.State.ExitCode}}", proj+"-script-runner-init-1")); state != "exited 0" {
				t.Fatalf("init not completed successfully: %s", state)
			}
			if entry == "script-runner-sandbox" {
				if out, err := compose(proj, mainFiles, "up", "-d", "script-runner"); err != nil {
					t.Fatalf("operator entry after sandbox-first: %v %s", err, out)
				}
			}
			t.Logf("fresh_volume/%s: down -v, init exited 0, sandbox running, /libs=[]", entry)
		})
	}
}
