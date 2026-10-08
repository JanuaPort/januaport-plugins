package hygiene

import (
	"bufio"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Wächter für die Auslieferung (JanuaPort/januaport#928, P4a): Release-Strecke,
// CI-Auslöser (E6), Deploy-Beispiele und die gepinnte gVisor-Fassung der
// runsc-Sonde. Die Nummern verweisen auf Plan P4 und die SEC-Prüfung (C1, C2).

const (
	workflowDir     = "../../../.github/workflows/"
	releaseWorkflow = workflowDir + "script-runner-release.yml"
	imageRepo       = "ghcr.io/januaport/script-runner"
)

type workflow struct {
	On          map[string]any         `yaml:"on"`
	Permissions map[string]string      `yaml:"permissions"`
	Env         map[string]string      `yaml:"env"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Needs       any               `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
	Env         map[string]string `yaml:"env"`
	Steps       []workflowStep    `yaml:"steps"`
}

type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]any    `yaml:"with"`
	Env  map[string]string `yaml:"env"`
}

func readWorkflow(t *testing.T, path string) workflow {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var w workflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return w
}

func (j workflowJob) needs() []string {
	switch n := j.Needs.(type) {
	case string:
		return []string{n}
	case []any:
		var out []string
		for _, v := range n {
			out = append(out, v.(string))
		}
		return out
	}
	return nil
}

// text fasst alles zusammen, was ein Job ausführt oder übergibt.
func (j workflowJob) text() string {
	var sb strings.Builder
	for k, v := range j.Env {
		sb.WriteString(k + "=" + v + "\n")
	}
	for _, s := range j.Steps {
		sb.WriteString(s.Uses + "\n" + s.Run + "\n")
		for k, v := range s.Env {
			sb.WriteString(k + "=" + v + "\n")
		}
		for k, v := range s.With {
			if str, ok := v.(string); ok {
				sb.WriteString(k + "=" + str + "\n")
			}
		}
	}
	return sb.String()
}

func (j workflowJob) writes() bool {
	for _, v := range j.Permissions {
		if v == "write" {
			return true
		}
	}
	return false
}

