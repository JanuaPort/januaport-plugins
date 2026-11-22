package hygiene

import (
	"regexp"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// Die Proxys der Box-Compose (../box/docker-compose.box.yml) stehen an der
// Grenze der Box: caddy (443), caddy-admin (8443), tailscale (Wartung). Sie
// laufen weder über einen wandernden Tag noch ungehärtet als root mit allen
// Capabilities (JanuaPort/januaport#1428). Die Box-Dateien haben kein eigenes
// Test-Paket; dieser Wächter liegt neben dem der Netze (box_netz_test.go).
//
// Nur am Gerät zu belegen: dass Caddy und Tailscale mit dieser Härtung starten.

const (
	boxCaddyPin     = "caddy:2.11.9-alpine@sha256:003a97808e781aa9f7e75cc2f43d34d461099031faf5e20faefb9f61126836f0"
	boxTailscalePin = "tailscale/tailscale:v1.104.7@sha256:72c1e19e0aedca96b8533bbb0b4368564410ff20b17e53b804eb6c1f04a31be4"
)

type boxProxyDienst struct {
	Image       string            `yaml:"image"`
	SecurityOpt []string          `yaml:"security_opt"`
	CapDrop     []string          `yaml:"cap_drop"`
	CapAdd      []string          `yaml:"cap_add"`
	Devices     []string          `yaml:"devices"`
	ReadOnly    bool              `yaml:"read_only"`
	Tmpfs       []string          `yaml:"tmpfs"`
	Environment map[string]string `yaml:"environment"`
}

func boxProxyDienste(t *testing.T) map[string]boxProxyDienst {
	t.Helper()
	var c struct {
		Services map[string]boxProxyDienst `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, boxComposePath)), &c); err != nil {
		t.Fatal(err)
	}
	return c.Services
}

// Version UND Digest, nie ein gleitender Tag: `docker compose pull` und jedes
// Update ziehen sonst eine ungeprüfte Fassung an die Grenze der Box.
func TestBoxProxyImagesPinnedByDigest(t *testing.T) {
	dienste := boxProxyDienste(t)
	gepinnt := regexp.MustCompile(`^[a-z0-9./-]+:v?\d+\.\d+\.\d+(-alpine)?@sha256:[0-9a-f]{64}$`)
	for _, tc := range []struct{ dienst, erwartet string }{
		{"caddy", boxCaddyPin},
		{"caddy-admin", boxCaddyPin},
		{"tailscale", boxTailscalePin},
	} {
		t.Run(tc.dienst, func(t *testing.T) {
			image := dienste[tc.dienst].Image
			if !gepinnt.MatchString(image) {
				t.Errorf("Image %q trägt nicht Version und sha256-Digest", image)
			}
			if image != tc.erwartet {
				t.Errorf("Image = %q, erwartet %q", image, tc.erwartet)
			}
		})
	}
}

// no-new-privileges, cap_drop ALL und genau die Capabilities, die der Modus
// braucht. Tailscale läuft im Userspace-Modus: kein TUN-Gerät, kein NET_ADMIN.
func TestBoxProxyIsHardened(t *testing.T) {
	dienste := boxProxyDienste(t)
	for _, tc := range []struct {
		dienst   string
		capAdd   []string
		readOnly bool
	}{
		{"caddy", []string{"NET_BIND_SERVICE"}, true},
		{"caddy-admin", []string{"NET_BIND_SERVICE"}, true},
		{"tailscale", nil, false},
	} {
		t.Run(tc.dienst, func(t *testing.T) {
			d := dienste[tc.dienst]
			if !slices.Contains(d.SecurityOpt, "no-new-privileges:true") {
				t.Errorf("security_opt = %v, no-new-privileges:true fehlt", d.SecurityOpt)
			}
			if !slices.Equal(d.CapDrop, []string{"ALL"}) {
				t.Errorf("cap_drop = %v, erwartet [ALL]", d.CapDrop)
			}
			if !slices.Equal(d.CapAdd, tc.capAdd) {
				t.Errorf("cap_add = %v, erwartet %v", d.CapAdd, tc.capAdd)
			}
			if d.ReadOnly != tc.readOnly {
				t.Errorf("read_only = %v, erwartet %v", d.ReadOnly, tc.readOnly)
			}
			if tc.readOnly && !slices.Contains(d.Tmpfs, "/tmp") {
				t.Errorf("tmpfs = %v, /tmp fehlt (read_only braucht einen beschreibbaren /tmp)", d.Tmpfs)
			}
		})
	}
	ts := dienste["tailscale"]
	if ts.Environment["TS_USERSPACE"] != "true" || len(ts.Devices) != 0 {
		t.Errorf("tailscale: cap_add [] gilt nur im Userspace-Modus ohne TUN-Gerät (TS_USERSPACE=%q, devices %v)", ts.Environment["TS_USERSPACE"], ts.Devices)
	}
}
