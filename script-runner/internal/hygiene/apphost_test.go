package hygiene

import (
	"os"
	"strings"
	"testing"
)

func TestInvariant10ApphostNetworkAndMounts(t *testing.T) {
	base := readCompose(t)
	if !strings.Contains(base, "  script-runner-apphost:\n") {
		t.Fatal("apphost missing")
	}
	if v := CheckCompose([]byte(base)); len(v) != 0 {
		t.Fatal(v)
	}
	for _, tc := range []struct{ from, to string }{
		{"    networks: [jnpt_apps]", "    networks: [jnpt_apps, plugins]"},
		{"    networks: [jnpt_apps]", "    networks: [plugins]"},
		{"    networks: [plugins]", "    networks: [plugins, jnpt_apps]"},
		{"    networks: [egress]", "    networks: [egress, jnpt_apps]"},
		{"    profiles: [apps]", "    profiles: []"},
		{"/run/jnpt/plugins/apphost:/run/secrets/jnpt:ro", "/run/jnpt/plugins/apphost:/run/secrets/jnpt"},
		{"/run/jnpt/plugins/apphost:/run/secrets/jnpt:ro", "/run/jnpt/plugins:/run/secrets/jnpt:ro"},
		{"    command: [\"apphost\"]", "    command: [\"apphost\"]\n    ports: [\"8080:8080\"]"},
		{"    command: [\"apphost\"]", "    command: [\"apphost\"]\n    tmpfs: [/tmp]"},
	} {
		if !strings.Contains(base, tc.from) {
			t.Fatal("mutation anchor missing", tc.from)
		}
		if v := CheckCompose([]byte(strings.Replace(base, tc.from, tc.to, 1))); len(v) == 0 {
			t.Fatal("mutation accepted", tc.to)
		}
	}
	copy, err := os.ReadFile("../../../box/docker-compose.script-runner.yml")
	if err != nil {
		t.Fatal(err)
	}
	if string(copy) != base {
		t.Fatal("box runner compose differs")
	}
}

func TestInvariant11CaddyFiles(t *testing.T) {
	b, err := os.ReadFile("../../apps.caddy")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"*.apps.{$JNPT_APPS_DOMAIN}", "ask http://script-runner-apphost:8081/tls-ask", "reverse_proxy script-runner-apphost:8080", "app_login_log", "delete code"} {
		if !strings.Contains(string(b), s) {
			t.Fatal("Caddy contract", s)
		}
	}
	box, err := os.ReadFile("../../../box/apps.caddy")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Replace(string(b), "tls {", "tls internal {", 1) != string(box) {
		t.Fatal("box Caddy differs beyond issuer")
	}
}
