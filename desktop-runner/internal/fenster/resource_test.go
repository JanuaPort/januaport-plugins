package fenster

import (
	"strings"
	"testing"
)

// SEC Fremdserver-Befund: Schicht 1 (WebResourceRequested) lässt nur den eigenen virtuellen Host durch.
// Dokumente brauchen zusätzlich die .html-Regel von NavigationAllowed, Unterressourcen nur den Host.
func TestResourceAllowed(t *testing.T) {
	const h = "https://app-demo-pc.jnpt.invalid"
	for _, tc := range []struct {
		name     string
		raw      string
		document bool
		want     bool
	}{
		{"skript", h + "/app.js", false, true},
		{"stilMitQuery", h + "/a/b.css?x=1", false, true},
		{"wurzel", h + "/", false, true},
		{"dokument", h + "/index.html", true, true},
		{"dokumentOhneHtml", h + "/app.js", true, false},
		{"dokumentSvg", h + "/a.svg", true, false},
		{"httpStattHttps", "http://app-demo-pc.jnpt.invalid/app.js", false, false},
		{"fremderHost", "https://example.org/app.js", false, false},
		{"loopback", "http://127.0.0.1:8080/x", false, false},
		{"fremdeApp", "https://app-andere-pc.jnpt.invalid/app.js", false, false},
		{"suffixHost", h + ".evil.example/app.js", false, false},
		{"port", "https://app-demo-pc.jnpt.invalid:8443/app.js", false, false},
		{"userinfo", "https://user" + "@" + "app-demo-pc.jnpt.invalid/app.js", false, false},
		{"grossgeschrieben", "HTTPS://APP-DEMO-PC.JNPT.INVALID/app.js", false, false},
		{"rueckstrich", h + "/a\b.js", false, false},
		{"steuerzeichen", h + "/a\x01.js", false, false},
		{"leerzeichen", h + "/a b.js", false, false},
		{"nichtAscii", h + "/ä.js", false, false},
		{"kodierterTrenner", h + "/a%2fb.js", false, false},
		{"elternVerzeichnis", h + "/../x.js", false, false},
		{"data", "data:text/javascript,1", false, false},
		{"blob", "blob:https://app-demo-pc.jnpt.invalid/1", false, false},
		{"about", "about:blank", false, false},
		{"ftp", "ftp://app-demo-pc.jnpt.invalid/x", false, false},
		{"leer", "", false, false},
		{"zuLang", h + "/" + strings.Repeat("a", 3000), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResourceAllowed("demo", tc.raw, tc.document); got != tc.want {
				t.Fatalf("ResourceAllowed(%q, %v) = %v", tc.raw, tc.document, got)
			}
		})
	}
	if ResourceAllowed("", h+"/app.js", false) || ResourceAllowed("Bad Name", h+"/app.js", false) {
		t.Fatal("ungültiger Appname")
	}
}

// Schicht 2: Das Netz im Browserprozess ist aus. Das tote Ziel ist kein Port, den ein anderes Konto binden
// kann (kein Loopback, kein fester Port); DNS ist ebenfalls aus. Exakter Wortlaut, kein Träger darin.
func TestBrowserArguments(t *testing.T) {
	const want = `--proxy-server=http://0.0.0.0:0 --proxy-bypass-list=<-loopback> --host-resolver-rules="MAP * ~NOTFOUND" --webrtc-ip-handling-policy=disable_non_proxied_udp`
	if BrowserArguments != want {
		t.Fatalf("BrowserArguments = %q", BrowserArguments)
	}
	if strings.Join(BrowserArgs(), " ") != BrowserArguments {
		t.Fatalf("BrowserArgs() weicht ab: %q", BrowserArgs())
	}
	if EnthaeltTraeger([]byte(BrowserArguments)) || strings.Contains(strings.ToLower(BrowserArguments), "jnpt") {
		t.Fatal("Träger oder Namensbestandteil in den Browserargumenten")
	}
	for _, bad := range []string{"127.0.0.1", "localhost", "::1", ":1 "} {
		if strings.Contains(BrowserArguments, bad) {
			t.Fatalf("Browserargumente nennen %q: ein Konto könnte den Port binden", bad)
		}
	}
	for _, must := range []string{"--proxy-server=", "--proxy-bypass-list=<-loopback>", "--host-resolver-rules=", "--webrtc-ip-handling-policy=disable_non_proxied_udp"} {
		if !strings.Contains(BrowserArguments, must) {
			t.Fatalf("%s fehlt", must)
		}
	}
}
