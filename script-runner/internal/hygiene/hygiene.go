// Package hygiene prüft die ausgelieferte Compose gegen Vertrag §6, die
// SEC-Zeile 3 und die Compose-Hygiene der Plugins (JanuaPort/januaport#885).
// Es ist ein Wächter für Tests, kein Laufzeitcode.
package hygiene

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// DeniedSyscalls ist die SEC-Liste plus die Ergänzungen aus P0 (Messkommentar
// §1): Sie steht als ausdrückliche EPERM-Regel am Anfang des Profils.
var DeniedSyscalls = []string{
	"ptrace", "process_vm_readv", "process_vm_writev", "unshare", "mount", "umount2",
	"keyctl", "add_key", "request_key", "bpf", "userfaultfd", "perf_event_open",
	"io_uring_setup", "io_uring_enter", "io_uring_register", "setns", "pivot_root",
}

const (
	projectName = "jnpt-plugin-script-runner"
	mediator    = "script-runner"
	sandbox     = "script-runner-sandbox"
	fetcher     = "script-runner-fetcher"
	socketMount = "sockets:/run/script-runner"
	keyDir      = "/run/jnpt/plugins/script-runner"
	gitKeyDir   = "/run/jnpt/plugins/script-runner-git"
)

type compose struct {
	Name     string             `yaml:"name"`
	Services map[string]service `yaml:"services"`
	Volumes  map[string]volume  `yaml:"volumes"`
	Networks map[string]network `yaml:"networks"`
}

// service kennt genau die Schlüssel, die die ausgelieferte Compose nutzt,
// dazu die gefährlichen, die checkCommon ausdrücklich verbietet. Alles andere
// weist KnownFields ab (deny by default).
type service struct {
	Image       string            `yaml:"image"`
	Environment map[string]string `yaml:"environment"`
	DependsOn   []string          `yaml:"depends_on"`
	Command     []string          `yaml:"command"`
	User        string            `yaml:"user"`
	Privileged  bool              `yaml:"privileged"`
	CapAdd      []string          `yaml:"cap_add"`
	CapDrop     []string          `yaml:"cap_drop"`
	SecurityOpt []string          `yaml:"security_opt"`
	ReadOnly    bool              `yaml:"read_only"`
	NetworkMode string            `yaml:"network_mode"`
	Networks    []string          `yaml:"networks"`
	Ipc         string            `yaml:"ipc"`
	Pid         string            `yaml:"pid"`
	PidsLimit   int               `yaml:"pids_limit"`
	MemLimit    string            `yaml:"mem_limit"`
	MemswapLim  string            `yaml:"memswap_limit"`
	Cpus        float64           `yaml:"cpus"`
	Runtime     string            `yaml:"runtime"`
	Restart     string            `yaml:"restart"`
	Tmpfs       []string          `yaml:"tmpfs"`
	Volumes     []string          `yaml:"volumes"`
	Ports       []string          `yaml:"ports"`
	Devices     []string          `yaml:"devices"`
	VolumesFrom []string          `yaml:"volumes_from"`
	UsernsMode  string            `yaml:"userns_mode"`
	Uts         string            `yaml:"uts"`
	Cgroup      string            `yaml:"cgroup"`
	Sysctls     any               `yaml:"sysctls"`
	ExtraHosts  any               `yaml:"extra_hosts"`
}

type volume struct {
	Name     string `yaml:"name"`
	External bool   `yaml:"external"`
}

type network struct {
	Name     string `yaml:"name"`
	External bool   `yaml:"external"`
}

// CheckCompose liefert alle Verstöße; leer heißt sauber.
func CheckCompose(src []byte) []string {
	var c compose
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return []string{"unbekannter Schlüssel oder kein gültiges YAML: " + err.Error()}
	}
	var v []string
	add := func(f string, a ...any) { v = append(v, fmt.Sprintf(f, a...)) }
	if c.Name != projectName {
		add("Projektname %q, want %q", c.Name, projectName)
	}
	for name, vol := range c.Volumes {
		if vol.Name != "" || vol.External {
			add("Volume %s mit eigenem Namen oder external", name)
		}
	}
	if n := c.Networks["default"]; !n.External || n.Name != "jnpt_default" {
		add("Netz default ist nicht das externe jnpt_default")
	}
	for _, want := range []string{mediator, sandbox, fetcher} {
		if _, ok := c.Services[want]; !ok {
			add("Dienst %s fehlt", want)
		}
	}
	for name, s := range c.Services {
		v = append(v, checkCommon(name, s)...)
	}
	v = append(v, checkMediator(c.Services[mediator])...)
	v = append(v, checkSandbox(c.Services[sandbox])...)
	v = append(v, checkFetcher(c.Services[fetcher])...)
	return v
}

