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
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	totalBlocked := 0
	for _, server := range []struct{ label, url string }{{"loopback", b.srv.URL}, {"extern", b.ext.URL}} {
		for _, v := range exfilVectors(server.url+"/ziel.html", server.url) {
			t.Run(server.label+"_"+v.name, func(t *testing.T) {
				b.t = t
				b.run(t, probeFrist, func(t *testing.T) {
					hits, conns, blocked := runExfil(t, b, v, modeConfig(t, probeMode))
					logged = b.logNewHits(t, logged)
					t.Logf("Betriebsart %q: Schicht 1 blockierte %d Anfragen", probeMode, blocked)
					totalBlocked += blocked
					if hits != 0 || conns != 0 {
						t.Fatalf("%s → %s: %d Anfragen, %d Verbindungen beim Fremdserver", v.name, server.label, hits, conns)
					}
				})
			})
		}
	}
	filterOn := probeMode == "" || probeMode == "beide" || probeMode == "nurSchicht1"
	if filterOn && totalBlocked == 0 {
		t.Error("Kontrolle blind: Schicht 1 hat in keinem Fall eine Anfrage abgefangen")
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
		{"nurSchicht1", modeConfig(t, "nurSchicht1")},
		{"nurSchicht2", modeConfig(t, "nurSchicht2")},
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

// Kontrolle je Schicht (SEC Lauf 5), mit einer Seite ohne CSP, damit nur die jeweilige Schicht zwischen Seite und
// Server steht: ohne Schichten sieht der Server den Abruf (Kontrolle: Der Weg ist echt), mit nur Schicht 1 fängt der
// Filter ihn ab (blockiert-Spur, 0 Treffer, Fehler in der Seite), mit nur Schicht 2 scheitert er im Netz (0 Treffer,
// Fehler in der Seite, kein Filter beteiligt).
func TestWindowsWebViewSchichtenKontrollen(t *testing.T) {
	b := newProbeBed(t)
	x := b.srv.URL + "/kontrolle.js"
	files := map[string]string{
		"index.html": "<!doctype html><html><head><meta charset=\"utf-8\"><script src=\"probe.js\"></script></head><body><script src=\"v.js\"></script></body></html>",
		"probe.js":   probeJS,
		"v.js":       `fetch("` + x + `", {mode: "no-cors"}).then(function(){ send("abruf", {ok: true}); }).catch(function(){ send("abruf", {ok: false}); });`,
	}
	for _, c := range []struct {
		name     string
		mutate   func(*SurfaceConfig)
		wantLeak bool
		wantBlk  bool
		okSeite  string // "" = egal: Ein no-cors-Abruf, den Schicht 1 lokal mit 403 beantwortet, gilt der Seite als geglückt
	}{
		{"ohneSchichten", func(c *SurfaceConfig) { c.noResourceFilter, c.noNetLock = true, true }, true, false, "true"},
		{"nurSchicht1", modeConfig(t, "nurSchicht1"), false, true, ""},
		{"nurSchicht2", modeConfig(t, "nurSchicht2"), false, false, "false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b.t = t
			b.run(t, probeFrist, func(t *testing.T) {
				hits0 := b.hits.Load()
				s, _, trace := b.openProbe("probe", files, c.mutate)
				if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("abruf ") }) {
					t.Fatal("die Seite meldet das Ergebnis des Abrufs nicht")
				}
				hits := b.hits.Load() - hits0
				blocked := 0
				for _, e := range *trace {
					if strings.HasPrefix(e, "WebResourceBlockiert ") && strings.Contains(e, "/kontrolle.js") {
						blocked++
					}
				}
				switch {
				case c.wantLeak && hits == 0:
					t.Fatalf("Kontrolle blind: Ohne Schichten sieht der Server den Abruf nicht (Treffer %d)", hits)
				case !c.wantLeak && hits != 0:
					t.Fatalf("Abruf kam durch (Treffer %d, Seite: %v)", hits, b.gw.snapshot())
				case c.okSeite != "" && !b.gw.has(`"ok":`+c.okSeite):
					t.Fatalf("Die Seite meldet nicht ok=%s: %v", c.okSeite, b.gw.snapshot())
				case c.wantBlk && blocked == 0:
					t.Fatal("Kontrolle blind: Schicht 1 hat den Abruf nicht als blockiert gemeldet")
				case !c.wantBlk && blocked != 0:
					t.Fatalf("Schicht 1 meldet blockiert, obwohl sie aus ist: %d", blocked)
				}
			})
		})
	}
}

