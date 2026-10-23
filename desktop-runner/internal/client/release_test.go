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
	for _, v := range []string{"desktop-runner-v*", "^desktop-runner-v[0-9]+\\.[0-9]+\\.[0-9]+$", "GOOS: windows", "GOARCH: amd64", "CGO_ENABLED: '0'", "https://neu.codesigning.azure.net/", "signing-account-name: januaport-signing", "certificate-profile-name: ${{ env.SIGNING_PROFILE }}", "AZURE_CLIENT_ID", "AZURE_TENANT_ID", "AZURE_SUBSCRIPTION_ID", "verify /pa", "UNSIGNIERT: SIGNING_PROFILE fehlt", "SHA256SUMS", "actions/attest-build-provenance@", "needs: sign"} {
		if !strings.Contains(text, v) {
			t.Fatal("Release-Vertrag", v)
		}
	}
	if strings.Count(text, "if: env.SIGNING_PROFILE != ''") != 2 {
		t.Fatal("optionale Signatur")
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