// checkCommon gilt für jeden Dienst (SEC-Zeile 3, #885-Hygiene).
func checkCommon(name string, s service) []string {
	var v []string
	add := func(f string, a ...any) { v = append(v, name+": "+fmt.Sprintf(f, a...)) }
	if !slices.Contains(s.SecurityOpt, "no-new-privileges:true") {
		add("no-new-privileges fehlt")
	}
	for _, o := range s.SecurityOpt {
		if strings.Contains(o, "unconfined") || strings.HasPrefix(o, "label=disable") || strings.HasPrefix(o, "systempaths") {
			add("security_opt %q", o)
		}
	}
	if s.Privileged {
		add("privileged")
	}
	if len(s.CapAdd) > 0 {
		add("cap_add")
	}
	if !slices.Equal(s.CapDrop, []string{"ALL"}) {
		add("cap_drop ist nicht [ALL]")
	}
	if !s.ReadOnly {
		add("read_only fehlt")
	}
	if s.NetworkMode == "host" || sharesNamespace(s.NetworkMode) {
		add("network_mode %q", s.NetworkMode)
	}
	if s.Ipc == "host" || s.Ipc == "shareable" || sharesNamespace(s.Ipc) {
		add("ipc %q", s.Ipc)
	}
	for key, val := range map[string]string{"pid": s.Pid, "userns_mode": s.UsernsMode, "uts": s.Uts, "cgroup": s.Cgroup} {
		if val == "host" || sharesNamespace(val) {
			add("%s %q", key, val)
		}
	}
	if len(s.VolumesFrom) > 0 {
		add("volumes_from")
	}
	if len(s.Devices) > 0 {
		add("devices")
	}
	if s.Sysctls != nil {
		add("sysctls")
	}
	if s.ExtraHosts != nil {
		add("extra_hosts")
	}
	if !strings.HasPrefix(s.Image, "${JNPT_SCRIPT_RUNNER_IMAGE:?") {
		add("Image nicht aus JNPT_SCRIPT_RUNNER_IMAGE")
	}
	for _, m := range s.Volumes {
		if strings.Contains(m, "docker.sock") {
			add("docker.sock eingehängt")
		}
		if name != mediator && strings.HasPrefix(m, keyDir+":") {
			add("Schlüsselordner des vermittlers eingehängt")
		}
		if name != fetcher && strings.HasPrefix(m, gitKeyDir) {
			add("Deploy-Key eingehängt")
		}
	}
	for _, p := range s.Ports {
		if name != mediator || p != "127.0.0.1:8091:8091" {
			add("Host-Port %q (nur /healthz auf 127.0.0.1:8091 erlaubt)", p)
		}
	}
	return v
}

func checkMediator(s service) []string {
	var v []string
	if !slices.Equal(s.Networks, []string{"default"}) {
		v = append(v, "script-runner: nicht genau im Netz default (jnpt_default)")
	}
	if !slices.Contains(s.Volumes, keyDir+":"+keyDir+":ro") {
		v = append(v, "script-runner: Schlüsselordner nicht read-only eingehängt")
	}
	return v
}

// checkSandbox ist Vertrag §6 Zeile für Zeile.
func checkSandbox(s service) []string {
	var v []string
	add := func(f string, a ...any) { v = append(v, sandbox+": "+fmt.Sprintf(f, a...)) }
	if s.NetworkMode != "none" || len(s.Networks) > 0 {
		add("network_mode ist nicht none")
	}
	if s.Ipc != "none" {
		add("ipc ist nicht none")
	}
	if s.PidsLimit != 128 {
		add("pids_limit %d, want 128", s.PidsLimit)
	}
	if s.MemLimit != "512m" || s.MemswapLim != "512m" || s.Cpus != 1 {
		add("Grenzen nicht 512m/512m/1 CPU")
	}
	if s.User != "65533:65533" {
		add("user %q", s.User)
	}
	if s.Restart != "always" {
		add("restart %q", s.Restart)
	}
	if s.Runtime != "${JNPT_SCRIPT_RUNNER_RUNTIME:-runc}" {
		add("runtime %q", s.Runtime)
	}
	if !slices.Contains(s.SecurityOpt, "seccomp=./seccomp/jnpt-sandbox-seccomp.json") {
		add("Seccomp-Profil fehlt")
	}
	const opts = "noexec,nosuid,nodev,uid=65533,gid=65533,mode=0700"
	if !slices.Equal(s.Tmpfs, []string{"/work:size=64m," + opts, "/tmp:size=16m," + opts}) {
		add("tmpfs nicht nach Vertrag §6")
	}
	if !slices.Equal(s.Volumes, []string{socketMount + ":ro"}) {
		add("Mounts %v, want nur den Socket-Ordner read-only", s.Volumes)
	}
	return v
}

func checkFetcher(s service) []string {
	var v []string
	if slices.Contains(s.Networks, "default") || s.NetworkMode != "" {
		v = append(v, "script-runner-fetcher: im Netz jnpt_default oder eigener network_mode")
	}
	if !slices.Contains(s.Volumes, gitKeyDir+":"+gitKeyDir+":ro") {
		v = append(v, "script-runner-fetcher: Deploy-Key-Ordner nicht read-only eingehängt")
	}
	return v
}

// sharesNamespace erkennt beide Formen, mit denen Compose den Namensraum eines
// anderen Containers oder Dienstes übernimmt: container:<name> und
// service:<name>.
func sharesNamespace(v string) bool {
	return strings.HasPrefix(v, "container:") || strings.HasPrefix(v, "service:")
}
