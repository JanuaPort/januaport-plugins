//go:build windows

package fensterio

// Native Sonden zum Fremdserver-Befund (Sondenlauf 4): NavigationStarting bricht ab, trotzdem kam GET /ziel.html
// am Testserver an. Schicht 1 (WebResourceRequested für alles) und Schicht 2 (Netz im Browserprozess aus) sind
// Pflicht, NavigationStarting ist die dritte Schicht. Zwei Fremdserver zählen Anfragen (mit Sec-Fetch-*,
// Sec-Purpose, Purpose) und neue Verbindungen: einer auf Loopback, einer auf der eigenen IPv4-Adresse des Gasts.
//
//	TestWindowsWebViewFremdserverNull     alle Vektoren, beide Server, beide Schichten an: 0 Anfragen, 0 Verbindungen
//	TestWindowsWebViewFremdserverUrsache  dieselben Vektoren auf Loopback ohne Schichten, nur Schicht 1, nur
//	                                      Schicht 2: nennt, was ausleitet, ohne etwas zu behaupten (kein Pass/Fail)
//	TestWindowsWebViewServiceWorkerWirdGeloescht   ClearBrowsingData erfasst Service Worker
//	TestWindowsWebViewRichtlinienHinweis  Proxy-Richtlinien und Umgebung, die Schicht 2 übersteuern könnten
//
// Laufzeit beachten: mehrere Minuten, daher -test.timeout 20m.

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

type exfilVector struct {
	name  string
	head  string // zusätzlich im <head> nach probe.js
	body  string
	files map[string]string
}

// exfilVectors baut alle Wege, auf denen eine Seite etwas zu einem fremden Server schicken könnte.
// x ist die volle Ziel-URL (…/ziel.html), o der Ursprung (…:port).
func exfilVectors(x, o string) []exfilVector {
	click := map[string]string{"v.js": `document.getElementById("l").click();`}
	return []exfilVector{
		{"navLocation", ``, `<script src="v.js"></script>`, map[string]string{"v.js": `location.href = "` + x + `";`}},
		{"metaRefresh", `<meta http-equiv="refresh" content="0;url=` + x + `">`, ``, nil},
		{"linkClick", ``, `<a id="l" href="` + x + `">x</a><script src="v.js"></script>`, click},
		{"preconnect", `<link rel="preconnect" href="` + o + `">`, ``, nil},
		{"dnsPrefetch", `<link rel="dns-prefetch" href="` + o + `">`, ``, nil},
		{"prefetch", `<link rel="prefetch" href="` + x + `">`, ``, nil},
		{"prerender", `<link rel="prerender" href="` + x + `">`, ``, nil},
		{"aPing", ``, `<a id="l" href="zwei.html" ping="` + x + `">x</a><script src="v.js"></script>`, click},
		{"sendBeacon", ``, `<script src="v.js"></script>`, map[string]string{"v.js": `navigator.sendBeacon("` + x + `", "x");`}},
		{"windowOpen", ``, `<script src="v.js"></script>`, map[string]string{"v.js": `window.open("` + x + `");`}},
		{"formPost", ``, `<form id="f" method="post" action="` + x + `"><input name="a" value="b"></form><script src="v.js"></script>`, map[string]string{"v.js": `document.getElementById("f").submit();`}},
		{"formGet", ``, `<form id="f" method="get" action="` + x + `"><input name="a" value="b"></form><script src="v.js"></script>`, map[string]string{"v.js": `document.getElementById("f").submit();`}},
		{"fetch", ``, `<script src="v.js"></script>`, map[string]string{"v.js": `fetch("` + x + `", {mode: "no-cors"}).catch(function(){});`}},
		{"bild", ``, `<img src="` + x + `">`, nil},
		{"rahmen", ``, `<iframe src="` + x + `"></iframe>`, nil},
		{"stilblatt", `<link rel="stylesheet" href="` + x + `">`, ``, nil},
		{"skript", ``, `<script src="` + x + `"></script>`, nil},
	}
}

// runExfil öffnet die Seite des Vektors, wartet auf ihr Skript (Kontrolle: die Seite lebt) und gibt der Seite
// Zeit, alles zu versuchen. Rückgabe: neue Anfragen, neue Verbindungen, Zahl der von Schicht 1 blockierten Anfragen.
func runExfil(t *testing.T, b *probeBed, v exfilVector, mutate func(*SurfaceConfig)) (hits, conns int32, blocked int) {
	t.Helper()
	files := map[string]string{
		"index.html": probePage(`<script src="probe.js"></script>`+v.head, v.body),
		"zwei.html":  probePage(`<script src="probe.js"></script>`, ``),
		"probe.js":   probeJS,
	}
	for name, body := range v.files {
		files[name] = body
	}
	hits0, conns0 := b.hits.Load(), b.conns.Load()
	s, _, trace := b.openProbe("probe", files, mutate)
	if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("js_ok") }) {
		t.Fatalf("%s: Seite lädt nicht (Kontrolle: Das Skript der Seite lief nie)", v.name)
	}
	pump(t, s, 3*time.Second, nil)
	for _, e := range *trace {
		if strings.HasPrefix(e, "WebResourceBlockiert ") {
			blocked++
		}
	}
	return b.hits.Load() - hits0, b.conns.Load() - conns0, blocked
}

