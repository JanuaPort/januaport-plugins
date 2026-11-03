package hygiene

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestI1ApphostExecAndI2Environment(t *testing.T) {
	var c compose
	if e := yaml.Unmarshal([]byte(readCompose(t)), &c); e != nil {
		t.Fatal(e)
	}
	s := c.Services[apphost]
	if len(s.Command) != 1 || s.Command[0] != "apphost" || s.Environment["GODEBUG"] != "multipathtcp=0" {
		t.Fatal("I-1/I-2: apphost command or GODEBUG")
	}
	b, e := os.ReadFile("../../Dockerfile")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), `ENTRYPOINT ["/usr/local/bin/script-runner"]`) {
		t.Fatal("I-1: binary exec entrypoint")
	}
	for _, replacement := range []string{`    command: ["sh", "-c", "script-runner apphost"]`, `    command: ["apphost", "extra"]`} {
		if v := CheckCompose([]byte(strings.Replace(readCompose(t), `    command: ["apphost"]`, replacement, 1))); len(v) == 0 {
			t.Fatal("I-1: forbidden command")
		}
	}
	if v := CheckCompose([]byte(strings.Replace(readCompose(t), "GODEBUG: multipathtcp=0", "GODEBUG: multipathtcp=1", 1))); len(v) == 0 {
		t.Fatal("I-2: MPTCP environment accepted")
	}
	main, e := os.ReadFile("../../main.go")
	if e != nil {
		t.Fatal(e)
	}
	start := strings.Index(string(main), "func runApphost()")
	end := strings.Index(string(main)[start:], "// initLibs") + start
	sourced := string(main)[start:end]
	if strings.Count(sourced, "apphost.Listen(") != 2 || strings.Contains(sourced, "ListenAndServe") {
		t.Fatal("I-2: both apphost listeners must use explicit TCP factory")
	}
}

func TestI3BinaryVulnerabilityCheck(t *testing.T) {
	w := readWorkflow(t, workflowDir+"script-runner.yml")
	found := false
	for _, job := range w.Jobs {
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "govulncheck -mode binary") {
				found = true
				for _, s := range []string{"docker build", "docker create", "docker cp", "/usr/local/bin/script-runner", "golang.org/x/vuln/cmd/govulncheck@"} {
					if !strings.Contains(step.Run, s) {
						t.Fatal("I-3: must scan the binary extracted from the built image", s)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("I-3: missing binary govulncheck")
	}
}
