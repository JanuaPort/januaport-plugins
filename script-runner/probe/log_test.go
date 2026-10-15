//go:build docker

package probe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLogCanary(t *testing.T) {
	const canary = "LOG-CANARY-1041-private"
	for _, mode := range []string{"normal", "large", "timeout", "error", "invalid", "empty"} {
		res, m := callReady(t, "log_canary", map[string]any{"mode": mode})
		log, _ := m["log"].(string)
		switch mode {
		case "normal":
			if log != canary+" 42 True\n\n" {
				t.Fatalf("SDK conversion/newlines: %q", log)
			}
		case "large":
			if len(log) > 64<<10 || !strings.HasSuffix(log, "\n[gekürzt]\n") || strings.Count(log, "[gekürzt]") != 1 {
				t.Fatalf("large log: bytes=%d", len(log))
			}
		case "timeout":
			if m["end"] != "timeout" || log != canary+"\n" {
				t.Fatalf("timeout lost log: %v", m)
			}
		case "error":
			if m["end"] != "error" || log != canary+"\n" {
				t.Fatalf("exception lost log: %v", m)
			}
		case "invalid":
			if log != "a\ufffdb\n" || !utf8.ValidString(log) {
				t.Fatalf("UTF-8: %q", log)
			}
		case "empty":
			if _, ok := m["log"]; ok {
				t.Fatal("empty log field present")
			}
		}
		if mode != "timeout" && mode != "error" && m["end"] != "ok" {
			t.Fatalf("%s: %v", mode, m)
		}
		if strings.Contains(text(res), canary) {
			t.Fatal("log in content")
		}
		logs := must(t, "docker", "logs", project+"-script-runner-1")
		if strings.Contains(logs, canary) {
			t.Fatal("log canary in mediator container logs")
		}
		t.Logf("log_canary/%s: end=%v log_bytes=%d, mediator logs clean", mode, m["end"], len(log))
	}
}
