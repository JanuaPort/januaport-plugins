package fenster

import (
	"strings"
	"testing"
)

// Diagnose der Sonden: Urteile mit festem Grund, Quelltexte begrenzt und ohne Träger, Antworten ohne Inhalt.
func TestSourceVerdict(t *testing.T) {
	own := "https://app-demo-pc.jnpt.invalid/index.html"
	for _, tc := range []struct {
		name   string
		src    Source
		ok     bool
		reason string
	}{
		{"eigeneSeite", Source{MainFrame: true, URL: own}, true, ""},
		{"unterrahmen", Source{MainFrame: false, URL: own}, false, "rahmen"},
		{"fremdeOrigin", Source{MainFrame: true, URL: "https://example.org/"}, false, "url"},
		{"fremdeApp", Source{MainFrame: true, URL: "https://app-andere-pc.jnpt.invalid/index.html"}, false, "url"},
		{"keineHtmlSeite", Source{MainFrame: true, URL: "https://app-demo-pc.jnpt.invalid/a.svg"}, false, "url"},
		{"leer", Source{MainFrame: true}, false, "url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := SourceVerdict("demo", tc.src)
			if ok != tc.ok || reason != tc.reason {
				t.Fatalf("ok=%v grund=%q", ok, reason)
			}
			if ok != SourceAllowed("demo", tc.src) {
				t.Fatal("SourceVerdict und SourceAllowed weichen ab")
			}
		})
	}
}

func TestDiagText(t *testing.T) {
	carrier := "https://x.invalid/?t=" + "jn" + "ptpc_" + strings.Repeat("A", 30)
	if got := DiagText(carrier); strings.Contains(got, "jn"+"ptpc_") || !strings.Contains(got, "träger") {
		t.Fatalf("Träger in der Ausgabe: %q", got)
	}
	if got := DiagText(strings.Repeat("a", 5000)); len(got) > 220 {
		t.Fatalf("nicht begrenzt: %d", len(got))
	}
	if got := DiagText("a\x1b[31m\nb"); strings.ContainsAny(got, "\x1b\n") {
		t.Fatalf("Steuerzeichen unmaskiert: %q", got)
	}
	if got := DiagText("https://app-demo-pc.jnpt.invalid/index.html"); got != `"https://app-demo-pc.jnpt.invalid/index.html"` {
		t.Fatalf("%q", got)
	}
}

func TestReplySummary(t *testing.T) {
	for in, want := range map[string]string{
		`{"id":"1","ok":true,"ergebnis":{"geheim":"x"}}`:            "ok",
		`{"id":null,"ok":false,"fehler":"abgelehnt"}`:               "fehler=abgelehnt",
		`{"id":"1","ok":false,"fehler":"C:\\Users\\geheim\\x.txt"}`: "fehler=?",
		`{"id":"1","ok":false}`:                                     "fehler=?",
		`kein json`:                                                 "unlesbar",
		``:                                                          "unlesbar",
	} {
		if got := ReplySummary([]byte(in)); got != want {
			t.Errorf("%q: %q, erwartet %q", in, got, want)
		}
	}
}
