package fensterio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/statefixture"
)

type coreBed struct {
	srv            *httptest.Server
	revoke         atomic.Bool
	personRequired atomic.Bool
	mcpHits        atomic.Int32
	tokHits        atomic.Int32
	lastHdr        atomic.Value // http.Header der letzten MCP-Anfrage
	appToken       string
}

func newCore(t *testing.T) *coreBed {
	c := &coreBed{appToken: "jn" + "ptpc_ID." + "WERT"}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/device/v1/apps/demo/token":
			c.tokHits.Add(1)
			if c.personRequired.Load() {
				w.WriteHeader(409)
				_, _ = io.WriteString(w, `{"error":{"class":"person_required","message":"Bitte mit dem eigenen Konto anmelden."}}`)
				return
			}
			if c.revoke.Load() {
				w.WriteHeader(401)
				_, _ = io.WriteString(w, `{"error":{"class":"revoked"}}`)
				return
			}
			fmt.Fprintf(w, `{"app_token":%q,"expires_at":"2099-01-01T00:00:00Z","mcp_path":"/api/device/v1/mcp"}`, c.appToken)
		case "/api/device/v1/mcp":
			c.mcpHits.Add(1)
			c.lastHdr.Store(r.Header.Clone())
			var req struct {
				ID int64 `json:"id"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &req)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"ping"}]}}`, req.ID)
		default:
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"class":"not_found","message":"Der Auftrag oder das Paket wurde nicht gefunden."}}`)
		}
	}))
	t.Cleanup(c.srv.Close)
	return c
}

type runtimeBed struct {
	core *coreBed
	bed  *statefixture.Bed
	rt   *Runtime
	ex   *PanelExchange
}

func newRuntimeBed(t *testing.T) *runtimeBed {
	t.Helper()
	b := &runtimeBed{core: newCore(t)}
	b.bed = statefixture.New(t, t.TempDir(), b.core.srv.URL)
	b.bed.AddApp(t, "demo", map[string][]byte{"index.html": statefixture.Page("<p>x</p>"), "app.js": []byte("1")})
	b.bed.SaveManifest(t, b.bed.Apps, 1, true)
	b.ex = NewPanelExchange(func(pipe.Message) bool { return false })
	var err error
	b.rt, err = NewRuntime(RuntimeConfig{Root: b.bed.Root, Cipher: statefixture.Cipher{}, Exchange: b.ex.Exchange, Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		info, _ := d.Info()
		rel, _ := filepath.Rel(root, p)
		body := ""
		if !d.IsDir() {
			raw, _ := os.ReadFile(p)
			body = fmt.Sprint(len(raw), "/", string(raw[:min(len(raw), 64)]))
		}
		out[rel] = body + "|" + info.ModTime().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNewRuntimePrueftEingaben(t *testing.T) {
	ex := func(context.Context, pipe.Message) ([]byte, error) { return nil, nil }
	for name, cfg := range map[string]RuntimeConfig{
		"ohneWurzel":   {Cipher: statefixture.Cipher{}, Exchange: ex},
		"relativ":      {Root: "relativ/pfad", Cipher: statefixture.Cipher{}, Exchange: ex},
		"ohneCipher":   {Root: t.TempDir(), Exchange: ex},
		"ohneExchange": {Root: t.TempDir(), Cipher: statefixture.Cipher{}},
	} {
		if _, err := NewRuntime(cfg); err == nil {
			t.Errorf("%s akzeptiert", name)
		}
	}
}

func TestRuntimeAppsLiestNurDasManifest(t *testing.T) {
	b := newRuntimeBed(t)
	list, err := b.rt.Apps()
	if err != nil || len(list.Names) != 1 || list.Names[0] != "demo" || !list.TestDevice {
		t.Fatalf("%+v %v", list, err)
	}
	b.bed.SaveManifest(t, nil, 2, false)
	list, err = b.rt.Apps()
	if err != nil || len(list.Names) != 0 || list.TestDevice {
		t.Fatalf("%+v %v", list, err)
	}
}

// B4: Der Träger wird erst beim Öffnen einer App gelesen; die Liste trägt ihn nie.
func TestRuntimeAppsOhneTraeger(t *testing.T) {
	b := newRuntimeBed(t)
	list, err := b.rt.Apps()
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(list)
	for _, bad := range []string{statefixture.Token(), "token", b.core.appToken} {
		if strings.Contains(string(out), bad) || strings.Contains(fmt.Sprintf("%+v", list), bad) {
			t.Fatalf("AppList trägt %q", bad)
		}
	}
}

func TestRuntimeOpenEndToEnd(t *testing.T) {
	b := newRuntimeBed(t)
	o, err := b.rt.Open("demo")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if o.Name != "demo" || !o.TestDevice || o.Dir != filepath.Join(b.bed.Root, "apps", "demo", b.bed.Apps[0].Commit) || o.UserDataDir != filepath.Join(b.bed.Root, "webview", "demo") {
		t.Fatalf("%+v", o)
	}
	if _, err = os.Stat(filepath.Join(o.Dir, "index.html")); err != nil {
		t.Fatal(err)
	}
	// Seite -> Host -> Gateway -> Kern -> Antwort
	o.Host.Receive(fenster.Source{MainFrame: true, URL: fenster.StartURL("demo")}, `{"id":"1","befehl":"werkzeuge","args":{}}`)
	o.Host.Wait()
	got := o.Host.Drain()
	if len(got) != 1 || !strings.Contains(string(got[0]), `"ergebnis":{"tools":[{"name":"ping"}]}`) {
		t.Fatalf("%q", got)
	}
	h := b.core.lastHdr.Load().(http.Header)
	if h.Get("X-Jnpt-Desktop-Contract") != "5" || h.Get("Authorization") != "Bearer "+statefixture.Token() || h.Get("X-Jnpt-App-Token") != b.core.appToken || len(h.Values("X-Jnpt-Run-Token")) != 0 {
		t.Fatalf("Kopfzeilen %v", h)
	}
	if strings.Contains(string(got[0]), statefixture.Token()) || strings.Contains(string(got[0]), b.core.appToken) {
		t.Fatal("Antwort trägt einen Träger")
	}
}

// B3: Kein Schreiben in den Zustand, weder beim Öffnen noch im Betrieb noch bei Widerruf.
func TestRuntimeSchreibtNichtsInDenZustand(t *testing.T) {
	b := newRuntimeBed(t)
	before := tree(t, b.bed.Root)
	_, _ = b.rt.Apps()
	o, err := b.rt.Open("demo")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		o.Host.Receive(fenster.Source{MainFrame: true, URL: fenster.StartURL("demo")}, `{"id":"1","befehl":"werkzeuge","args":{}}`)
	}
	o.Host.Wait()
	o.Close()
	after := tree(t, b.bed.Root)
	if len(before) != len(after) {
		t.Fatalf("Dateimenge geändert: %d -> %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s verändert", k)
		}
	}
}

// B3: Bei „revoked“ zeigt das Bedienfeld einen festen Fehler; gelöscht wird nichts, das bleibt Sache des Läufers.
func TestRuntimeWiderrufLoeschtNichts(t *testing.T) {
	b := newRuntimeBed(t)
	b.core.revoke.Store(true)
	before := tree(t, b.bed.Root)
	o, err := b.rt.Open("demo")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	src := fenster.Source{MainFrame: true, URL: fenster.StartURL("demo")}
	o.Host.Receive(src, `{"id":"1","befehl":"werkzeuge","args":{}}`)
	o.Host.Wait()
	got := o.Host.Drain()
	if len(got) != 1 || !strings.Contains(string(got[0]), `"fehler":"nicht_angemeldet"`) {
		t.Fatalf("%q", got)
	}
	hits := b.core.tokHits.Load()
	o.Host.Receive(src, `{"id":"2","befehl":"werkzeuge","args":{}}`)
	o.Host.Wait()
	if b.core.tokHits.Load() != hits || b.core.mcpHits.Load() != 0 {
		t.Fatal("nach Widerruf noch Netzverkehr")
	}
	after := tree(t, b.bed.Root)
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s nach Widerruf verändert", k)
		}
	}
	if len(before) != len(after) {
		t.Fatal("Widerruf löschte oder legte Dateien an")
	}
}

func TestRuntimeOpenFehlerOhneNetz(t *testing.T) {
	for name, prep := range map[string]func(*runtimeBed, *testing.T){
		"unbekannteApp": func(b *runtimeBed, t *testing.T) {},
		"nichtImManifest": func(b *runtimeBed, t *testing.T) {
			b.bed.SaveManifest(t, nil, 2, false)
		},
		"siegelbruch": func(b *runtimeBed, t *testing.T) {
			p := filepath.Join(b.bed.Root, "apps", "demo", b.bed.Apps[0].Commit, "app.js")
			if err := os.WriteFile(p, []byte("evil"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"ohneZugangsdaten": func(b *runtimeBed, t *testing.T) {
			if err := os.Remove(filepath.Join(b.bed.Root, "credential")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := newRuntimeBed(t)
			prep(b, t)
			app := "demo"
			if name == "unbekannteApp" {
				app = "gibtsnicht"
			}
			if o, err := b.rt.Open(app); err == nil {
				o.Close()
				t.Fatal("geöffnet")
			}
			if b.core.mcpHits.Load()+b.core.tokHits.Load() != 0 {
				t.Fatal("Netzverkehr beim Fehlschlag")
			}
		})
	}
	b := newRuntimeBed(t)
	for _, bad := range []string{"", "Demo", "a-b", "../demo", "demo/x"} {
		if o, err := b.rt.Open(bad); err == nil {
			o.Close()
			t.Errorf("Name %q geöffnet", bad)
		}
	}
}

func TestRuntimeCloseSchliesstDenHost(t *testing.T) {
	b := newRuntimeBed(t)
	o, err := b.rt.Open("demo")
	if err != nil {
		t.Fatal(err)
	}
	o.Close()
	o.Close()
	if !o.Host.Closed() {
		t.Fatal("Host offen")
	}
	o.Host.Receive(fenster.Source{MainFrame: true, URL: fenster.StartURL("demo")}, `{"id":"1","befehl":"werkzeuge","args":{}}`)
	o.Host.Wait()
	if b.core.mcpHits.Load() != 0 {
		t.Fatal("nach Close noch Aufrufe")
	}
}

// Anhalten läuft über den vorhandenen Exchange; ohne Verbindung ist es ein fester Code.
func TestRuntimeAnhaltenOhneVerbindung(t *testing.T) {
	b := newRuntimeBed(t)
	o, err := b.rt.Open("demo")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	o.Host.Receive(fenster.Source{MainFrame: true, URL: fenster.StartURL("demo")}, `{"id":"h","befehl":"anhalten","args":{}}`)
	o.Host.Wait()
	got := o.Host.Drain()
	if len(got) != 1 || !strings.Contains(string(got[0]), `"fehler":"nicht_erreichbar"`) {
		t.Fatalf("%q", got)
	}
}

type slowCipher struct {
	statefixture.Cipher
	gate chan struct{}
}

func (c slowCipher) Open(b []byte) ([]byte, error) {
	<-c.gate
	return c.Cipher.Open(b)
}

// B1: Das Öffnen (Dateien lesen, Siegel prüfen, Zugangsdaten entschlüsseln) läuft nie auf dem UI-Thread.
func TestRuntimeOpenAsyncKehrtSofortZurueck(t *testing.T) {
	b := newRuntimeBed(t)
	gate := make(chan struct{})
	rt, err := NewRuntime(RuntimeConfig{Root: b.bed.Root, Cipher: slowCipher{gate: gate}, Exchange: b.ex.Exchange, Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		o   *Opened
		err error
	}
	done := make(chan result, 1)
	within(t, time.Second, "OpenAsync", func() {
		rt.OpenAsync("demo", func(o *Opened, err error) { done <- result{o, err} })
	})
	select {
	case <-done:
		t.Fatal("Rückruf vor Freigabe")
	case <-time.After(30 * time.Millisecond):
	}
	close(gate)
	select {
	case r := <-done:
		if r.err != nil || r.o == nil {
			t.Fatal(r.err)
		}
		r.o.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("kein Rückruf")
	}
}