// Fail-closed (SEC Lauf 5): Ein widersprechendes Argument oder eine WEBVIEW2_*-Variable lässt das Fenster nicht
// öffnen. Fester Fehler, kein Fenster, und die Spur sagt, welches Argument oder welche Variable es war (nur Namen).
func TestWindowsWebViewBrowserpruefungFailClosed(t *testing.T) {
	b := newProbeBed(t)
	try := func(t *testing.T, mutate func(*SurfaceConfig)) (error, []string) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(probePage("", "x")), 0o600); err != nil {
			t.Fatal(err)
		}
		host, err := NewHost(HostConfig{App: "probe", Gateway: b.gw, Lokal: probeLokal{}, Wake: func() {}})
		if err != nil {
			t.Fatal(err)
		}
		dataRoot, err := os.MkdirTemp("", "probe-")
		if err != nil {
			t.Fatal(err)
		}
		b.closers = append(b.closers, func() { removeWhenFree(t, dataRoot) })
		o := &Opened{Name: "probe", Dir: dir, UserDataDir: filepath.Join(dataRoot, "webview", "probe"), Host: host}
		var mu sync.Mutex
		var trace []string
		cfg := SurfaceConfig{Probe: func(ev, detail string) { mu.Lock(); trace = append(trace, ev+" "+detail); mu.Unlock() }}
		mutate(&cfg)
		s, err := NewSurface(o, cfg)
		if err == nil {
			b.closers = append(b.closers, s.Close)
		}
		mu.Lock()
		defer mu.Unlock()
		return err, append([]string(nil), trace...)
	}
	t.Run("widersprechendesArgument", func(t *testing.T) {
		b.t = t
		b.run(t, probeFrist, func(t *testing.T) {
			err, trace := try(t, func(c *SurfaceConfig) { c.extraBrowserArgs = []string{"--proxy-server=http://0.0.0.0:1"} })
			if !errors.Is(err, ErrWebView) {
				t.Fatalf("erwartet den festen Fehler, bekam %v (Spur %v)", err, trace)
			}
			found := false
			for _, e := range trace {
				if strings.HasPrefix(e, "BrowserArgument ") && strings.Contains(e, "--proxy-server") {
					found = true
				}
			}
			if !found {
				t.Fatalf("Die Prüfung der Kommandozeile hat den Widerspruch nicht gemeldet: %v", trace)
			}
			if n := openSurfaces(); n != 0 {
				t.Fatalf("%d Fenster nach dem Fehlstart", n)
			}
		})
	})
	t.Run("webview2Variable", func(t *testing.T) {
		b.t = t
		b.run(t, probeFrist, func(t *testing.T) {
			// Der Lader hat die Variable auf leer gesetzt; ein echter Wert danach ist der Eingriff. Danach zurück auf den
			// Zustand des Laders, damit spätere Sonden ihn wiederfinden.
			old, had := os.LookupEnv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS")
			os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--disable-features=Prerender2")
			defer func() {
				if had {
					os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", old)
				} else {
					os.Unsetenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS")
				}
			}()
			err, trace := try(t, func(*SurfaceConfig) {})
			if !errors.Is(err, ErrWebView) {
				t.Fatalf("erwartet den festen Fehler, bekam %v (Spur %v)", err, trace)
			}
			found := false
			for _, e := range trace {
				if strings.HasPrefix(e, "Eingriff ") && strings.Contains(e, "WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS(laenge=29,laufend)") && !strings.Contains(e, "Prerender2") {
					found = true // Name und Länge, nie der Wert
				}
			}
			if !found {
				t.Fatalf("Der Eingriff wurde nicht gemeldet: %v", trace)
			}
			if n := openSurfaces(); n != 0 {
				t.Fatalf("%d Fenster nach dem Fehlstart", n)
			}
		})
	})
}

// logLocationConsent erklärt, warum eine Standortabfrage hängen kann: Die Windows-Datenschutzeinstellung für den
// Standort (Maschine und Benutzer) als Allow/Deny. Nur für die Fehlerausgabe der Geolocation-Sonde.
func logLocationConsent(t *testing.T) {
	t.Helper()
	for _, root := range []struct {
		name string
		key  registry.Key
	}{{"HKLM", registry.LOCAL_MACHINE}, {"HKCU", registry.CURRENT_USER}} {
		k, err := registry.OpenKey(root.key, `SOFTWARE\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\location`, registry.QUERY_VALUE)
		if err != nil {
			t.Logf("Standort-Einwilligung %s: nicht lesbar (%v)", root.name, err)
			continue
		}
		v, _, err := k.GetStringValue("Value")
		k.Close()
		t.Logf("Standort-Einwilligung %s: %s (%v)", root.name, fenster.DiagText(v), err)
	}
}
