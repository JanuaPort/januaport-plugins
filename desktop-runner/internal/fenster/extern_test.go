package fenster

import (
	"strings"
	"testing"
)

func TestExternalLink(t *testing.T) {
	for _, tc := range []struct {
		name, raw, host string
		ok              bool
	}{
		{"einfach", "https://example.org/", "example.org", true},
		{"pfadQuery", "https://docs.example.org/a/b?x=1#f", "docs.example.org", true},
		{"ohnePfad", "https://example.org", "example.org", true},
		{"port", "https://example.org:8443/x", "example.org:8443", true},
		{"punycode", "https://xn--bcher-kva.example/", "xn--bcher-kva.example", true},
		{"http", "http://example.org/", "", false},
		{"file", "file:///C:/x", "", false},
		{"data", "data:text/html,x", "", false},
		{"javascript", "javascript:alert(1)", "", false},
		{"mailto", "mailto:a@example.org", "", false},
		{"ms", "ms-settings:network", "", false},
		{"userinfo", "https://user@example.org/", "", false},
		{"userinfoPasswort", "https://user:pw@example.org/", "", false},
		{"atImHost", "https://example.org@evil.test/", "", false},
		{"leereUserinfo", "https://@example.org/", "", false},
		{"grossSchema", "HTTPS://example.org/", "", false},
		{"leeresHost", "https:///x", "", false},
		{"nurSchema", "https://", "", false},
		{"backslash", `https://example.org\@evil.example/`, "", false},
		{"leerzeichen", "https://example.org/a b", "", false},
		{"zeilenumbruch", "https://example.org/\nx", "", false},
		{"nul", "https://example.org/\x00", "", false},
		{"umlautHost", "https://b\u00fccher.example/", "", false},
		{"ipv6", "https://[::1]/", "", false},
		{"leererPort", "https://example.org:/", "", false},
		{"portKeineZahl", "https://example.org:abc/", "", false},
		{"port65536", "https://example.org:65536/", "", false},
		{"prozentHost", "https://example%2eorg/", "", false},
		{"unterstrichHost", "https://ex_ample.org/", "", false},
		{"punktAmAnfang", "https://.example.org/", "", false},
		{"zuLang", "https://example.org/" + strings.Repeat("a", 2100), "", false},
		{"leer", "", "", false},
		{"fuehrenderLeerraum", " https://example.org/", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, ok := ExternalLink(tc.raw)
			if host != tc.host || ok != tc.ok {
				t.Fatalf("ExternalLink(%q) = %q, %v", tc.raw, host, ok)
			}
		})
	}
}

func TestDecide(t *testing.T) {
	const own = "https://app-demo-pc.jnpt.invalid/index.html"
	for _, tc := range []struct {
		name, raw, host string
		want            Decision
	}{
		{"eigeneSeite", own, "", DecisionAllow},
		{"eigeneUnterseite", "https://app-demo-pc.jnpt.invalid/a.html?b#c", "", DecisionAllow},
		{"eigenesSvgBlockiert", "https://app-demo-pc.jnpt.invalid/a.svg", "", DecisionBlock},
		{"eigeneWurzelBlockiert", "https://app-demo-pc.jnpt.invalid/", "", DecisionBlock},
		{"fremderHttps", "https://example.org/x", "example.org", DecisionPrompt},
		{"andereApp", "https://app-andere-pc.jnpt.invalid/", "", DecisionBlock},
		{"http", "http://example.org/", "", DecisionBlock},
		{"file", "file:///C:/Windows/win.ini", "", DecisionBlock},
		{"data", "data:text/html,x", "", DecisionBlock},
		{"about", "about:blank", "", DecisionBlock},
		{"blob", "blob:https://app-demo-pc.jnpt.invalid/x", "", DecisionBlock},
		{"userinfo", "https://user@example.org/", "", DecisionBlock},
		{"eigenerHostMitPort", "https://app-demo-pc.jnpt.invalid:8443/", "", DecisionBlock},
		{"leer", "", "", DecisionBlock},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, host := Decide("demo", tc.raw)
			if got != tc.want || host != tc.host {
				t.Fatalf("Decide(%q) = %v, %q", tc.raw, got, host)
			}
		})
	}
	if d, _ := Decide("Demo", own); d != DecisionBlock {
		t.Fatal("ungültiger Appname")
	}
}

func TestLayout(t *testing.T) {
	for _, tc := range []struct {
		name         string
		w, h         int
		bar          bool
		view, barBox Rect
	}{
		{"ohneLeiste", 800, 600, false, Rect{0, 0, 800, 600}, Rect{}},
		{"mitLeiste", 800, 600, true, Rect{0, BarHeight, 800, 600 - BarHeight}, Rect{0, 0, 800, BarHeight}},
		{"zuNiedrigMitLeiste", 800, BarHeight, true, Rect{}, Rect{0, 0, 800, BarHeight}},
		{"niedrigerAlsLeiste", 800, 10, true, Rect{}, Rect{0, 0, 800, 10}},
		{"negativ", -5, -5, true, Rect{}, Rect{}},
		{"null", 0, 0, false, Rect{}, Rect{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, bar := Layout(tc.w, tc.h, tc.bar)
			if view != tc.view || bar != tc.barBox {
				t.Fatalf("Layout = %+v, %+v", view, bar)
			}
		})
	}
}

func TestFensterTexte(t *testing.T) {
	if WindowTitle("kasse") != "JanuaPort – kasse" {
		t.Fatal(WindowTitle("kasse"))
	}
	if TestBarText != "Test – Testgerät" {
		t.Fatal(TestBarText)
	}
	if got := LinkPromptText("example.org"); !strings.Contains(got, "example.org") || !strings.Contains(got, "Browser") {
		t.Fatal(got)
	}
}
