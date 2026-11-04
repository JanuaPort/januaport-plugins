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
		{"wurzel", "https://" + host + "/", true},
		{"ohnePfad", "https://" + host, true},
		{"datei", "https://" + host + "/index.html", true},
		{"unterpfad", "https://" + host + "/a/b.js", true},
		{"query", "https://" + host + "/a?x=1&y=2", true},
		{"fragment", "https://" + host + "/#abschnitt", true},
		{"queryOhnePfad", "https://" + host + "?x=1", true},
		{"atImPfad", "https://" + host + "/@evil.example/", true},
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
		{"grossSchema", "HTTPS://" + host + "/", false},
		{"grossHost", "https://APP-DEMO-PC.jnpt.invalid/", false},
		{"grossGemischt", "https://App-Demo-pc.jnpt.invalid/", false},
		{"port443", "https://" + host + ":443/", false},
		{"port8443", "https://" + host + ":8443/", false},
		{"leererPort", "https://" + host + ":/", false},
		{"userinfo", "https://user@" + host + "/", false},
		{"userinfoPasswort", "https://user:pw@" + host + "/", false},
		{"leereUserinfo", "https://@" + host + "/", false},
		{"atFremd", "https://" + host + "@evil.example/", false},
		{"atNachHost", "https://" + host + "@", false},
		{"backslash", "https://" + host + `\@evil.example/`, false},
		{"backslashPfad", "https://" + host + `/a\b`, false},
		{"leerzeichen", "https://" + host + "/a b", false},
		{"zeilenumbruch", "https://" + host + "/\nx", false},
		{"tab", "https://\t" + host + "/", false},
		{"nul", "https://" + host + "/\x00", false},
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
