package hygiene

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

const composePath = "../../docker-compose.yml"

func readCompose(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// SEC-Zeile 3 und Vertrag §6: die ausgelieferte Compose ist sauber.
func TestShippedComposeIsClean(t *testing.T) {
	if v := CheckCompose([]byte(readCompose(t))); len(v) != 0 {
		t.Fatalf("Verstöße:\n%s", strings.Join(v, "\n"))
	}
}

// Negativproben: jede Mutation muss mindestens einen Verstoß ergeben. Jede
// Regel hat so einen Beleg, dass sie greift.
func TestComposeMutationsAreCaught(t *testing.T) {
	base := readCompose(t)
	tests := []struct {
		name string
		from string
		to   string
	}{
		{"seccomp unconfined", "seccomp=./seccomp/jnpt-sandbox-seccomp.json", "seccomp=unconfined"},
		{"apparmor unconfined", "      - no-new-privileges:true\n      - seccomp=", "      - no-new-privileges:true\n      - apparmor=unconfined\n      - seccomp="},
		{"no-new-privileges fehlt am mediator", "    command: [\"mediator\"]\n    security_opt:\n      - no-new-privileges:true", "    command: [\"mediator\"]\n    security_opt:\n      - label=type:x"},
		{"privileged", "    read_only: true\n", "    read_only: true\n    privileged: true\n"},
		{"cap_add", "    cap_drop: [ALL]\n", "    cap_drop: [ALL]\n    cap_add: [SYS_ADMIN]\n"},
		{"docker.sock", "      - sockets:/run/script-runner:ro\n", "      - sockets:/run/script-runner:ro\n      - /var/run/docker.sock:/var/run/docker.sock\n"},
		{"MCP-Host-Port", "      - \"127.0.0.1:8091:8091\"\n", "      - \"127.0.0.1:8091:8091\"\n      - \"8090:8090\"\n"},
		{"Health-Port nicht auf Loopback", "      - \"127.0.0.1:8091:8091\"\n", "      - \"8091:8091\"\n"},
		{"network_mode host", "    network_mode: none\n", "    network_mode: host\n"},
		{"festes Image", "image: ${JNPT_SCRIPT_RUNNER_IMAGE:?", "image: ghcr.io/example/x:latest #"},
		{"falscher Projektname", "name: jnpt-plugin-script-runner", "name: jnpt"},
		{"benanntes Volume", "  store: {}\n", "  store:\n    name: jnpt_jnpt-data\n"},
		{"externes Volume", "  store: {}\n", "  store:\n    external: true\n"},
		{"pids_limit anders", "pids_limit: 128", "pids_limit: 4096"},
		{"ipc fehlt", "    ipc: none\n", ""},
		{"tmpfs ohne mode", "/work:size=64m,noexec,nosuid,nodev,uid=65533,gid=65533,mode=0700", "/work:size=64m,noexec,nosuid,nodev"},
		{"Socket-Ordner rw", "      - sockets:/run/script-runner:ro\n", "      - sockets:/run/script-runner\n"},
		{"Deploy-Key im mediator", "      - /run/jnpt/plugins/script-runner:/run/jnpt/plugins/script-runner:ro\n", "      - /run/jnpt/plugins/script-runner:/run/jnpt/plugins/script-runner:ro\n      - /run/jnpt/plugins/script-runner-git:/k:ro\n"},
		{"abholer im jnpt_default", "    networks: [egress]\n", "    networks: [default]\n"},
		{"read_only fehlt", "    read_only: true\n", "    read_only: false\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(base, tt.from) {
				t.Fatalf("Testfehler: %q nicht in der Compose", tt.from)
			}
			mutated := strings.Replace(base, tt.from, tt.to, 1)
			if v := CheckCompose([]byte(mutated)); len(v) == 0 {
				t.Fatal("Mutation nicht erkannt")
			}
		})
	}
}

// S1 / Vertrag §6: das Profil sperrt die SEC-Liste ausdrücklich und erlaubt
// keinen dieser Syscalls in einer späteren Regel.
func TestSeccompProfile(t *testing.T) {
	b, err := os.ReadFile("../../seccomp/jnpt-sandbox-seccomp.json")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		DefaultAction string `json:"defaultAction"`
		Syscalls      []struct {
			Names    []string `json:"names"`
			Action   string   `json:"action"`
			ErrnoRet *int     `json:"errnoRet"`
		} `json:"syscalls"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.DefaultAction != "SCMP_ACT_ERRNO" {
		t.Fatalf("defaultAction = %s", p.DefaultAction)
	}
	first := p.Syscalls[0]
	if first.Action != "SCMP_ACT_ERRNO" || first.ErrnoRet == nil || *first.ErrnoRet != 1 {
		t.Fatalf("erste Regel ist keine EPERM-Sperre: %+v", first)
	}
	deny := map[string]bool{}
	for _, n := range first.Names {
		deny[n] = true
	}
	for _, n := range DeniedSyscalls {
		if !deny[n] {
			t.Errorf("%s fehlt in der Sperrregel", n)
		}
	}
	for i, s := range p.Syscalls[1:] {
		if s.Action != "SCMP_ACT_ALLOW" {
			continue
		}
		for _, n := range s.Names {
			if deny[n] {
				t.Errorf("Regel %d erlaubt %s", i+1, n)
			}
		}
	}
}

// R12: das Image hat einen per Digest gepinnten Python-Unterbau und kein pip.
func TestDockerfile(t *testing.T) {
	b, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !regexp.MustCompile(`(?m)^FROM python:3\.[0-9]+-slim@sha256:[0-9a-f]{64}`).MatchString(s) {
		t.Error("Python-Basis nicht per Digest gepinnt")
	}
	if !regexp.MustCompile(`(?m)^FROM golang:1\.25\.[0-9]+@sha256:[0-9a-f]{64} AS build`).MatchString(s) {
		t.Error("Go-Builder nicht per Digest gepinnt")
	}
	for _, want := range []string{"ensurepip", "pip", "CGO_ENABLED=0"} {
		if !strings.Contains(s, want) {
			t.Errorf("Dockerfile ohne %q", want)
		}
	}
	if strings.Contains(s, "pip install") {
		t.Error("pip install im Dockerfile")
	}
}
