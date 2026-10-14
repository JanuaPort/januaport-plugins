//go:build docker

package probe

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Netzgrenze des vermittlers (JanuaPort/januaport#1069). run.sh legt das Netz
// <prefix>-plugins mit --internal an (Stellvertreter für jnpt_plugins); daran
// hängen nur der vermittler und der Test-Läufer mit dem Alias „jnpt“
// (Stub-Gateway). Der abholer hängt am eigenen Netz <projekt>_egress, dort
// sitzt auch der SSH-Git-Anbieter.

func mediatorContainer() string { return project + "-script-runner-1" }
func fetcherContainer() string  { return project + "-script-runner-fetcher-1" }

// tcpProbe versucht aus einem Container heraus eine TCP-Verbindung (Python des
// Images, kein Zusatzwerkzeug). Ergebnis „ok“ oder der Fehler.
func tcpProbe(t *testing.T, container, host, port string) string {
	t.Helper()
	code := "import socket,sys\n" +
		"try:\n" +
		"    socket.create_connection((sys.argv[1], int(sys.argv[2])), 3).close()\n" +
		"    print('ok')\n" +
		"except Exception as e:\n" +
		"    print(type(e).__name__, e)\n"
	out, err := run("docker", "exec", container, "python3", "-I", "-c", code, host, port)
	if err != nil {
		t.Fatalf("docker exec %s: %v\n%s", container, err, out)
	}
	return strings.TrimSpace(out)
}

// #1069: Aus dem vermittler gibt es keinen Weg nach außen — weder ins
// Internet (Adresse und Name) noch zu einem Nachbarn in einem anderen Netz
// (der Git-Anbieter am Netz des abholers, per Name und per Adresse). Die
// Gegenprobe aus dem abholer zeigt, dass Ziel und Methode funktionieren.
func TestNetMediatorHasNoEgress(t *testing.T) {
	sshdIP := strings.TrimSpace(must(t, "docker", "inspect", "-f",
		`{{(index .NetworkSettings.Networks "`+project+`_egress").IPAddress}}`, sshdName))
	if sshdIP == "" {
		t.Fatal("Git-Anbieter ohne Adresse im Netz des abholers")
	}
	if got := tcpProbe(t, fetcherContainer(), "git.example.com", "22"); got != "ok" {
		t.Fatalf("Gegenprobe: abholer erreicht den Git-Anbieter nicht: %s", got)
	}
	for _, ziel := range [][2]string{
		{"1.1.1.1", "443"},
		{"example.com", "443"},
		{"git.example.com", "22"},
		{sshdIP, "22"},
	} {
		if got := tcpProbe(t, mediatorContainer(), ziel[0], ziel[1]); got == "ok" {
			t.Errorf("vermittler erreicht %s:%s", ziel[0], ziel[1])
		} else {
			t.Logf("%s:%s → %s", ziel[0], ziel[1], got)
		}
	}
}

// #1069: Weg B steht — der vermittler erreicht jnpt:8484 im internen Netz.
func TestNetMediatorReachesGateway(t *testing.T) {
	if got := tcpProbe(t, mediatorContainer(), "jnpt", "8484"); got != "ok" {
		t.Fatalf("vermittler erreicht jnpt:8484 nicht: %s", got)
	}
}

// #1069: Der vermittler hängt an genau einem Netz und veröffentlicht keinen
// Host-Port.
func TestNetMediatorOnlyPluginNetworkNoPorts(t *testing.T) {
	var netze map[string]any
	if err := json.Unmarshal([]byte(must(t, "docker", "inspect", "-f", "{{json .NetworkSettings.Networks}}", mediatorContainer())), &netze); err != nil {
		t.Fatal(err)
	}
	if _, ok := netze[prefix+"-plugins"]; len(netze) != 1 || !ok {
		t.Fatalf("Netze des vermittlers %v, erwartet genau %s-plugins", netze, prefix)
	}
	internal := strings.TrimSpace(must(t, "docker", "network", "inspect", "-f", "{{.Internal}}", prefix+"-plugins"))
	if internal != "true" {
		t.Fatalf("%s-plugins ist nicht internal: %s", prefix, internal)
	}
	if ports := strings.TrimSpace(must(t, "docker", "port", mediatorContainer())); ports != "" {
		t.Fatalf("Host-Ports: %s", ports)
	}
}

// #1069: Der Healthcheck der ausgelieferten Compose läuft im Image (python3)
// und meldet healthy — das liest der Aktuator mit HEALTH_DOCKER=1.
func TestHealthcheckReportsHealthy(t *testing.T) {
	deadline := time.Now().Add(90 * time.Second)
	var zustand string
	for time.Now().Before(deadline) {
		zustand = strings.TrimSpace(must(t, "docker", "inspect", "-f", "{{.State.Health.Status}}", mediatorContainer()))
		if zustand == "healthy" {
			return
		}
		time.Sleep(2 * time.Second)
	}
	log := must(t, "docker", "inspect", "-f", "{{json .State.Health.Log}}", mediatorContainer())
	t.Fatalf("Health-Status %q nach 90 s; Log: %.400s", zustand, log)
}
