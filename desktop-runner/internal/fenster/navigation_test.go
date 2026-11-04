package fenster

import (
	"reflect"
	"strings"
	"testing"
)

func TestAppHost(t *testing.T) {
	for _, tc := range []struct {
		app, host string
		ok        bool
	}{
		{"demo", "app-demo-pc.jnpt.invalid", true},
		{"a_b", "app-a-b-pc.jnpt.invalid", true},
		{"_x_", "app--x--pc.jnpt.invalid", true},
		{"a1", "app-a1-pc.jnpt.invalid", true},
		{strings.Repeat("a", 48), "app-" + strings.Repeat("a", 48) + "-pc.jnpt.invalid", true},
		{"", "", false},
		{"Demo", "", false},
		{"a-b", "", false},
		{"a.b", "", false},
		{"../x", "", false},
		{"a b", "", false},
		{"ä", "", false},
		{strings.Repeat("a", 49), "", false},
	} {
		host, ok := AppHost(tc.app)
		if host != tc.host || ok != tc.ok {
			t.Errorf("AppHost(%q) = %q, %v", tc.app, host, ok)
		}
	}
	// Die Abbildung _ -> - darf nie zwei Apps auf einen Host legen: Bindestrich ist kein Namenszeichen.
	a, _ := AppHost("a_b")
	b, _ := AppHost("ab")
	if a == b {
		t.Fatal("Hostkollision")
	}
}

func TestNavigationAllowed(t *testing.T) {
	const host = "app-demo-pc.jnpt.invalid"
	for _, tc := range []struct {
		name, url string
		ok        bool
	}{
		// SEC-R4 F2: Nur .html-Seiten tragen die CSP; alles andere (SVG, JS, JSON …) läuft ohne und darf
		// nie als Seite geladen werden. Der Startpunkt ist /index.html.
		{"wurzel", "https://" + host + "/", false},
		{"ohnePfad", "https://" + host, false},
		{"datei", "https://" + host + "/index.html", true},
		{"unterpfad", "https://" + host + "/a/b.js", false},
		{"unterpfadHtml", "https://" + host + "/a/b.html", true},
		{"queryOhneHtml", "https://" + host + "/a?x=1&y=2", false},
		{"queryMitHtml", "https://" + host + "/a.html?x=1&y=2", true},
		{"fragmentOhneHtml", "https://" + host + "/#abschnitt", false},
		{"fragmentMitHtml", "https://" + host + "/index.html#abschnitt", true},
		{"queryOhnePfad", "https://" + host + "?x=1", false},
		{"atImPfadOhneHtml", "https://" + host + "/@evil.example/", false},
		{"atImPfadMitHtml", "https://" + host + "/@evil.example/x.html", true},
		{"leereQuery", "https://" + host + "/x.html?", true},
		{"queryUndFragment", "https://" + host + "/x.html?q=1#f", true},
		{"svg", "https://" + host + "/a.svg", false},
		{"svgImUnterordner", "https://" + host + "/img/logo.svg", false},
		{"map", "https://" + host + "/x.map", false},
		{"json", "https://" + host + "/x.json", false},
		{"txt", "https://" + host + "/x.txt", false},
		{"css", "https://" + host + "/x.css", false},
		{"js", "https://" + host + "/x.js", false},
		{"webmanifest", "https://" + host + "/x.webmanifest", false},
		{"htm", "https://" + host + "/x.htm", false},
		{"grossHTML", "https://" + host + "/x.HTML", false},
		{"gemischtHtml", "https://" + host + "/x.Html", false},
		{"htmlDannSvg", "https://" + host + "/x.html.svg", false},
		{"htmlMitSlash", "https://" + host + "/x.html/", false},
		{"svgMitHtmlInQuery", "https://" + host + "/x.svg?a=.html", false},
		{"svgMitHtmlImFragment", "https://" + host + "/x.svg#.html", false},
		{"nurEndung", "https://" + host + "/.html", false},
		{"nurEndungImUnterordner", "https://" + host + "/a/.html", false},
		{"prozentSlash", "https://" + host + "/x.svg%2f..%2fy.html", false},
		{"prozentSlashGross", "https://" + host + "/a%2Fb.html", false},
		{"prozentBackslash", "https://" + host + "/a%5cb.html", false},
		{"prozentNul", "https://" + host + "/x%00.html", false},
		{"prozentFrage", "https://" + host + "/a%3f.html", false},
		{"prozentRaute", "https://" + host + "/a%23.html", false},
		{"prozentUmlaut", "https://" + host + "/%C3%A4.html", true},
		{"atFremdHtml", "https://" + host + "@evil.example/x.html", false},
		{"portHtml", "https://" + host + ":8443/x.html", false},
		{"suffixHtml", "https://" + host + ".evil.example/x.html", false},
		{"fremderHost", "https://evil.example/", false},
		{"andereApp", "https://app-andere-pc.jnpt.invalid/", false},
		{"subdomain", "https://x." + host + "/", false},
		{"suffixAngriff", "https://" + host + ".evil.example/", false},
		{"praefixAngriff", "https://evil" + host + "/", false},
		{"trailingDot", "https://" + host + "./", false},
		{"http", "http://" + host + "/", false},
		{"file", "file:///C:/Windows/win.ini", false},
		{"fileMitHost", "file://" + host + "/x", false},
		{"data", "data:text/html,<script>1</script>", false},
		{"about", "about:blank", false},
		{"aboutSrcdoc", "about:srcdoc", false},
		{"javascript", "javascript:alert(1)", false},
		{"blob", "blob:https://" + host + "/uuid", false},
		{"viewSource", "view-source:https://" + host + "/", false},
		{"edge", "edge://settings", false},
		{"schemaRelativ", "//" + host + "/", false},
		{"grossSchema", "HTTPS://" + host + "/x.html", false},
		{"grossHost", "https://APP-DEMO-PC.jnpt.invalid/x.html", false},
		{"grossGemischt", "https://App-Demo-pc.jnpt.invalid/x.html", false},
		{"port443", "https://" + host + ":443/", false},
		{"port8443", "https://" + host + ":8443/", false},
		{"leererPort", "https://" + host + ":/", false},
		{"userinfo", "https://user@" + host + "/", false},
		{"userinfoPasswort", "https://user:pw@" + host + "/", false},
		{"leereUserinfo", "https://@" + host + "/", false},
		{"atFremd", "https://" + host + "@evil.example/", false},
		{"atNachHost", "https://" + host + "@", false},
		{"backslash", "https://" + host + `\@evil.example/`, false},
		{"backslashHtml", "https://" + host + `\@evil.example/x.html`, false},
		{"backslashPfad", "https://" + host + `/a\b`, false},
		{"backslashPfadHtml", "https://" + host + `/a\b.html`, false},
		{"leerzeichen", "https://" + host + "/a b.html", false},
		{"zeilenumbruch", "https://" + host + "/\nx", false},
		{"tab", "https://\t" + host + "/", false},
		{"nul", "https://" + host + "/\x00", false},
		{"zeilenumbruchHtml", "https://" + host + "/\nx.html", false},
		{"nulHtml", "https://" + host + "/\x00.html", false},
		{"prozentHost", "https://" + host + "%2e/", false},
		{"homoglyph", "https://\u0430pp-demo-pc.jnpt.invalid/", false},
		{"leer", "", false},
		{"nurSchema", "https://", false},
		{"fuehrenderLeerraum", " https://" + host + "/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NavigationAllowed("demo", tc.url); got != tc.ok {
				t.Fatalf("NavigationAllowed(%q) = %v", tc.url, got)
			}
		})
	}
	for _, app := range []string{"", "Demo", "a-b", "../demo", "demo/x"} {
		if NavigationAllowed(app, "https://"+host+"/") {
			t.Errorf("ungültiger Appname %q zugelassen", app)
		}
	}
}