// logNewHits gibt die Treffer-Zeilen aus, die seit from dazugekommen sind, und liefert die neue Länge.
func (b *probeBed) logNewHits(t *testing.T, from int) int {
	b.paths.Lock()
	defer b.paths.Unlock()
	for _, line := range b.hitLog[from:] {
		t.Logf("Treffer %s", line)
	}
	return len(b.hitLog)
}

// Alle Wege zu beiden Fremdservern: 0 Anfragen und 0 Verbindungen, mit sehender Kontrolle (ein Go-Client trifft
// beide Server und wird gezählt) und mit der Meldung, wie viele Anfragen Schicht 1 blockiert hat.
func TestWindowsWebViewFremdserverNull(t *testing.T) {
	b := newProbeBed(t)
	// Kontrolle: Die Server und ihre Zähler sehen Anfragen und Verbindungen.
	for _, base := range []string{b.srv.URL, b.ext.URL} {
		h, c := b.hits.Load(), b.conns.Load()
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := client.Get(base + "/kontrolle")
		if err != nil {
			t.Fatalf("Kontrolle: %s nicht erreichbar: %v", base, err)
		}
		resp.Body.Close()
		if b.hits.Load() != h+1 || b.conns.Load() < c+1 {
			t.Fatalf("Kontrolle blind: %s zählt nicht (Anfragen %d→%d, Verbindungen %d→%d)", base, h, b.hits.Load(), c, b.conns.Load())
		}
	}
	logged := b.logNewHits(t, 0)
	for _, server := range []struct{ label, url string }{{"loopback", b.srv.URL}, {"extern", b.ext.URL}} {
		for _, v := range exfilVectors(server.url+"/ziel.html", server.url) {
			t.Run(server.label+"_"+v.name, func(t *testing.T) {
				b.t = t
				b.run(t, probeFrist, func(t *testing.T) {
					hits, conns, blocked := runExfil(t, b, v, nil)
					logged = b.logNewHits(t, logged)
					t.Logf("Schicht 1 blockierte %d Anfragen", blocked)
					if hits != 0 || conns != 0 {
						t.Fatalf("%s → %s: %d Anfragen, %d Verbindungen beim Fremdserver", v.name, server.label, hits, conns)
					}
				})
			})
		}
	}
}

// Ursachensonde, kein Beleg: Welche Wege leiten ohne Schichten aus, und welche Schicht allein hält sie auf?
// Die Ausgabe nennt je Vektor Anfragen und Verbindungen samt Sec-Fetch-Dest/-Mode/-Site, Sec-Purpose und Purpose.
// Fehlschlag nur, wenn die Seite gar nicht lädt.
func TestWindowsWebViewFremdserverUrsache(t *testing.T) {
	b := newProbeBed(t)
	configs := []struct {
		name   string
		mutate func(*SurfaceConfig)
	}{
		{"ohneSchichten", func(c *SurfaceConfig) { c.noResourceFilter, c.noNetLock = true, true }},
		{"nurSchicht1", func(c *SurfaceConfig) { c.noNetLock = true }},
		{"nurSchicht2", func(c *SurfaceConfig) { c.noResourceFilter = true }},
	}
	logged := 0
	for _, cfg := range configs {
		for _, v := range exfilVectors(b.srv.URL+"/ziel.html", b.srv.URL) {
			t.Run(cfg.name+"_"+v.name, func(t *testing.T) {
				b.t = t
				b.run(t, probeFrist, func(t *testing.T) {
					hits, conns, blocked := runExfil(t, b, v, cfg.mutate)
					logged = b.logNewHits(t, logged)
					verdict := "dicht"
					if hits != 0 || conns != 0 {
						verdict = "LEITET AUS"
					}
					t.Logf("URSACHE %s / %s: %s (Anfragen %d, Verbindungen %d, von Schicht 1 blockiert %d)", cfg.name, v.name, verdict, hits, conns, blocked)
				})
			})
		}
	}
}

