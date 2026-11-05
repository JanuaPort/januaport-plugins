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
	stepText := func(job, marker string) string {
		t.Helper()
		for _, raw := range jobs[job].(map[string]any)["steps"].([]any) {
			s := raw.(map[string]any)
			name, _ := s["name"].(string)
			if strings.Contains(name, marker) {
				if run, ok := s["run"].(string); ok {
					return run
				}
				return s["with"].(map[string]any)["script"].(string)
			}
		}
		t.Fatal("Release-Schritt fehlt", job, marker)
		return ""
	}
	buildText := stepText("build", "Tag prüfen")
	verifyText := stepText("sign", "Signatursonde")
	for _, exe := range []string{"desktop-runner.exe", "jnpt-bedienfeld.exe"} {
		if !strings.Contains(buildText, "-o out/"+exe) {
			t.Fatal("Exe nicht gebaut", exe)
		}
		for _, required := range []string{"Test-Path -LiteralPath release/" + exe, "verify /pa release/" + exe, "Get-FileHash release/" + exe, "*" + exe + "`n"} {
			if !strings.Contains(verifyText, required) {
				t.Fatal("Existenz/Signatur/Summen", exe, required)
			}
		}
		if !regexp.MustCompile(`verify /pa release/` + regexp.QuoteMeta(exe) + `\s+if \(\$LASTEXITCODE -ne 0\) \{ throw '[a-z_]+invalid' \}`).MatchString(verifyText) {
			t.Fatal("Signaturfehler wird nicht abgewiesen", exe)
		}
	}
	if !strings.Contains(buildText, "./cmd/bedienfeld") {
		t.Fatal("Bedienfeld-Build fehlt")
	}
	// #1240: Beide Exe sind GUI-Programme; als Konsolenprogramm öffnet die Benutzeraufgabe ein Terminalfenster.
	builds := 0
	for _, line := range strings.Split(buildText, "\n") {
		if !strings.Contains(line, "go build") {
			continue
		}
		builds++
		if !strings.Contains(line, "-H=windowsgui") {
			t.Fatal("Build ohne -H=windowsgui:", strings.TrimSpace(line))
		}
	}
	if builds != 2 {
		t.Fatal("erwartet genau zwei Build-Zeilen, gefunden", builds)
	}
	if !regexp.MustCompile(`-X main\.version=\$releaseVersion[^\n]*-o out/desktop-runner\.exe`).MatchString(buildText) {
		t.Fatal("Läufer-Version wird nicht eingebettet")
	}
	for _, job := range []string{"build", "publish"} {
		found := false
		for _, raw := range jobs[job].(map[string]any)["steps"].([]any) {
			s := raw.(map[string]any)
			uses, _ := s["uses"].(string)
			if (job == "build" && strings.Contains(uses, "upload-artifact@")) || (job == "publish" && strings.Contains(uses, "attest-build-provenance@")) {
				key := "path"
				if job == "publish" {
					key = "subject-path"
				}
				v := s["with"].(map[string]any)[key].(string)
				for _, exe := range []string{"desktop-runner.exe", "jnpt-bedienfeld.exe"} {
					if !strings.Contains(v, exe) {
						t.Fatal(job, "Artefakt/Provenance fehlt", exe)
					}
				}
				found = true
			}
		}
		if !found {
			t.Fatal(job, "Artefakt/Provenance fehlt")
		}
	}
	if !strings.Contains(stepText("publish", "Release mit Provenance"), "'jnpt-bedienfeld.exe'") {
		t.Fatal("Bedienfeld nicht veröffentlicht")
	}
	sign := jobs["sign"].(map[string]any)
	signActions := 0
	for _, raw := range sign["steps"].([]any) {
		step := raw.(map[string]any)
		uses, _ := step["uses"].(string)
		if strings.HasPrefix(uses, "azure/artifact-signing-action@") {
			signActions++
			with := step["with"].(map[string]any)
			if with["files-folder"] != `${{ github.workspace }}\release` || with["files-folder-filter"] != "exe" || step["if"] != nil {
				t.Fatal("Azure muss beide Exe verpflichtend signieren")
			}
		}
	}
	if signActions != 1 {
		t.Fatal("Vorhandene Azure-Signatur fehlt")
	}
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
