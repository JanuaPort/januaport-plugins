package apphost

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// proxyUpstream ersetzt das Gateway hinter dem Proxy durch einen Testserver.
func (f *fixture) proxyUpstream(h http.HandlerFunc) {
	f.t.Helper()
	gw := httptest.NewServer(h)
	f.t.Cleanup(gw.Close)
	f.h.c.GatewayURL = gw.URL
}

// browserProxyCall ist ein /mcp-Aufruf des Browsers, dessen Kontext der Test abbrechen kann.
func (f *fixture) browserProxyCall(ctx context.Context, query string) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, "POST", f.server.URL+"/mcp"+query, strings.NewReader(`{}`))
	if err != nil {
		f.t.Fatal(err)
	}
	r.Host = "a.apps.example.test"
	r.Header = proxyHeaders()
	return (&http.Client{}).Do(r)
}

// shrinkPackageClient macht die 30 s des Paketabruf- und Anmelde-Clients klein.
// Die Kontrolle in den Tests belegt, dass dieser Client den langsamen Upstream
// wirklich abbrechen würde.
func shrinkPackageClient(f *fixture, d time.Duration) {
	f.h.client.Timeout = d
	f.h.client.Transport.(*http.Transport).ResponseHeaderTimeout = d
}

// awaitAbort wartet, bis der Upstream-Kontext endet. Der Körper wird zuerst
// gelesen: net/http erkennt das Trennen des Clients erst, nachdem der Handler
// den Körper bis EOF gelesen hat (sonst bliebe der Kontext offen). Die Frist
// ist ein Sicherheitsnetz, damit ein Fehler als Fehlschlag statt als Hänger endet.
func awaitAbort(r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	select {
	case <-r.Context().Done():
	case <-time.After(20 * time.Second):
	}
}

// slowFor wartet d oder bis die Anfrage endet; false heißt abgebrochen.
func slowFor(r *http.Request, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-r.Context().Done():
		return false
	}
}

// #1424: Ein Werkzeugaufruf, dessen Antwort-Header später kommen als die
// Frist des Paketabruf-Clients, wird nicht mit 502 abgeschnitten.
func TestProxySlowHeadersOutlastPackageClientDeadline(t *testing.T) {
	f := setupWith(t, func(c *Config) { c.ProxyTimeout = 10 * time.Second })
	shrinkPackageClient(f, 100*time.Millisecond)
	f.proxyUpstream(func(w http.ResponseWriter, r *http.Request) {
		if !slowFor(r, 500*time.Millisecond) {
			return
		}
		_, _ = io.WriteString(w, `{"late":true}`)
	})
	if _, err := f.h.request(context.Background(), "GET", "/slow", "k", nil); err == nil {
		t.Fatal("Kontrolle: der Paketabruf-Client hätte den langsamen Upstream abbrechen müssen")
	}
	resp, err := f.browserProxyCall(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	requireStatus(t, resp, 200)
	if got := contents(t, resp); got != `{"late":true}` {
		t.Fatalf("Körper = %q", got)
	}
}

// #1424: Sind die Header sofort da und der Körper kommt verzögert, kommt er
// vollständig an (Client.Timeout begrenzte früher auch das Körperlesen).
func TestProxySlowBodyArrivesComplete(t *testing.T) {
	f := setupWith(t, func(c *Config) { c.ProxyTimeout = 10 * time.Second })
	shrinkPackageClient(f, 100*time.Millisecond)
	f.proxyUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: eins\n\n")
		w.(http.Flusher).Flush()
		if !slowFor(r, 500*time.Millisecond) {
			return
		}
		_, _ = io.WriteString(w, "data: zwei\n\n")
	})
	resp, err := f.browserProxyCall(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	requireStatus(t, resp, 200)
	if got := contents(t, resp); got != "data: eins\n\ndata: zwei\n\n" {
		t.Fatalf("Körper = %q", got)
	}
}

// #1424: Bricht der Browser ab, wird der Upstream-Kontext abgebrochen.
func TestProxyBrowserAbortCancelsUpstream(t *testing.T) {
	f := setupWith(t, func(c *Config) { c.ProxyTimeout = time.Minute })
	entered, cancelled := make(chan struct{}), make(chan struct{})
	f.proxyUpstream(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		awaitAbort(r)
		close(cancelled)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := f.browserProxyCall(ctx, ""); err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Upstream wurde nie erreicht")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Upstream-Kontext wurde nach dem Abbruch des Browsers nicht abgebrochen")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Browser-Aufruf kehrt nach dem Abbruch nicht zurück")
	}
}