// Service Worker (SEC): Ein Service Worker der App überlebt sonst den Personenwechsel. ClearBrowsingData muss ihn
// erfassen; nach dem Löschen findet eine frisch geladene Seite keine Registrierung mehr.
func TestWindowsWebViewServiceWorkerWirdGeloescht(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		s, _, _ := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<script src="registrieren.js"></script>`),
			"registrieren.js": `
navigator.serviceWorker.register("sw.js").then(function(){ return navigator.serviceWorker.ready; })
  .then(function(){ return navigator.serviceWorker.getRegistrations(); })
  .then(function(r){ send("sw_registriert", {n: r.length}); })
  .catch(function(e){ send("sw_fehler", {name: e.name}); });
`,
			"sw.js":        `self.addEventListener("install", function(){ self.skipWaiting(); });`,
			"pruefen.html": probePage(`<script src="probe.js"></script>`, `<script src="pruefen.js"></script>`),
			"pruefen.js":   `navigator.serviceWorker.getRegistrations().then(function(r){ send("sw_anzahl", {n: r.length}); }).catch(function(e){ send("sw_anzahl_fehler", {name: e.name}); });`,
			"probe.js":     probeJS,
		}, nil)
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("sw_registriert") || b.gw.has("sw_fehler") }) {
			t.Fatal("Seite meldet nichts zum Service Worker")
		}
		if b.gw.has("sw_fehler") {
			t.Fatalf("Service Worker lässt sich nicht registrieren (Kontrolle blind): %v", b.gw.snapshot())
		}
		if !b.gw.has(`"n":1`) {
			t.Fatalf("keine Registrierung gezählt: %v", b.gw.snapshot())
		}
		ok := make(chan bool, 1)
		if err := s.ClearBrowsingData(func(good bool) { ok <- good }); err != nil {
			t.Fatalf("ClearBrowsingData: %v", err)
		}
		cleared := false
		if !pump(t, s, 20*time.Second, func() bool {
			select {
			case cleared = <-ok:
				return true
			default:
				return false
			}
		}) || !cleared {
			t.Fatal("ClearBrowsingData meldet keinen Erfolg")
		}
		host, _ := fenster.AppHost("probe")
		if err := s.view.Navigate("https://" + host + "/pruefen.html"); err != nil {
			t.Fatalf("Navigate: %v", err)
		}
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("sw_anzahl") }) {
			t.Fatalf("Prüfseite meldet nichts: %v", b.gw.snapshot())
		}
		if !b.gw.has(`sw_anzahl {"n":0}`) {
			t.Fatalf("Service Worker überlebt ClearBrowsingData: %v", b.gw.snapshot())
		}
	})
}

// Hinweis, kein Beleg: Was könnte die Argumente von Schicht 2 übersteuern? Proxy-Richtlinien der Maschine und
// des Benutzers sowie Umgebungsvariablen des WebView2-Loaders werden ausgegeben. Eine Maschinen-Richtlinie für den
// Proxy ist eine Restgrenze von Schicht 2; Schicht 1 gilt dann allein.
func TestWindowsWebViewRichtlinienHinweis(t *testing.T) {
	if !probeOptIn {
		t.Skip("opt-in: JNPT_NATIVE_WEBVIEW_PROBE=1")
	}
	found := 0
	for _, root := range []struct {
		name string
		key  registry.Key
	}{{"HKLM", registry.LOCAL_MACHINE}, {"HKCU", registry.CURRENT_USER}} {
		for _, path := range []string{`SOFTWARE\Policies\Microsoft\Edge`, `SOFTWARE\Policies\Microsoft\Edge\WebView2`, `SOFTWARE\Policies\Microsoft\Edge\WebView2\AdditionalBrowserArguments`} {
			k, err := registry.OpenKey(root.key, path, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			names, _ := k.ReadValueNames(-1)
			for _, name := range names {
				if !strings.Contains(strings.ToLower(name), "proxy") && !strings.Contains(strings.ToLower(name), "argument") {
					continue
				}
				found++
				if s, _, err := k.GetStringValue(name); err == nil {
					t.Logf("Richtlinie %s\\%s\\%s = %s", root.name, path, name, fenster.DiagText(s))
				} else if n, _, err := k.GetIntegerValue(name); err == nil {
					t.Logf("Richtlinie %s\\%s\\%s = %d", root.name, path, name, n)
				} else {
					t.Logf("Richtlinie %s\\%s\\%s (nicht lesbar)", root.name, path, name)
				}
			}
			k.Close()
		}
	}
	for _, env := range []string{"WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "WEBVIEW2_BROWSER_EXECUTABLE_FOLDER", "WEBVIEW2_USER_DATA_FOLDER", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		if v := os.Getenv(env); v != "" {
			found++
			t.Logf("Umgebung %s gesetzt: %s", env, fenster.DiagText(v))
		}
	}
	if found == 0 {
		t.Log("keine Proxy-Richtlinie und keine WebView2-Umgebungsvariable gefunden")
	} else {
		t.Logf("%d Fundstellen: Sie können Schicht 2 übersteuern; Schicht 1 (WebResourceRequested) gilt dann allein", found)
	}
}
