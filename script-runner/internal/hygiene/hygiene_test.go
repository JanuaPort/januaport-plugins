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
	if len(DeniedSyscalls) != 17 {
		t.Fatalf("SEC-Liste hat %d Einträge, want 17", len(DeniedSyscalls))
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

// SEC-Bedingung zu F3 B1: eigener Runtime-Name. runtimeArgs gelten für jeden
// Container, der den Namen nutzt; ein bestehendes „runsc“ des Betreibers
// bleibt unberührt.
func TestDaemonJSONExample(t *testing.T) {
	b, err := os.ReadFile("../../deploy/daemon.json.example")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Runtimes map[string]struct {
			Path        string   `json:"path"`
			RuntimeArgs []string `json:"runtimeArgs"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Runtimes) != 1 {
		t.Fatalf("genau eine Runtime erwartet, %d", len(d.Runtimes))
	}
	r, ok := d.Runtimes["runsc-jnpt"]
	if !ok || r.Path == "" {
		t.Fatalf("Runtime runsc-jnpt fehlt: %+v", d.Runtimes)
	}
	if strings.Join(r.RuntimeArgs, " ") != "--oci-seccomp --host-uds=open" {
		t.Fatalf("runtimeArgs = %v", r.RuntimeArgs)
	}
}

// Kein Text setzt JNPT_SCRIPT_RUNNER_RUNTIME auf „runsc“ oder legt einen
// daemon.json-Eintrag „runsc“ an; README und CLAUDE.md nennen runsc-jnpt,
// --host-uds=open (nicht all) und --oci-seccomp.
func TestDocsUseOwnRuntimeName(t *testing.T) {
	bad := regexp.MustCompile(`JNPT_SCRIPT_RUNNER_RUNTIME[=:]\s*"?runsc([^-]|$)|--runtime[ =]runsc([^-]|$)|"runsc"\s*:|host-uds=all`)
	files := []string{"../../README.md", "../../CLAUDE.md", "../../docker-compose.yml",
		"../../deploy/daemon.json.example", "../../probe/run-runsc.sh"}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := bad.Find(b); m != nil {
			t.Errorf("%s: %q", f, m)
		}
	}
	for _, f := range []string{"../../README.md", "../../CLAUDE.md"} {
		b, _ := os.ReadFile(f)
		for _, want := range []string{"runsc-jnpt", "--host-uds=open", "--oci-seccomp"} {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s nennt %q nicht", f, want)
			}
		}
	}
}

// SEC-Zweitprüfung (Bedingung vor dem Merge): deny by default. Jeder
// Schlüssel, den der Wächter nicht kennt, ist ein Verstoß (KnownFields), und
// die gefährlichen Schlüssel sind zusätzlich ausdrücklich verboten — auch
// wenn sie im Struct stehen. Tabellenfälle je Dienst.
func TestComposeUnknownAndForbiddenKeysPerService(t *testing.T) {
	base := readCompose(t)
	anchors := map[string]string{
		"script-runner":         "    command: [\"mediator\"]\n",
		"script-runner-sandbox": "    command: [\"guard\"]\n",
		"script-runner-fetcher": "    command: [\"fetcher\"]\n",
	}
	inserts := []string{
		"    volumes_from: [script-runner]\n",
		"    userns_mode: host\n",
		"    cgroup: host\n",
		"    uts: host\n",
		"    pid: host\n",
		"    sysctls: {net.ipv4.ip_forward: 1}\n",
		"    extra_hosts: [\"jnpt:10.0.0.1\"]\n",
		"    devices: [/dev/fuse]\n",
		"    fantasie_schluessel: 1\n",
	}
	for svc, anchor := range anchors {
		if !strings.Contains(base, anchor) {
			t.Fatalf("Testfehler: Anker für %s fehlt", svc)
		}
		for _, ins := range inserts {
			t.Run(svc+"/"+strings.TrimSpace(ins), func(t *testing.T) {
				mutated := strings.Replace(base, anchor, anchor+ins, 1)
				if v := CheckCompose([]byte(mutated)); len(v) == 0 {
					t.Fatal("nicht erkannt")
				}
			})
		}
	}
	// Schlüssel, die die Sandbox schon trägt, werden ersetzt statt verdoppelt.
	for _, tt := range []struct{ from, to string }{
		{"    ipc: none\n", "    ipc: host\n"},
		{"    ipc: none\n", "    ipc: shareable\n"},
		{"    network_mode: none\n", "    network_mode: container:script-runner\n"},
		{"    network_mode: none\n", "    network_mode: host\n"},
	} {
		t.Run("sandbox/"+strings.TrimSpace(tt.to), func(t *testing.T) {
			if v := CheckCompose([]byte(strings.Replace(base, tt.from, tt.to, 1))); len(v) == 0 {
				t.Fatal("nicht erkannt")
			}
		})
	}
	// Auch auf oberster Ebene: ein unbekannter Schlüssel ist ein Verstoß.
	if v := CheckCompose([]byte(base + "\nconfigs: {}\n")); len(v) == 0 {
		t.Fatal("unbekannter Top-Level-Schlüssel nicht erkannt")
	}
}

