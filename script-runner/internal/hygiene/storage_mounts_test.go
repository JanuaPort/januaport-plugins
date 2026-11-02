package hygiene

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKeyMountParentsAtComposeAdapter(t *testing.T) {
	for _, tc := range []struct{ service, source, reason string }{
		{mediator, "/run/jnpt/plugins", "Deploy-Key eingehängt"},
		{sandbox, "/run/jnpt", "Deploy-Key eingehängt"},
		{sandbox, "/run/jnpt", "Schlüsselordner des vermittlers eingehängt"},
		{fetcher, "/run/jnpt/plugins", "Schlüsselordner des vermittlers eingehängt"},
		{mediator, "/run", "Deploy-Key eingehängt"},
		{mediator, "/", "Deploy-Key eingehängt"},
		{mediator, gitKeyDir, "Deploy-Key eingehängt"},
		{mediator, gitKeyDir + "/runner-pins.pub", "Deploy-Key eingehängt"},
		{fetcher, keyDir, "Schlüsselordner des vermittlers eingehängt"},
		{fetcher, keyDir + "/runtime-key", "Schlüsselordner des vermittlers eingehängt"},
		{mediator, "/run/jnpt/plugins/other/..", "Deploy-Key eingehängt"},
		{initializer, "/run/jnpt", "Deploy-Key eingehängt"},
	} {
		t.Run(tc.service+tc.source+tc.reason, func(t *testing.T) {
			var c map[string]any
			if e := yaml.Unmarshal([]byte(readCompose(t)), &c); e != nil {
				t.Fatal(e)
			}
			s := c["services"].(map[string]any)[tc.service].(map[string]any)
			s["volumes"] = append(s["volumes"].([]any), tc.source+":/p:ro")
			raw, e := yaml.Marshal(c)
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, v := range CheckCompose(raw) {
				if v == tc.service+": "+tc.reason {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing specific key finding for %s:/p:ro in %s", tc.source, tc.service)
			}
		})
	}
}

func TestKeyMountComparisonUsesComponents(t *testing.T) {
	for _, source := range []string{"/run/jnpt/plugins-other", "/run/jnpt/plugins/script-runner-git-other", "/run/jnpt/plugins/script-runner-other", "named-volume"} {
		s := service{Volumes: mounts{source + ":/p:ro"}}
		for _, v := range checkCommon(mediator, s) {
			if strings.Contains(v, "Deploy-Key") {
				t.Fatal("sibling path blocked", source)
			}
		}
		for _, v := range checkCommon(fetcher, s) {
			if strings.Contains(v, "Schlüsselordner") {
				t.Fatal("sibling path blocked", source)
			}
		}
	}
}

// H6: assert the key-specific finding at the real YAML adapter, not merely
// another sandbox mount-policy finding. Unknown sources can expose either key.
func TestUnresolvedKeyMountSourcesAtComposeAdapter(t *testing.T) {
	for _, source := range []string{"../../run/jnpt", "./seccomp/../../../run/jnpt", "run/jnpt", "~/run/jnpt", "${KEYDIR}", "$KEYDIR", "/opt/${KEYDIR}", "${KEYDIR:-/run/jnpt}"} {
		for _, svc := range []string{mediator, fetcher, sandbox, initializer} {
			t.Run(svc+"/"+source, func(t *testing.T) {
				findings := CheckCompose(composeWithExtraMount(t, svc, source+":/p"))
				want := []string{}
				if svc != mediator {
					want = append(want, svc+": Schlüsselordner des vermittlers eingehängt")
				}
				if svc != fetcher {
					want = append(want, svc+": Deploy-Key eingehängt")
				}
				for _, reason := range want {
					found := false
					for _, v := range findings {
						if v == reason {
							found = true
						}
					}
					if !found {
						t.Errorf("missing %q for %s: %v", reason, source, findings)
					}
				}
			})
		}
	}
}

func TestSafeProjectRelativeMountAtAuthorizedServices(t *testing.T) {
	for _, svc := range []string{mediator, fetcher} {
		t.Run(svc, func(t *testing.T) {
			if v := CheckCompose(composeWithExtraMount(t, svc, "./seccomp:/x")); len(v) != 0 {
				t.Fatalf("safe project-relative source rejected: %v", v)
			}
		})
	}
	// The real file, including named volumes and a relative security_opt
	// profile (not a bind source), must remain clean.
	if v := CheckCompose([]byte(readCompose(t))); len(v) != 0 {
		t.Fatal(v)
	}
}

func composeWithExtraMount(t *testing.T, svc, mount string) []byte {
	t.Helper()
	var c map[string]any
	if err := yaml.Unmarshal([]byte(readCompose(t)), &c); err != nil {
		t.Fatal(err)
	}
	s := c["services"].(map[string]any)[svc].(map[string]any)
	s["volumes"] = append(s["volumes"].([]any), mount)
	b, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
