package client

import (
	"gopkg.in/yaml.v3"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseUndWindowsCI(t *testing.T) {
	b, e := os.ReadFile("../../../.github/workflows/desktop-runner-release.yml")
	if e != nil {
		t.Fatal(e)
	}
	var w map[string]any
	if yaml.Unmarshal(b, &w) != nil {
		t.Fatal("YAML")
	}
	jobs := w["jobs"].(map[string]any)
	sign := jobs["sign"].(map[string]any)
	if sign["environment"] != "release-signing" || sign["permissions"].(map[string]any)["id-token"] != "write" {
		t.Fatal("Signierberechtigung")
	}
	text := string(b)
	for _, v := range []string{"desktop-runner-v*", "^desktop-runner-v[0-9]+\\.[0-9]+\\.[0-9]+$", "GOOS: windows", "GOARCH: amd64", "CGO_ENABLED: '0'", "https://neu.codesigning.azure.net/", "signing-account-name: januaport-signing", "certificate-profile-name: ${{ env.SIGNING_PROFILE }}", "AZURE_CLIENT_ID", "AZURE_TENANT_ID", "AZURE_SUBSCRIPTION_ID", "verify /pa", "signing_profile_missing", "Authenticode-signiert", "SHA256SUMS", "actions/attest-build-provenance@", "needs: sign"} {
		if !strings.Contains(text, v) {
			t.Fatal("Release-Vertrag", v)
		}
	}
	if strings.Contains(text, "if: env.SIGNING_PROFILE") || strings.Contains(text, "UNSIGNIERT") {
		t.Fatal("Signatur muss Pflicht sein")
	}
	for _, m := range regexp.MustCompile(`uses:\s+([^\s]+)`).FindAllStringSubmatch(text, -1) {
		if strings.HasPrefix(m[1], "./") {
			continue
		}
		if !regexp.MustCompile(`^[\w/-]+@[0-9a-f]{40}$`).MatchString(m[1]) {
			t.Fatal("ungepinnte Action", m[1])
		}
	}
	b, e = os.ReadFile("../../../.github/workflows/desktop-runner.yml")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"windows-latest", "task-standard-user.ps1", "go test -count=1 ./...", "go test -race -count=1 ./...", "python-3.12.10-embed-amd64.zip", "if: failure()", "go test -count=1 -v ./internal/platform ."} {
		if !strings.Contains(string(b), v) {
			t.Fatal("Windows-CI", v)
		}
	}
}

func TestInstalltestWorkflow(t *testing.T) {
	b, err := os.ReadFile("../../../.github/workflows/desktop-runner-installtest.yml")
	if err != nil {
		t.Fatal(err)
	}
	var w map[string]any
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	on := w["on"].(map[string]any)
	if len(on) != 1 {
		t.Fatal("Installationstest ausschließlich manuell")
	}
	version := on["workflow_dispatch"].(map[string]any)["inputs"].(map[string]any)["version"].(map[string]any)
	if version["default"] != "0.1.0" || version["type"] != "string" || version["required"] != true {
		t.Fatal("Release-Auswahl")
	}
	permissions := w["permissions"].(map[string]any)
	if len(permissions) != 2 || permissions["contents"] != "read" || permissions["attestations"] != "read" {
		t.Fatal("Installationstest-Berechtigungen")
	}
	job := w["jobs"].(map[string]any)["installtest"].(map[string]any)
	if job["environment"] != "showcase-installtest" || job["runs-on"] != "windows-latest" || job["timeout-minutes"] != 15 {
		t.Fatal("Abnahmeumgebung")
	}
	if job["defaults"].(map[string]any)["run"].(map[string]any)["shell"] != "pwsh" {
		t.Fatal("Windows-Shell")
	}
	for _, raw := range job["steps"].([]any) {
		step := raw.(map[string]any)
		if uses, ok := step["uses"].(string); ok {
			if !regexp.MustCompile(`^[\w/-]+@[0-9a-f]{40}$`).MatchString(uses) {
				t.Fatal("ungepinnte Action", uses)
			}
			continue
		}
		run := step["run"].(string)
		if strings.Contains(run, "${{") || !strings.Contains(run, "-CreateStandardUser") {
			t.Fatal("Eingaben nur über Env, Standardkonto ist Pflicht")
		}
		env := step["env"].(map[string]any)
		if env["SHOWCASE_PAIR_CODE"] != "${{ secrets.SHOWCASE_PAIR_CODE }}" || env["INSTALLTEST_VERSION"] != "${{ inputs.version }}" {
			t.Fatal("Kopplungscode und Version")
		}
	}
	ci, err := os.ReadFile("../../../.github/workflows/desktop-runner.yml")
	if err != nil || !strings.Contains(string(ci), "./probe/installtest-mutations.ps1") {
		t.Fatal("Offline-Adaptertests mit Mutationen fehlen im Windows-CI")
	}
}
