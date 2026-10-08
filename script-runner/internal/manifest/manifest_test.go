package manifest

import (
	"errors"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

const valid = `name: invoice_check
description: Prüft offene Rechnungen gegen die Kartei.
entry: main.py
input_schema:
  type: object
  properties:
    limit: {type: integer}
tools:
  - kartei_find
  - memory_append
timeout_s: 20
`

func TestParseValid(t *testing.T) {
	m, err := Parse([]byte(valid), "invoice_check")
	if err != nil {
		t.Fatal(err)
	}
	if m.Entry != "main.py" || m.TimeoutS != 20 || len(m.Tools) != 2 {
		t.Fatalf("Manifest = %+v", m)
	}
	if m.InputSchema["type"] != "object" {
		t.Fatalf("InputSchema = %v", m.InputSchema)
	}
}

func TestParseRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string) string
		reason string
	}{
		{"Name weicht vom Pin ab", repl("name: invoice_check", "name: other"), contract.ReasonManifestInvalid},
		{"Beschreibung leer", repl("description: Prüft offene Rechnungen gegen die Kartei.", "description: ''"), contract.ReasonManifestInvalid},
		{"Beschreibung zu lang", repl("description: Prüft offene Rechnungen gegen die Kartei.", "description: "+strings.Repeat("x", 1025)), contract.ReasonManifestInvalid},
		{"Einstieg absolut", repl("entry: main.py", "entry: /etc/passwd"), contract.ReasonManifestInvalid},
		{"Einstieg mit ..", repl("entry: main.py", "entry: ../x.py"), contract.ReasonManifestInvalid},
		{"Einstieg kein .py", repl("entry: main.py", "entry: main.sh"), contract.ReasonManifestInvalid},
		{"Schema kein Objekt", repl("  type: object", "  type: array"), contract.ReasonManifestInvalid},
		{"Timeout 0", repl("timeout_s: 20", "timeout_s: 0"), contract.ReasonManifestInvalid},
		{"Timeout 26", repl("timeout_s: 20", "timeout_s: 26"), contract.ReasonManifestInvalid},
		{"Timeout fehlt", repl("timeout_s: 20\n", ""), contract.ReasonManifestInvalid},
		{"unbekanntes Feld", func(s string) string { return s + "network: true\n" }, contract.ReasonManifestInvalid},
		{"Werkzeug mit Punkt", repl("  - kartei_find", "  - kartei.find"), contract.ReasonToolNotQualified},
		{"Werkzeug groß", repl("  - kartei_find", "  - Kartei_Find"), contract.ReasonToolNotQualified},
		{"Werkzeug script_", repl("  - kartei_find", "  - script_other"), contract.ReasonToolNotQualified},
		{"Werkzeug admin_", repl("  - kartei_find", "  - admin_list_tokens"), contract.ReasonToolNotQualified},
		{"Werkzeug doppelt", repl("  - memory_append", "  - kartei_find"), contract.ReasonManifestInvalid},
		{"kein YAML", func(string) string { return "::: nope" }, contract.ReasonManifestInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.mutate(valid)), "invoice_check")
			var inv *Invalid
			if !errors.As(err, &inv) {
				t.Fatalf("err = %v, want *Invalid", err)
			}
			if inv.Reason != tt.reason {
				t.Fatalf("Reason = %q, want %q", inv.Reason, tt.reason)
			}
		})
	}
}

func TestParseAllowsBaseToolsAndEmptyList(t *testing.T) {
	for _, tools := range []string{"tools: []\n", "tools:\n  - catalog\n  - ping\n"} {
		src := strings.Replace(valid, "tools:\n  - kartei_find\n  - memory_append\n", tools, 1)
		if _, err := Parse([]byte(src), "invoice_check"); err != nil {
			t.Errorf("%q: %v", tools, err)
		}
	}
}

func TestValidToolName(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"catalog", true},
		{"kartei_find", true},
		{"github_create-branch", true},
		{"a", true},
		{"", false},
		{"1abc", false},
		{"script_x", false},
		// F4: skript_ ist kein gesperrtes Präfix mehr, nur noch ein gewöhnlicher Name.
		{"skript_x", true},
		{"admin_x", false},
		{"x y", false},
		{strings.Repeat("a", 129), false},
	}
	for _, tt := range tests {
		if got := ValidToolName(tt.in); got != tt.want {
			t.Errorf("ValidToolName(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func repl(old, new string) func(string) string {
	return func(s string) string {
		if !strings.Contains(s, old) {
			panic("Testfehler: " + old + " fehlt")
		}
		return strings.Replace(s, old, new, 1)
	}
}
