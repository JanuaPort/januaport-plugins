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
	initializer = "script-runner-init"
	socketMount = "sockets:/run/script-runner"
	// libsMount ist der Volume mit den entpackten Python-Bibliotheken (#1041):
	// beim vermittler beschreibbar, in der Sandbox read-only — der EINE
	// zusätzliche Mount der Sandbox. Nur der kurzlebige Init sieht ihn ebenfalls voll.
	libsMount = "libs:/libs"
	keyDir    = "/run/jnpt/plugins/script-runner"
	gitKeyDir = "/run/jnpt/plugins/script-runner-git"

	// pluginNet ist das interne Netz der Kern-Compose (internal: true,
	// JanuaPort/januaport#1069): dort hängen nur Gateway und vermittler.
	pluginNet  = "jnpt_plugins"
	gatewayNet = "jnpt_default"
)

// mediatorHealthcheck ist der Healthcheck des vermittlers ohne neuen Code:
// Python des Images gegen /healthz im Container. Der Aktuator liest ihn mit
// HEALTH_DOCKER=1, weil es keinen Host-Port mehr gibt.
var mediatorHealthcheck = []string{"CMD", "python3", "-I", "-c",
	"import urllib.request; urllib.request.urlopen('http://127.0.0.1:8091/healthz', timeout=3)"}

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
	DependsOn   dependencies      `yaml:"depends_on"`
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
	Volumes     mounts            `yaml:"volumes"`
	Ports       []string          `yaml:"ports"`
	Healthcheck *healthcheck      `yaml:"healthcheck"`
	Devices     []string          `yaml:"devices"`
	VolumesFrom []string          `yaml:"volumes_from"`
	UsernsMode  string            `yaml:"userns_mode"`
	Uts         string            `yaml:"uts"`
	Cgroup      string            `yaml:"cgroup"`
	Sysctls     any               `yaml:"sysctls"`
	ExtraHosts  any               `yaml:"extra_hosts"`
}

// mounts accepts the one long-form mount needed to hide staging (#1096).
// Its own decoder keeps KnownFields: node.Decode would silently accept keys.
type mounts []string

func (ms *mounts) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("mounts: keine Liste")
	}
	for _, n := range node.Content {
		if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
			*ms = append(*ms, n.Value)
			continue
		}
		var m struct {
			Type     string `yaml:"type"`
			Source   string `yaml:"source"`
			Target   string `yaml:"target"`
			ReadOnly bool   `yaml:"read_only"`
			Volume   struct {
				Subpath string `yaml:"subpath"`
				NoCopy  bool   `yaml:"nocopy"`
			} `yaml:"volume"`
		}
		b, err := yaml.Marshal(n)
		if err != nil {
			return err
		}
		d := yaml.NewDecoder(bytes.NewReader(b))
		d.KnownFields(true)
		if err := d.Decode(&m); err != nil {
			return err
		}
		if m.Type != "volume" {
			return fmt.Errorf("mounts: nur Volume-Langform erlaubt")
		}
		v := m.Source + ":" + m.Target
		if m.ReadOnly {
			v += ":ro"
		}
		v += ":subpath=" + m.Volume.Subpath
		if m.Volume.NoCopy {
			v += ":nocopy"
		}
		*ms = append(*ms, v)
	}
	return nil
}

// dependencies accepts Compose's list and condition map while preserving
// deny-by-default decoding (in particular, no required:false dependency).
type dependencies map[string]string

func (ds *dependencies) UnmarshalYAML(node *yaml.Node) error {
	*ds = dependencies{}
	if node.Kind == yaml.SequenceNode {
		var names []string
		if err := node.Decode(&names); err != nil {
			return err
		}
		for _, name := range names {
			(*ds)[name] = "service_started"
		}
		return nil
	}
	var opts map[string]struct {
		Condition string `yaml:"condition"`
	}
	b, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err := d.Decode(&opts); err != nil {
		return err
	}
	for name, opt := range opts {
		(*ds)[name] = opt.Condition
	}
	return nil
}

type healthcheck struct {
	Test     []string `yaml:"test"`
	Interval string   `yaml:"interval"`
	Timeout  string   `yaml:"timeout"`
	Retries  int      `yaml:"retries"`
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
	if n := c.Networks["plugins"]; !n.External || n.Name != pluginNet {
		add("Netz plugins ist nicht das externe %s", pluginNet)
	}
	if _, ok := c.Volumes["libs"]; !ok {
		add("Volume libs fehlt")
	}
	for _, want := range []string{mediator, sandbox, fetcher, initializer} {
		if _, ok := c.Services[want]; !ok {
			add("Dienst %s fehlt", want)
		}
	}
	for name, s := range c.Services {
		v = append(v, checkCommon(name, s)...)
		if name != fetcher {
			v = append(v, checkNoEgress(name, s, c.Networks)...)
		}
	}
	v = append(v, checkMediator(c.Services[mediator], c.Networks)...)
	v = append(v, checkSandbox(c.Services[sandbox])...)
	v = append(v, checkFetcher(c.Services[fetcher], c.Networks)...)
	v = append(v, checkInit(c.Services[initializer])...)
	return v
}

