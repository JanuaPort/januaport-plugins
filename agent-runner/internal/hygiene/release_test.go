package hygiene

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Wächter für die Auslieferung (JanuaPort/januaport#1099, B1.1): Release-Strecke,
// Pinning und Digest-Bindung. SEC C1/C2 entsprechen der script-runner-Strecke.

const (
	workflowDir     = "../../../.github/workflows/"
	releaseWorkflow = workflowDir + "agent-runner-release.yml"
	imageRepo       = "ghcr.io/januaport/agent-runner"
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
	Defaults    struct {
		Run struct {
			WorkingDirectory string `yaml:"working-directory"`
		} `yaml:"run"`
	} `yaml:"defaults"`
}

type workflowStep struct {
	Name  string            `yaml:"name"`
	Uses  string            `yaml:"uses"`
	Run   string            `yaml:"run"`
	With  map[string]any    `yaml:"with"`
	Env   map[string]string `yaml:"env"`
	Shell string            `yaml:"shell"`
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

// B1.1: Auslöser ist nur der Tag agent-runner-v*, nie ein Branch.
func TestReleaseWorkflowOnlyOnTag(t *testing.T) {
	w := readWorkflow(t, releaseWorkflow)
	if got := onKeys(w); !slices.Equal(got, []string{"push"}) {
		t.Fatalf("Auslöser %v, erwartet nur push", got)
	}
	push, _ := w.On["push"].(map[string]any)
	if len(push) != 1 || !slices.Equal(stringList(push["tags"]), []string{"agent-runner-v*"}) {
		t.Fatalf("push = %v, erwartet nur tags [agent-runner-v*]", push)
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
	reference := readWorkflow(t, workflowDir+"script-runner-release.yml")
	allowed := map[string]bool{}
	for _, j := range reference.Jobs {
		for _, s := range j.Steps {
			if s.Uses != "" {
				allowed[s.Uses] = true
			}
		}
	}
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
			if !allowed[s.Uses] {
				t.Errorf("Job %s: %s weicht von script-runner ab", name, s.Uses)
			}
		}
	}
	if n == 0 {
		t.Fatal("keine Aktion gefunden")
	}
}

func TestReleaseTagSchema(t *testing.T) {
	w := readWorkflow(t, releaseWorkflow)
	const schema = `^agent-runner-v[0-9]+\.[0-9]+\.[0-9]+$`
	if !strings.Contains(w.Jobs["unit"].text(), `echo "$GITHUB_REF_NAME" | grep -Eq '`+schema+`'`) {
		t.Fatal("Tag-Schema wird vor build nicht geprüft")
	}
	valid := regexp.MustCompile(schema)
	for _, tag := range []string{"main", "agent-runner-v1.2", "agent-runner-v1.2.3-rc1", "agent-runner-v1.2.3-extra", "script-runner-v1.2.3", "agent-runner-v1x2x3"} {
		if valid.MatchString(tag) {
			t.Errorf("verbotener Tag: %s", tag)
		}
	}
	if !valid.MatchString("agent-runner-v0.1.0") {
		t.Fatal("Release-Tag abgewiesen")
	}
}

func TestSmokeStateIgnored(t *testing.T) {
	b, err := os.ReadFile("../../.gitignore")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(strings.Fields(string(b)), "/probe/.smoke/") {
		t.Fatal("Sonden-Zustand ist nicht ignoriert")
	}
}

// B1.1: ein Build, nur linux/amd64, SBOM und Provenance, per Digest
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
	if with["context"] != "agent-runner" {
		t.Errorf("context = %v", with["context"])
	}
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
	if len(w.Jobs) != 5 || w.Jobs["unit"].Defaults.Run.WorkingDirectory != "agent-runner" {
		t.Fatal("Jobs oder unit-Arbeitsverzeichnis weichen ab")
	}
	for _, want := range []string{"go vet ./...", "go test -race -count=1 ./..."} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit ohne %q", want)
		}
	}
	probes := w.Jobs["probes"].text()
	foundProbe := false
	for _, s := range w.Jobs["probes"].Steps {
		if s.Shell == "pwsh" && s.Run == `./agent-runner/probe/smoke.ps1 -Image "${env:IMAGE_REPO}@${env:DIGEST}"` {
			foundProbe = true
		}
	}
	if !foundProbe {
		t.Error("PowerShell-Sonde nicht an Build-Digest gebunden")
	}
	for _, want := range []string{"needs.build.outputs.digest", ".config.digest", "probe/smoke.ps1", "${IMAGE_REPO}@${DIGEST}", "github.run_id"} {
		if !strings.Contains(probes, want) {
			t.Errorf("probes ohne %q", want)
		}
	}
	for _, want := range []string{`docker pull -q --platform linux/amd64 "$ref"`, `docker save "$ref"`, `tar -xOf - manifest.json`, `.platform.architecture == "amd64"`, `grep -c .)" = 1`, `checked=sha256:`, `pushed=$(docker buildx imagetools inspect`} {
		if !strings.Contains(probes, want) {
			t.Errorf("Config-Bindung ohne %q", want)
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
	if !strings.Contains(publish, `imagetools create -t "${IMAGE_REPO}:${version}" "${IMAGE_REPO}@${DIGEST}"`) {
		t.Error("publish verwendet nicht den Build-Digest")
	}
	scan := w.Jobs["scan"].text()
	for _, want := range []string{"--exit-code 0", "--input image.tar", "--scanners vuln", "actions/upload-artifact@"} {
		if !strings.Contains(scan, want) {
			t.Errorf("Scan ohne %q", want)
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

// Die Sonde läuft im Release gegen das ausgelieferte Image; ihre Hilfs-Images
// dürfen nicht über ein bewegliches Tag nachrutschen (Hinweis GATE, #1099).
func TestSmokeHelperImagesPinnedByDigest(t *testing.T) {
	b, err := os.ReadFile("../../probe/smoke.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, ":latest") {
		t.Fatal("smoke.ps1 nutzt ein :latest-Image")
	}
	pinned := regexp.MustCompile(`'(busybox|curlimages/curl):[0-9.]+@sha256:[0-9a-f]{64}'`)
	if got := len(pinned.FindAllString(s, -1)); got != 2 {
		t.Fatalf("erwartet zwei per Digest gepinnte Hilfs-Images, gefunden %d", got)
	}
}
