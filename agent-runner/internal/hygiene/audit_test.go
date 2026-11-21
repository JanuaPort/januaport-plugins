package hygiene

import (
	"slices"
	"testing"
)

const ciWorkflow = workflowDir + "agent-runner.yml"

// JanuaPort/januaport#1438: Die Prüfung läuft auch auf Pull Requests, mit
// denselben Pfaden wie bei Push.
func TestCIRunsOnPullRequestWithModulePaths(t *testing.T) {
	w := readWorkflow(t, ciWorkflow)
	push, _ := w.On["push"].(map[string]any)
	pr, ok := w.On["pull_request"].(map[string]any)
	if !ok {
		t.Fatalf("agent-runner.yml läuft nicht bei pull_request: %v", onKeys(w))
	}
	pushPaths, prPaths := stringList(push["paths"]), stringList(pr["paths"])
	if !slices.Contains(prPaths, "agent-runner/**") || !slices.Contains(prPaths, ".github/workflows/agent-runner.yml") {
		t.Fatalf("pull_request ohne Modulpfade: %v", prPaths)
	}
	slices.Sort(pushPaths)
	slices.Sort(prPaths)
	if !slices.Equal(pushPaths, prPaths) {
		t.Fatalf("Pfade weichen ab: push %v, pull_request %v", pushPaths, prPaths)
	}
	if !slices.Contains(onKeys(w), "push") {
		t.Fatal("Push-Auslöser entfernt")
	}
}