func TestMessageAllowed(t *testing.T) {
	const url = "https://app-demo-pc.jnpt.invalid/index.html"
	ok := Source{MainFrame: true, URL: url}
	for _, befehl := range []string{"status", "anhalten", "werkzeuge", "werkzeug"} {
		if !MessageAllowed("demo", ok, befehl) {
			t.Errorf("Befehl %q abgewiesen", befehl)
		}
	}
	for _, tc := range []struct {
		name   string
		app    string
		src    Source
		befehl string
	}{
		{"unterrahmen", "demo", Source{MainFrame: false, URL: url}, "status"},
		{"fremdeOrigin", "demo", Source{MainFrame: true, URL: "https://evil.example/"}, "status"},
		{"andereApp", "demo", Source{MainFrame: true, URL: "https://app-andere-pc.jnpt.invalid/"}, "status"},
		{"aboutBlank", "demo", Source{MainFrame: true, URL: "about:blank"}, "status"},
		{"leereOrigin", "demo", Source{MainFrame: true}, "status"},
		{"unbekannterAppname", "Demo", ok, "status"},
		{"pause", "demo", ok, "pause"},
		{"setupStart", "demo", ok, "setup_start"},
		{"fortsetzen", "demo", ok, "fortsetzen"},
		{"leer", "demo", ok, ""},
		{"gross", "demo", ok, "Status"},
		{"mitLeerzeichen", "demo", ok, "status "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if MessageAllowed(tc.app, tc.src, tc.befehl) {
				t.Fatal("hätte abgewiesen werden müssen")
			}
		})
	}
	if SourceAllowed("demo", Source{MainFrame: false, URL: url}) || !SourceAllowed("demo", ok) {
		t.Fatal("SourceAllowed")
	}
}

func TestBefehleGeschlossen(t *testing.T) {
	if !reflect.DeepEqual(Befehle(), []string{"anhalten", "status", "werkzeug", "werkzeuge"}) {
		t.Fatal(Befehle())
	}
}

func TestStartURL(t *testing.T) {
	if got := StartURL("demo"); got != "https://app-demo-pc.jnpt.invalid/index.html" {
		t.Fatal(got)
	}
	if !NavigationAllowed("demo", StartURL("demo")) || !SourceAllowed("demo", Source{MainFrame: true, URL: StartURL("demo")}) {
		t.Fatal("Startpunkt nicht zugelassen")
	}
	for _, app := range []string{"", "Demo", "a-b", "../x"} {
		if StartURL(app) != "" {
			t.Errorf("StartURL(%q) nicht leer", app)
		}
	}
}

// Eine Nicht-HTML-Datei der eigenen App ist auch als Nachrichtenquelle ungültig.
func TestMessageAllowedNurVonHTMLSeiten(t *testing.T) {
	for _, u := range []string{"https://app-demo-pc.jnpt.invalid/x.svg", "https://app-demo-pc.jnpt.invalid/", "https://app-demo-pc.jnpt.invalid/a.js"} {
		if MessageAllowed("demo", Source{MainFrame: true, URL: u}, "status") {
			t.Errorf("Nachricht von %s zugelassen", u)
		}
	}
}
