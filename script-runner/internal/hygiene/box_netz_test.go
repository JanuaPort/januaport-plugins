package hygiene

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Die Box (../box) lässt in ihrer forward-Kette (policy drop) nur fest benannte
// Bridges durch. Zwei Netze des Skript-Läufers müssen deshalb feste Namen
// tragen und genau die Freigaben bekommen, die sie brauchen (JanuaPort/januaport#1427):
//
//   - das Abholer-Netz egress (jnpt-fetch0): ssh zum Git-Server, sonst nichts.
//     Mit zufälligem br-<hash> verwirft die Kette jedes Paket, die Container
//     laufen fehlerfrei weiter und holen nie einen Pin — symptomlos.
//     Der Name steht NUR in der Box-Override-Datei: Ein fester Bridge-Name in
//     der allgemeinen Compose ließe auf einem Host kein zweites Projekt zu
//     (Tests, Probeläufe, Kern-Hosts: "networks have same bridge name").
//   - das Apps-Netz jnpt_apps (jnpt-apps0): das der Apphost als external
//     erwartet. Es liegt in der Box-Compose, die nur auf der Box läuft.
//     Ohne Anlage dort scheitert `--profile apps`.

const (
	bridgeOptKey     = "com.docker.network.bridge.name"
	hardeningPath    = "../../../box/scripts/hardening.sh"
	boxComposePath   = "../../../box/docker-compose.box.yml"
	boxOverridePath  = "../../../box/docker-compose.script-runner.box.yml"
	boxRunnerCompose = "../../../box/docker-compose.script-runner.yml"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// firewallRegeln liefert die Regelzeilen der forward-Kette ohne Kommentare.
func firewallRegeln(t *testing.T) []string {
	t.Helper()
	var regeln []string
	for _, z := range strings.Split(readFile(t, hardeningPath), "\n") {
		z = strings.TrimSpace(z)
		if strings.HasPrefix(z, "iifname") {
			regeln = append(regeln, z)
		}
	}
	return regeln
}

// overrideBridge liest den Bridge-Namen des Abholer-Netzes aus der Box-Override-
// Datei. Sie setzt genau diesen einen Schlüssel und sonst nichts.
func overrideBridge(t *testing.T) string {
	t.Helper()
	var o struct {
		Services map[string]any `yaml:"services"`
		Volumes  map[string]any `yaml:"volumes"`
		Networks map[string]struct {
			DriverOpts map[string]string `yaml:"driver_opts"`
			Name       string            `yaml:"name"`
			External   bool              `yaml:"external"`
		} `yaml:"networks"`
	}
	d := yaml.NewDecoder(strings.NewReader(readFile(t, boxOverridePath)))
	d.KnownFields(true)
	if err := d.Decode(&o); err != nil {
		t.Fatalf("Override-Datei: %v", err)
	}
	if len(o.Services) != 0 || len(o.Volumes) != 0 || len(o.Networks) != 1 {
		t.Fatalf("Override darf nur networks.egress setzen: services %v, volumes %v, networks %v", o.Services, o.Volumes, o.Networks)
	}
	egress, ok := o.Networks["egress"]
	if !ok || egress.Name != "" || egress.External || len(egress.DriverOpts) != 1 {
		t.Fatalf("Override setzt nicht genau driver_opts an networks.egress: %+v", o.Networks)
	}
	name := egress.DriverOpts[bridgeOptKey]
	if name == "" || len(name) > 15 {
		t.Fatalf("Bridge-Name %q: leer oder länger als 15 Zeichen (IFNAMSIZ)", name)
	}
	return name
}

// Die allgemeine Compose und ihre Box-Kopie tragen KEINEN festen Bridge-Namen:
// zwei Compose-Projekte auf einem Host müssten sich sonst dieselbe Bridge teilen.
func TestRunnerComposeHasNoFixedBridgeName(t *testing.T) {
	for _, quelle := range []string{readCompose(t), readFile(t, boxRunnerCompose)} {
		if strings.Contains(quelle, bridgeOptKey) {
			t.Errorf("Compose trägt %s — der Pin gehört in die Box-Override-Datei", bridgeOptKey)
		}
	}
}

func TestBoxOverrideSetsFetcherBridgeName(t *testing.T) {
	if got := overrideBridge(t); got != "jnpt-fetch0" {
		t.Errorf("Bridge-Name der Override-Datei = %q, erwartet jnpt-fetch0", got)
	}
}

// Die Firewall gibt dem Abholer genau ssh frei, unter demselben Namen, den die
// Override-Datei setzt. Der Abholer spricht nur ssh (gitref.CheckRepoURL lehnt
// https ab); ein allgemeines accept für die Bridge ließe ihn überall hin.
func TestBoxFirewallAllowsOnlySSHFromFetcherBridge(t *testing.T) {
	name := overrideBridge(t)
	var fetch []string
	for _, r := range firewallRegeln(t) {
		if strings.Contains(r, `"`+name+`"`) {
			fetch = append(fetch, r)
		}
	}
	want := `iifname "` + name + `" tcp dport 22 accept`
	if len(fetch) != 1 || fetch[0] != want {
		t.Fatalf("Regeln für %s = %q, erwartet genau %q", name, fetch, want)
	}
}

type boxComposeSicht struct {
	Services map[string]struct {
		Networks any `yaml:"networks"`
	} `yaml:"services"`
	Networks map[string]struct {
		Name       string            `yaml:"name"`
		Internal   bool              `yaml:"internal"`
		DriverOpts map[string]string `yaml:"driver_opts"`
		Ipam       struct {
			Config []struct {
				Subnet string `yaml:"subnet"`
			} `yaml:"config"`
		} `yaml:"ipam"`
	} `yaml:"networks"`
}

// Die Box-Compose legt jnpt_apps an: intern, feste Bridge, festes Subnetz; die
// Proxy-Fläche hängt daran (sonst legt Compose das Netz gar nicht an, und das
// external des Apphost bleibt leer). Das Netz hat keinen Ausgang: die Firewall
// gibt nur Verkehr INNERHALB der Bridge frei.
func TestBoxComposeCreatesAppsNetwork(t *testing.T) {
	var c boxComposeSicht
	if err := yaml.Unmarshal([]byte(readFile(t, boxComposePath)), &c); err != nil {
		t.Fatal(err)
	}
	n := c.Networks["apps"]
	if n.Name != "jnpt_apps" || !n.Internal {
		t.Errorf("Netz apps = name %q internal %v, erwartet jnpt_apps und internal", n.Name, n.Internal)
	}
	if got := n.DriverOpts["com.docker.network.bridge.name"]; got != "jnpt-apps0" {
		t.Errorf("Bridge-Name = %q, erwartet jnpt-apps0", got)
	}
	if len(n.Ipam.Config) != 1 || !regexp.MustCompile(`^\$\{JNPT_APPS_SUBNET:-172\.31\.250\.0/28\}$`).MatchString(n.Ipam.Config[0].Subnet) {
		t.Errorf("Subnetz = %+v, erwartet ${JNPT_APPS_SUBNET:-172.31.250.0/28}", n.Ipam.Config)
	}
	netze, _ := c.Services["caddy"].Networks.(map[string]any)
	if _, ok := netze["apps"]; !ok {
		t.Errorf("caddy hängt nicht am Netz apps: %v", c.Services["caddy"].Networks)
	}
	found := false
	for _, r := range firewallRegeln(t) {
		if r == `iifname "jnpt-apps0" oifname "jnpt-apps0" accept` {
			found = true
		}
		if strings.Contains(r, "jnpt-apps0") && !strings.Contains(r, `oifname "jnpt-apps0"`) {
			t.Errorf("Regel %q lässt Verkehr aus dem Apps-Netz hinaus", r)
		}
	}
	if !found {
		t.Error("hardening.sh: Freigabe innerhalb der Bridge jnpt-apps0 fehlt")
	}
}
