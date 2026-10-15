package hygiene

import (
	"gopkg.in/yaml.v3"
	"testing"
)

func TestLibsInitAndSandboxMountFailClosed(t *testing.T) {
	var base map[string]any
	if err := yaml.Unmarshal([]byte(readCompose(t)), &base); err != nil {
		t.Fatal(err)
	}
	services := base["services"].(map[string]any)
	if _, ok := services["script-runner-init"]; !ok {
		t.Fatal("cold volume has no init service")
	}
	tests := []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"init missing", func(ss, _ map[string]any) { delete(ss, "script-runner-init") }},
		{"init network", func(_, s map[string]any) { s["network_mode"] = "bridge" }},
		{"init plugin network", func(_, s map[string]any) { delete(s, "network_mode"); s["networks"] = []string{"plugins"} }},
		{"init writable root", func(_, s map[string]any) { s["read_only"] = false }},
		{"init capabilities", func(_, s map[string]any) { s["cap_drop"] = []string{} }},
		{"init privileges", func(_, s map[string]any) { s["security_opt"] = []string{} }},
		{"init root user", func(_, s map[string]any) { s["user"] = "0:0" }},
		{"init wrong command", func(_, s map[string]any) { s["command"] = []string{"mediator"} }},
		{"init restart", func(_, s map[string]any) { s["restart"] = "always" }},
		{"init socket", func(_, s map[string]any) { s["volumes"] = []string{"libs:/libs", "sockets:/run/script-runner"} }},
		{"init store", func(_, s map[string]any) { s["volumes"] = []string{"libs:/libs", "store:/var/lib/script-runner"} }},
		{"init libs readonly", func(_, s map[string]any) { s["volumes"] = []string{"libs:/libs:ro"} }},
		{"init libs missing", func(_, s map[string]any) { s["volumes"] = []string{} }},
		{"dependency missing", func(ss, _ map[string]any) { delete(ss["script-runner-sandbox"].(map[string]any), "depends_on") }},
		{"dependency only started", func(ss, _ map[string]any) {
			ss["script-runner-sandbox"].(map[string]any)["depends_on"] = map[string]any{"script-runner-init": map[string]any{"condition": "service_started"}}
		}},
		{"dependency optional", func(ss, _ map[string]any) {
			ss["script-runner-sandbox"].(map[string]any)["depends_on"] = map[string]any{"script-runner-init": map[string]any{"condition": "service_completed_successfully", "required": false}}
		}},
		{"nocopy missing", func(ss, _ map[string]any) {
			mount := ss["script-runner-sandbox"].(map[string]any)["volumes"].([]any)[1].(map[string]any)
			delete(mount["volume"].(map[string]any), "nocopy")
		}},
		{"nocopy false", func(ss, _ map[string]any) {
			mount := ss["script-runner-sandbox"].(map[string]any)["volumes"].([]any)[1].(map[string]any)
			mount["volume"].(map[string]any)["nocopy"] = false
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c map[string]any
			if err := yaml.Unmarshal([]byte(readCompose(t)), &c); err != nil {
				t.Fatal(err)
			}
			ss := c["services"].(map[string]any)
			tt.mutate(ss, ss["script-runner-init"].(map[string]any))
			b, err := yaml.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if v := CheckCompose(b); len(v) == 0 {
				t.Fatal("forbidden init/mount configuration accepted")
			}
		})
	}
}