// isPluginNet: Nur das externe jnpt_plugins ist ein Netz ohne Ausgang. Ein
// eigenes Netz des Projekts, das implizite Projekt-Default und jedes andere
// externe Netz gelten als Netz mit Ausgang (#1069).
func isPluginNet(key string, nets map[string]network) bool {
	n, ok := nets[key]
	return ok && n.External && n.Name == pluginNet
}

// checkNoEgress: Ausgang hat nur der abholer. Jeder andere Dienst hat kein Netz
// (network_mode: none) oder hängt ausschließlich an jnpt_plugins. Ohne
// Netzliste hinge er am Projekt-Default — mit Ausgang.
func checkNoEgress(name string, s service, nets map[string]network) []string {
	if s.NetworkMode == "none" && len(s.Networks) == 0 {
		return nil
	}
	if s.NetworkMode != "" || len(s.Networks) == 0 {
		return []string{name + ": hängt an einem Netz mit Ausgang (nur der abholer darf das)"}
	}
	var v []string
	for _, key := range s.Networks {
		if !isPluginNet(key, nets) {
			v = append(v, fmt.Sprintf("%s: Netz %s hat Ausgang (nur der abholer darf das)", name, key))
		}
	}
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
		if name != mediator && name != sandbox && name != initializer && (strings.HasPrefix(m, "libs:") || strings.Contains(m, ":/libs")) {
			add("libs eingehängt (nur vermittler, Sandbox und Init)")
		}
		if name != fetcher && strings.HasPrefix(m, gitKeyDir) {
			add("Deploy-Key eingehängt")
		}
	}
	// #1069: Ein veröffentlichter Port eines Containers, der nur im internen
	// Netz hängt, ist nicht erreichbar, und jeder andere Port wäre ein Weg
	// von außen hinein. Deshalb keiner.
	for _, p := range s.Ports {
		add("Host-Port %q (keine Host-Ports erlaubt)", p)
	}
	return v
}

func checkMediator(s service, nets map[string]network) []string {
	var v []string
	if len(s.DependsOn) != 2 || s.DependsOn[sandbox] != "service_started" || s.DependsOn[fetcher] != "service_started" {
		v = append(v, "script-runner: Sandbox/abholer-Abhängigkeit fehlt")
	}
	if len(s.Networks) != 1 || !isPluginNet(s.Networks[0], nets) {
		v = append(v, "script-runner: nicht genau im externen Netz "+pluginNet)
	}
	if !slices.Contains(s.Volumes, libsMount) {
		v = append(v, "script-runner: libs nicht beschreibbar als "+libsMount)
	}
	if !slices.Contains(s.Volumes, keyDir+":"+keyDir+":ro") {
		v = append(v, "script-runner: Schlüsselordner nicht read-only eingehängt")
	}
	if h := s.Healthcheck; h == nil || !slices.Equal(h.Test, mediatorHealthcheck) || h.Interval != "10s" {
		v = append(v, "script-runner: Healthcheck nicht python3 gegen 127.0.0.1:8091/healthz alle 10s")
	}
	return v
}

// checkSandbox ist Vertrag §6 Zeile für Zeile.
func checkSandbox(s service) []string {
	var v []string
	add := func(f string, a ...any) { v = append(v, sandbox+": "+fmt.Sprintf(f, a...)) }
	if len(s.DependsOn) != 1 || s.DependsOn[initializer] != "service_completed_successfully" {
		add("Init muss erfolgreich beendet sein")
	}
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
	if !slices.Equal(s.Volumes, []string{socketMount + ":ro", libsMount + ":ro:subpath=ready:nocopy"}) {
		add("Mounts %v, want genau den Socket-Ordner und libs, beide read-only", s.Volumes)
	}
	return v
}

func checkInit(s service) []string {
	var v []string
	add := func(text string) { v = append(v, initializer+": "+text) }
	if s.NetworkMode != "none" || len(s.Networks) != 0 {
		add("kein Netz erlaubt")
	}
	if s.User != "65532:65532" {
		add("user ist nicht 65532:65532")
	}
	if !slices.Equal(s.Command, []string{"init-libs"}) {
		add("Befehl ist nicht init-libs")
	}
	if s.Restart != "no" {
		add("Init darf nicht neu starten")
	}
	if !slices.Equal(s.Volumes, []string{libsMount}) {
		add("nur der volle libs-Mount ist erlaubt, kein Socket/Store/Schlüssel")
	}
	if !slices.Equal(s.SecurityOpt, []string{"no-new-privileges:true"}) {
		add("security_opt nicht wie ausgeliefert")
	}
	if len(s.Tmpfs) != 0 || len(s.Environment) != 0 || len(s.DependsOn) != 0 || s.Healthcheck != nil {
		add("Init trägt zusätzliche Laufzeit-Konfiguration")
	}
	return v
}

// checkFetcher: Der abholer braucht Ausgang zum Git-Anbieter, aber keinen Weg
// zum Gateway — weder über jnpt_plugins noch über jnpt_default.
func checkFetcher(s service, nets map[string]network) []string {
	var v []string
	if s.NetworkMode != "" {
		v = append(v, "script-runner-fetcher: eigener network_mode")
	}
	for _, key := range s.Networks {
		if n, ok := nets[key]; ok && n.External && (n.Name == pluginNet || n.Name == gatewayNet) {
			v = append(v, "script-runner-fetcher: im Netz "+n.Name+" (Weg zum Gateway)")
		}
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
