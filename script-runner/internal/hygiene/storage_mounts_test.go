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