// #1424: Die Frist der Anfrage ist die Obergrenze: ohne Header endet der
// Aufruf mit 502 und der Upstream-Kontext wird abgebrochen.
func TestProxyDeadlineBoundsHeaderWait(t *testing.T) {
	f := setupWith(t, func(c *Config) { c.ProxyTimeout = 300 * time.Millisecond })
	cancelled := make(chan struct{})
	f.proxyUpstream(func(w http.ResponseWriter, r *http.Request) {
		awaitAbort(r)
		close(cancelled)
	})
	start := time.Now()
	resp, err := f.browserProxyCall(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	requireStatus(t, resp, 502)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Obergrenze greift nicht: %v", time.Since(start))
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Upstream-Kontext nicht abgebrochen")
	}
}

// #1424: Ein Kopierfehler nach dem Header-Versand wird geloggt (nur fester Code,
// kein Inhalt, kein Token, keine Query); der Browser sieht den bis dahin
// gelieferten Teil.
func TestProxyCopyFailureIsLoggedWithoutContent(t *testing.T) {
	f := setupWith(t, func(c *Config) { c.ProxyTimeout = 300 * time.Millisecond })
	f.proxyUpstream(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "body-canary-teil")
		w.(http.Flusher).Flush()
		awaitAbort(r)
	})
	resp, err := f.browserProxyCall(context.Background(), "?query=copy-query-canary")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	requireStatus(t, resp, 200)
	_, _ = io.ReadAll(resp.Body)
	logs := f.logs.String()
	if !strings.Contains(logs, "apphost_proxy_copy_failed") {
		t.Fatalf("Kopierfehler nicht geloggt: %q", logs)
	}
	for _, secret := range []string{credential("app"), credential("apphost"), "body-canary-teil", "copy-query-canary"} {
		if strings.Contains(logs, secret) {
			t.Fatal("Geheimnis oder Inhalt im Log")
		}
	}
}

// Ein sauber beendeter Aufruf loggt keinen Kopierfehler.
func TestProxyCompleteCallLogsNoCopyFailure(t *testing.T) {
	f := setup(t)
	requireStatus(t, f.request("POST", "a.apps.example.test", "/mcp", proxyHeaders(), `{}`), 200)
	if strings.Contains(f.logs.String(), "apphost_proxy_copy_failed") {
		t.Fatal("Kopierfehler ohne Fehler")
	}
}

// Der Proxy-Client ist ein zweiter Client, aber kein zweiter Netzweg: gleiche
// Transport-Vorgaben (kein Umgebungs-Proxy, derselbe Dialer), keine
// Weiterleitungen, keine Gesamtfrist am Client; die 30 s von Paketabruf und
// Anmeldung bleiben.
func TestProxyClientKeepsNetworkRestrictions(t *testing.T) {
	f := setup(t)
	if f.h.client.Timeout != 30*time.Second || f.h.client.Transport.(*http.Transport).ResponseHeaderTimeout != 30*time.Second {
		t.Fatal("Paketabruf und Anmeldung behalten ihre 30 Sekunden")
	}
	if f.h.proxyClient == f.h.client || f.h.proxyClient.Timeout != 0 {
		t.Fatal("Proxy braucht einen eigenen Client ohne Gesamtfrist")
	}
	tr, ok := f.h.proxyClient.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil || tr.DialContext == nil {
		t.Fatal("Proxy-Transport ohne Umgebungs-Proxy und eigenen Dialer")
	}
	if tr.ResponseHeaderTimeout != f.h.proxyTimeout {
		t.Fatalf("ResponseHeaderTimeout %v passt nicht zur Proxy-Frist %v", tr.ResponseHeaderTimeout, f.h.proxyTimeout)
	}
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(target.Close)
	f.proxyUpstream(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	})
	resp, err := f.browserProxyCall(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	requireStatus(t, resp, http.StatusFound)
	if hits.Load() != 0 {
		t.Fatal("Proxy folgt einer Weiterleitung")
	}
}

// Ohne Angabe gilt die Wandzeit-Decke des Vertrags (86400 s) plus Puffer; sie
// ist die Obergrenze für Kontext und Header-Wartezeit.
func TestProxyDefaultDeadlineCoversContractMaxWall(t *testing.T) {
	f := setup(t)
	const maxWall = 86400 * time.Second
	if f.h.proxyTimeout <= maxWall || f.h.proxyTimeout > maxWall+10*time.Minute {
		t.Fatalf("Standardfrist %v: Decke %v plus kleiner Puffer erwartet", f.h.proxyTimeout, maxWall)
	}
}

// I-2 gilt auch für den Proxy-Transport: explizites TCP, unabhängig von GODEBUG.
func TestProxyTransportExplicitTCPUnderMultipathEnvironment(t *testing.T) {
	t.Setenv("GODEBUG", "multipathtcp=1")
	l, err := Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			accepted <- c
		}
	}()
	c, err := newProxyTransport(time.Minute).DialContext(context.Background(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	peer := <-accepted
	defer func() { _ = peer.Close() }()
	if enabled, err := c.(*net.TCPConn).MultipathTCP(); err != nil || enabled {
		t.Fatal("I-2: Proxy-Verbindung nutzt MPTCP", err)
	}
}
