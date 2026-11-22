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
//   - das Apps-Netz jnpt_apps (jnpt-apps0): das der Apphost als external
//     erwartet. Ohne Anlage in der Box-Compose scheitert `--profile apps`.

const (
	fetcherBridgeZeile = "      com.docker.network.bridge.name: jnpt-fetch0\n"
	egressBlock        = "  egress:\n    driver_opts:\n" + fetcherBridgeZeile
	hardeningPath      = "../../../box/scripts/hardening.sh"
	boxComposePath     = "../../../box/docker-compose.box.yml"
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

func TestFetcherNetworkHasFixedBridgeName(t *testing.T) {
	base := readCompose(t)
	if !strings.Contains(base, egressBlock) {
		t.Fatalf("egress trägt keinen festen Bridge-Namen: %q fehlt", egressBlock)
	}
	if v := CheckCompose([]byte(base)); len(v) != 0 {
		t.Fatalf("Verstöße:\n%s", strings.Join(v, "\n"))
	}
	for name, mutated := range map[string]string{
		"Pin entfernt":       strings.Replace(base, egressBlock, "  egress: {}\n", 1),
		"anderer Name":       strings.Replace(base, "jnpt-fetch0", "jnpt-x0", 1),
		"Name zu lang":       strings.Replace(base, "jnpt-fetch0", "jnpt-fetcher-egress0", 1),
		"leerer driver_opts": strings.Replace(base, fetcherBridgeZeile, "      foo: bar\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			v := CheckCompose([]byte(mutated))
			if !hasViolation(v, "Bridge") {
				t.Fatalf("Mutation nicht als Bridge-Verstoß erkannt: %v", v)
			}
		})
	}
}

func hasViolation(v []string, part string) bool {
	for _, s := range v {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

// Die Firewall gibt dem Abholer genau ssh frei. Der Abholer spricht nur ssh
// (gitref.CheckRepoURL lehnt https ab); ein allgemeines accept für die Bridge
// ließe ihn überall hin.
func TestBoxFirewallAllowsOnlySSHFromFetcherBridge(t *testing.T) {
	var fetch []string
	for _, r := range firewallRegeln(t) {
		if strings.Contains(r, "jnpt-fetch0") {
			fetch = append(fetch, r)
		}
	}
	want := []string{`iifname "jnpt-fetch0" tcp dport 22 accept`}
	if len(fetch) != len(want) || fetch[0] != want[0] {
		t.Fatalf("Regeln für jnpt-fetch0 = %q, erwartet genau %q", fetch, want)
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