func onKeys(w workflow) []string {
	var keys []string
	for k := range w.On {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func stringList(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, e := range l {
		out = append(out, e.(string))
	}
	return out
}

// Plan P4a: Auslöser ist nur der Tag script-runner-v*, nie ein Branch.
func TestReleaseWorkflowOnlyOnTag(t *testing.T) {
	w := readWorkflow(t, releaseWorkflow)
	if got := onKeys(w); !slices.Equal(got, []string{"push"}) {
		t.Fatalf("Auslöser %v, erwartet nur push", got)
	}
	push, _ := w.On["push"].(map[string]any)
	if len(push) != 1 || !slices.Equal(stringList(push["tags"]), []string{"script-runner-v*"}) {
		t.Fatalf("push = %v, erwartet nur tags [script-runner-v*]", push)
	}
}

// Wie im Gateway: Workflow-Ebene nur lesen; Schreibrecht auf Pakete nur in
// den zwei Jobs, die nach GHCR schreiben (Bauen per Digest, Taggen).
func TestReleaseWorkflowPermissions(t *testing.T) {
	w := readWorkflow(t, releaseWorkflow)
	if len(w.Permissions) != 1 || w.Permissions["contents"] != "read" {
		t.Fatalf("Workflow-permissions = %v", w.Permissions)
	}
	var writers []string
	for name, j := range w.Jobs {
		if j.Permissions == nil {
			t.Errorf("Job %s ohne eigene permissions", name)
		}
		for scope, v := range j.Permissions {
			if v == "write" && scope != "packages" {
				t.Errorf("Job %s: %s: write", name, scope)
			}
		}
		if j.writes() {
			writers = append(writers, name)
		}
	}
	slices.Sort(writers)
	if !slices.Equal(writers, []string{"build", "publish"}) {
		t.Fatalf("Jobs mit Schreibrecht %v, erwartet [build publish]", writers)
	}
}

// SEC C2: Im Job mit packages: write läuft keine Aktion eines Dritten — nur
// actions/* und docker/*. Trivy läuft nur in einem Job ohne Schreibrecht, als
// Binary mit gepinnter Version und SHA-256.
func TestReleaseWorkflowC2NoThirdPartyWithWrite(t *testing.T) {
	w := readWorkflow(t, releaseWorkflow)
	trivyJobs := 0
	for name, j := range w.Jobs {
		hasTrivy := strings.Contains(strings.ToLower(j.text()), "trivy")
		if !j.writes() {
			if hasTrivy {
				trivyJobs++
				checkTrivyPinned(t, name, j)
			}
			continue
		}
		if hasTrivy {
			t.Errorf("Job %s mit Schreibrecht nennt Trivy", name)
		}
		for _, s := range j.Steps {
			if s.Uses != "" && !strings.HasPrefix(s.Uses, "actions/") && !strings.HasPrefix(s.Uses, "docker/") {
				t.Errorf("Job %s mit Schreibrecht nutzt %s", name, s.Uses)
			}
		}
	}
	if trivyJobs != 1 {
		t.Fatalf("Trivy in %d Jobs ohne Schreibrecht, erwartet 1", trivyJobs)
	}
}

func checkTrivyPinned(t *testing.T, name string, j workflowJob) {
	t.Helper()
	if !regexp.MustCompile(`TRIVY_VERSION=\d+\.\d+\.\d+\n`).MatchString(j.text()) {
		t.Errorf("Job %s: TRIVY_VERSION nicht gepinnt", name)
	}
	if !regexp.MustCompile(`TRIVY_SHA256=[0-9a-f]{64}\n`).MatchString(j.text()) {
		t.Errorf("Job %s: TRIVY_SHA256 fehlt", name)
	}
	if !strings.Contains(j.text(), "sha256sum -c") {
		t.Errorf("Job %s: Prüfsumme wird nicht geprüft", name)
	}
	for _, s := range j.Steps {
		if strings.Contains(strings.ToLower(s.Uses), "trivy") {
			t.Errorf("Job %s: Trivy als Aktion %s", name, s.Uses)
		}
	}
}

// Jede Aktion der Release-Strecke ist per voller Commit-SHA gepinnt.
func TestReleaseWorkflowUsesPinnedBySHA(t *testing.T) {
	w := readWorkflow(t, releaseWorkflow)
	pinned := regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+@[0-9a-f]{40}$`)
	n := 0
	for name, j := range w.Jobs {
		for _, s := range j.Steps {
			if s.Uses == "" {
				continue
			}
			n++
			if !pinned.MatchString(s.Uses) {
				t.Errorf("Job %s: %s nicht per Commit-SHA gepinnt", name, s.Uses)
			}
		}
	}
	if n == 0 {
		t.Fatal("keine Aktion gefunden")
	}
}

// Plan P4a + E7: ein Build, nur linux/amd64, SBOM und Provenance, per Digest
// gepusht und ohne Tag — getaggt wird erst nach den Sonden.
func TestReleaseWorkflowBuildPushByDigest(t *testing.T) {
	w := readWorkflow(t, releaseWorkflow)
	if w.Env["IMAGE_REPO"] != imageRepo {
		t.Fatalf("IMAGE_REPO = %q", w.Env["IMAGE_REPO"])
	}
	var builds []workflowStep
	for name, j := range w.Jobs {
		for _, s := range j.Steps {
			if strings.HasPrefix(s.Uses, "docker/build-push-action@") {
				if name != "build" {
					t.Errorf("Build im Job %s", name)
				}
				builds = append(builds, s)
			}
		}
	}
	if len(builds) != 1 {
		t.Fatalf("%d Build-Schritte, erwartet genau 1", len(builds))
	}
	with := builds[0].With
	if with["platforms"] != "linux/amd64" {
		t.Errorf("platforms = %v", with["platforms"])
	}
	if with["sbom"] != true {
		t.Errorf("sbom = %v", with["sbom"])
	}
	if with["provenance"] != "mode=max" {
		t.Errorf("provenance = %v", with["provenance"])
	}
	if _, ok := with["tags"]; ok {
		t.Error("Build setzt Tags — getaggt wird erst nach den Sonden")
	}
	out, _ := with["outputs"].(string)
	for _, want := range []string{"type=image", "push-by-digest=true", "push=true", "name=${{ env.IMAGE_REPO }}"} {
		if !strings.Contains(out, want) {
			t.Errorf("outputs ohne %q: %q", want, out)
		}
	}
}

// SEC C1 und Reihenfolge: Unit-Tests vor dem Build; die Sonden laufen gegen
// den gepushten Digest und vergleichen vorher den Config-Digest; getaggt wird
// nur nach grünen Sonden, und der Tag muss auf denselben Digest zeigen.
func TestReleaseWorkflowOrderAndC1(t *testing.T) {
	w := readWorkflow(t, releaseWorkflow)
	need := map[string][]string{
		"build":   {"unit"},
		"probes":  {"build"},
		"scan":    {"build"},
		"publish": {"build", "probes"},
	}
	for job, want := range need {
		j, ok := w.Jobs[job]
		if !ok {
			t.Fatalf("Job %s fehlt", job)
		}
		got := j.needs()
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s needs %v, erwartet %v", job, got, want)
		}
	}
	unit := w.Jobs["unit"].text()
	for _, want := range []string{"go test", "seccomp/derive.py --check"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit ohne %q", want)
		}
	}
	probes := w.Jobs["probes"].text()
	for _, want := range []string{"needs.build.outputs.digest", ".config.digest", "probe/run.sh", "P2_IMAGE", "github.run_id"} {
		if !strings.Contains(probes, want) {
			t.Errorf("probes ohne %q", want)
		}
	}
	for name, j := range w.Jobs {
		if strings.Contains(j.text(), "imagetools create") && name != "publish" {
			t.Errorf("Tag im Job %s", name)
		}
	}
	publish := w.Jobs["publish"].text()
	for _, want := range []string{"imagetools create", "sha256sum"} {
		if !strings.Contains(publish, want) {
			t.Errorf("publish ohne %q", want)
		}
	}
	// SEC H-a: Weicht der Config-Digest ab, bricht probes ab; publish bricht
	// ab, wenn der Tag nicht auf den geprüften Digest zeigt.
	if !regexp.MustCompile(`\[ "\$checked" = "\$pushed" \] \|\| \{[^}]*exit 1; \}`).MatchString(probes) {
		t.Error("probes: Config-Digest-Vergleich ohne Abbruch (exit 1)")
	}
	if !regexp.MustCompile(`\[ "\$tagged" = "\$DIGEST" \] \|\| \{[^}]*exit 1; \}`).MatchString(publish) {
		t.Error("publish: kein Abbruch, wenn der Tag nicht auf den geprüften Digest zeigt")
	}
	// Kein `latest`: Anlagen beziehen per Digest aus dem Katalog.
	for name, j := range w.Jobs {
		if strings.Contains(strings.ToLower(j.text()), "latest") {
			t.Errorf("Job %s setzt oder nennt latest", name)
		}
	}
}

// E6 (korrigiert nach SEC): script-runner.yml und kartei-rpa.yml laufen nur
// bei Push auf main und per Hand; secret-scan.yml bleibt auf jedem Push und
// jedem Pull Request (#787, öffentliches Repo).
func TestCIWorkflowTriggers(t *testing.T) {
	for _, f := range []string{"script-runner.yml", "kartei-rpa.yml"} {
		w := readWorkflow(t, workflowDir+f)
		if got := onKeys(w); !slices.Equal(got, []string{"push", "workflow_dispatch"}) {
			t.Errorf("%s: Auslöser %v", f, got)
		}
		push, _ := w.On["push"].(map[string]any)
		if !slices.Equal(stringList(push["branches"]), []string{"main"}) {
			t.Errorf("%s: push.branches = %v", f, push["branches"])
		}
	}
	w := readWorkflow(t, workflowDir+"secret-scan.yml")
	if got := onKeys(w); !slices.Equal(got, []string{"pull_request", "push", "workflow_dispatch"}) {
		t.Errorf("secret-scan.yml: Auslöser %v", got)
	}
	if w.On["push"] != nil {
		t.Errorf("secret-scan.yml: push gefiltert: %v", w.On["push"])
	}
}

func readKeyValues(t *testing.T, path string) (map[string]string, []string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	kv := map[string]string{}
	var comments []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			comments = append(comments, line)
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Errorf("%s: Zeile ohne =: %q", path, line)
			continue
		}
		kv[k] = v
	}
	return kv, comments
}

// Plan P4 Befund 6: echtes Image-Repository; ohne runtime-key bucht der
// Aktuator `unprovisioned` statt `failed`.
func TestDeployDescriptor(t *testing.T) {
	kv, _ := readKeyValues(t, "../../deploy/script-runner.conf.example")
	want := map[string]string{
		"IMAGE_REPO":   imageRepo,
		"COMPOSE_FILE": "/opt/jnpt/plugins/script-runner/docker-compose.yml",
		"SERVICE":      "script-runner",
		"ENV_FILE":     "/opt/jnpt/plugins/script-runner/.env",
		"ENV_KEY":      "JNPT_SCRIPT_RUNNER_IMAGE",
		"HEALTH_URL":   "http://127.0.0.1:8091/healthz",
		"REQUIRE":      "/opt/jnpt/plugins/script-runner/seccomp/jnpt-sandbox-seccomp.json:/run/jnpt/plugins/script-runner/runtime-key",
	}
	for k, v := range want {
		if kv[k] != v {
			t.Errorf("%s = %q, erwartet %q", k, kv[k], v)
		}
	}
	if len(kv) != len(want) {
		t.Errorf("Schlüssel %v, erwartet genau %d", kv, len(want))
	}
}

// Plan P4a: Standard runc; runsc-jnpt nur als Kommentar.
func TestEnvExample(t *testing.T) {
	kv, comments := readKeyValues(t, "../../deploy/.env.example")
	if len(kv) != 1 || kv["JNPT_SCRIPT_RUNNER_RUNTIME"] != "runc" {
		t.Fatalf("aktive Zeilen %v, erwartet nur JNPT_SCRIPT_RUNNER_RUNTIME=runc", kv)
	}
	if !slices.ContainsFunc(comments, func(c string) bool {
		return strings.Contains(c, "JNPT_SCRIPT_RUNNER_RUNTIME=runsc-jnpt")
	}) {
		t.Error("Kommentar mit JNPT_SCRIPT_RUNNER_RUNTIME=runsc-jnpt fehlt")
	}
}

// Plan P4a: Die runsc-Sonde lädt eine gepinnte gVisor-Fassung (dieselbe wie
// im Runbook) und prüft sie gegen eine SHA-512-Konstante, nie release/latest.
func TestRunscProbePinsGVisor(t *testing.T) {
	b, err := os.ReadFile("../../probe/run-runsc.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "latest") {
		t.Error("run-runsc.sh lädt latest")
	}
	for _, re := range []string{`(?m)^gvisor_release=\d{8}\.\d+$`, `(?m)^gvisor_sha512=[0-9a-f]{128}$`} {
		if !regexp.MustCompile(re).MatchString(s) {
			t.Errorf("run-runsc.sh ohne %s", re)
		}
	}
	if !strings.Contains(s, "sha512sum -c") || !strings.Contains(s, "$gvisor_sha512") {
		t.Error("run-runsc.sh prüft nicht gegen die Konstante")
	}
}
