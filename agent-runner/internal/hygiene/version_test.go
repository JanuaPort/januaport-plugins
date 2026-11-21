package hygiene

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/agent-runner/internal/runner"
)

// JanuaPort/januaport#1436: Der Release-Tag muss zur Versionskonstante passen.
// Der Test führt den Schritt aus dem Workflow selbst aus, nicht eine Kopie.
func TestReleaseTagMatchesVersionConstant(t *testing.T) {
	w := readWorkflow(t, releaseWorkflow)
	var script string
	var names []string
	for _, s := range w.Jobs["unit"].Steps {
		names = append(names, s.Name)
		if strings.Contains(s.Name, "Versionskonstante") {
			script = s.Run
		}
	}
	if script == "" {
		t.Fatal("unit-Job prüft den Tag nicht gegen die Versionskonstante")
	}
	if slices.Index(names, "go test") < slices.IndexFunc(names, func(n string) bool { return strings.Contains(n, "Versionskonstante") }) {
		t.Fatal("Versionsprüfung muss vor den Tests laufen")
	}
	for _, tc := range []struct {
		tag string
		ok  bool
	}{
		{"agent-runner-v" + runner.Version, true},
		{"agent-runner-v0.0.1", false},
		{"agent-runner-v" + runner.Version + "1", false},
		{"agent-runner-v1" + runner.Version, false},
	} {
		cmd := exec.Command("sh", "-c", script)
		cmd.Dir = "../.."
		cmd.Env = append(os.Environ(), "GITHUB_REF_NAME="+tc.tag)
		out, err := cmd.CombinedOutput()
		if (err == nil) != tc.ok {
			t.Errorf("Tag %s: ok=%v, erwartet %v (%s)", tc.tag, err == nil, tc.ok, out)
		}
	}
}

// Die Betreiberdoku nennt die Version, die der Läufer meldet.
func TestDocsNameVersionConstant(t *testing.T) {
	for _, name := range []string{"../../README.md", "../../CLAUDE.md"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), runner.Version) {
			t.Errorf("%s nennt die Version %s nicht", name, runner.Version)
		}
	}
	b, _ := os.ReadFile("../../README.md")
	if first, _, _ := strings.Cut(string(b), "\n"); !strings.Contains(first, runner.Version) {
		t.Errorf("README Zeile 1 nennt nicht %s: %s", runner.Version, first)
	}
}