// Die ausdrücklichen Verbote greifen auch ohne KnownFields: checkCommon
// bekommt einen Dienst, der die Schlüssel im Struct trägt.
func TestForbiddenFieldsExplicit(t *testing.T) {
	base := service{Image: "${JNPT_SCRIPT_RUNNER_IMAGE:?x}", SecurityOpt: []string{"no-new-privileges:true"}, CapDrop: []string{"ALL"}, ReadOnly: true}
	tests := []struct {
		name string
		mod  func(*service)
	}{
		{"volumes_from", func(s *service) { s.VolumesFrom = []string{"script-runner"} }},
		{"userns_mode", func(s *service) { s.UsernsMode = "host" }},
		{"uts", func(s *service) { s.Uts = "host" }},
		{"cgroup", func(s *service) { s.Cgroup = "host" }},
		{"pid", func(s *service) { s.Pid = "host" }},
		{"ipc host", func(s *service) { s.Ipc = "host" }},
		{"ipc shareable", func(s *service) { s.Ipc = "shareable" }},
		{"network_mode host", func(s *service) { s.NetworkMode = "host" }},
		{"network_mode container", func(s *service) { s.NetworkMode = "container:x" }},
		{"devices", func(s *service) { s.Devices = []string{"/dev/fuse"} }},
		{"sysctls", func(s *service) { s.Sysctls = map[string]any{"a": 1} }},
		{"extra_hosts", func(s *service) { s.ExtraHosts = []any{"a:1.2.3.4"} }},
	}
	if v := checkCommon("x", base); len(v) != 0 {
		t.Fatalf("Grundfall nicht sauber: %v", v)
	}
	for _, tt := range tests {
		s := base
		tt.mod(&s)
		if v := checkCommon("x", s); len(v) == 0 {
			t.Errorf("%s nicht erkannt", tt.name)
		}
	}
}

// SEC-Abnahme 0993c07 (Nachzug): ipc, pid und network_mode kennen neben
// container:<name> auch service:<name>. Beides teilt den Namensraum eines
// anderen Dienstes und ist verboten.
func TestServicePrefixForbiddenPerService(t *testing.T) {
	base := readCompose(t)
	anchors := map[string]string{
		"script-runner":         "    command: [\"mediator\"]\n",
		"script-runner-fetcher": "    command: [\"fetcher\"]\n",
	}
	for svc, anchor := range anchors {
		for _, ins := range []string{
			"    ipc: service:script-runner-sandbox\n",
			"    pid: service:script-runner-sandbox\n",
			"    network_mode: service:script-runner-sandbox\n",
		} {
			t.Run(svc+"/"+strings.TrimSpace(ins), func(t *testing.T) {
				if v := CheckCompose([]byte(strings.Replace(base, anchor, anchor+ins, 1))); len(v) == 0 {
					t.Fatal("nicht erkannt")
				}
			})
		}
	}
	for _, tt := range []struct{ from, to string }{
		{"    ipc: none\n", "    ipc: service:script-runner\n"},
		{"    network_mode: none\n", "    network_mode: service:script-runner\n"},
		{"    command: [\"guard\"]\n", "    command: [\"guard\"]\n    pid: service:script-runner\n"},
	} {
		t.Run("script-runner-sandbox/"+strings.Fields(tt.to)[len(strings.Fields(tt.to))-2], func(t *testing.T) {
			if v := CheckCompose([]byte(strings.Replace(base, tt.from, tt.to, 1))); len(v) == 0 {
				t.Fatal("nicht erkannt")
			}
		})
	}
	ok := service{Image: "${JNPT_SCRIPT_RUNNER_IMAGE:?x}", SecurityOpt: []string{"no-new-privileges:true"}, CapDrop: []string{"ALL"}, ReadOnly: true}
	for name, mod := range map[string]func(*service){
		"ipc":          func(s *service) { s.Ipc = "service:x" },
		"pid":          func(s *service) { s.Pid = "service:x" },
		"network_mode": func(s *service) { s.NetworkMode = "service:x" },
	} {
		s := ok
		mod(&s)
		if v := checkCommon("x", s); len(v) == 0 {
			t.Errorf("checkCommon: %s service:x nicht erkannt", name)
		}
	}
}
