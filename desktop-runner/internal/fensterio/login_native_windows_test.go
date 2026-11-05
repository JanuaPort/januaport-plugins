//go:build windows

package fensterio

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

func TestWindowsWebViewAnmeldeknopf(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		s, o, _ := b.openProbe("probe", map[string]string{"index.html": probePage(`<script src="probe.js"></script>`, ""), "probe.js": probeJS}, nil)
		var calls atomic.Int32
		l := NewLogin(NewGateway(nil, "probe"), o.Host, func(ctx context.Context) (client.PersonSession, error) {
			calls.Add(1)
			<-ctx.Done()
			return client.PersonSession{}, ctx.Err()
		}, nil)
		o.login = l
		instance, e := moduleHandle()
		if e != nil {
			t.Fatal(e)
		}
		if e = s.createLoginControls(instance); e != nil {
			t.Fatal(e)
		}
		s.layout()
		send := user32.NewProc("SendMessageW")
		if e = send.Find(); e != nil {
			t.Fatal(e)
		}
		send.Call(s.loginButton, 0x00f5, 0, 0) // BM_CLICK am echten Win32-Knopf
		if !pump(t, s, time.Second, func() bool { return calls.Load() == 1 }) {
			t.Fatal("Knopf startet nicht")
		}
		send.Call(s.loginButton, 0x00f5, 0, 0)
		if calls.Load() != 1 {
			t.Fatal("Mehrfachklick startet zweiten Versuch")
		}
		l.Close()
	})
}

// BOX, nur JNPT_NATIVE_WEBVIEW_PROBE=1: gleicher Name bei zwei Personen,
// echte Navigation -> ClearBrowsingData -> Reload; sehende Persistenzkontrolle.
func TestWindowsWebViewPersonenwechselUndAbmelden(t *testing.T) {
	b := newProbeBed(t)
	b.run(t, probeFrist, func(t *testing.T) {
		s, o, _ := b.openProbe("probe", map[string]string{
			"index.html": probePage(`<script src="probe.js"></script>`, `<script src="konto.js"></script>`),
			"probe.js":   probeJS,
			"sw.js":      `self.addEventListener("install",()=>self.skipWaiting());`,
			"konto.js": `(async function(){
const dbs=await indexedDB.databases(); const sw=await navigator.serviceWorker.getRegistrations();
send("bestand",{local:localStorage.getItem("konto"),session:sessionStorage.getItem("konto"),db:dbs.some(d=>d.name==="konto"),sw:sw.length});
localStorage.setItem("konto","synthetisch");sessionStorage.setItem("konto","synthetisch");
await new Promise((resolve,reject)=>{const r=indexedDB.open("konto",1);r.onupgradeneeded=()=>r.result.createObjectStore("werte");r.onsuccess=()=>{r.result.close();resolve();};r.onerror=reject;});
await navigator.serviceWorker.register("sw.js");await navigator.serviceWorker.ready;
setInterval(()=>localStorage.setItem("konto","synthetisch"),10);send("gespeichert",{});
})().catch(()=>send("speicherfehler",{}));`,
		}, nil)
		waitStored := func() {
			if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("gespeichert") || b.gw.has("speicherfehler") }) || b.gw.has("speicherfehler") {
				t.Fatal("Speicherkontrolle nicht beobachtbar", b.gw.snapshot())
			}
		}
		waitStored()
		b.gw.reset()
		if e := s.view.Navigate(fenster.StartURL("probe")); e != nil {
			t.Fatal(e)
		}
		waitStored()
		if !b.gw.has(`"local":"synthetisch"`) || !b.gw.has(`"db":true`) || !b.gw.has(`"sw":1`) {
			t.Fatal("Persistenzkontrolle blind", b.gw.snapshot())
		}
		person := personForTest(t)
		person.Name = "Gleich"
		person.Changed = false
		l := NewLogin(NewGateway(nil, "probe"), o.Host, func(context.Context) (client.PersonSession, error) { return person, nil }, nil)
		o.login = l
		l.name = "Gleich"
		l.signed = true
		l.gateway.install(person.Session)
		b.gw.reset()
		l.Start()
		if !pump(t, s, 20*time.Second, func() bool { return !l.busy }) {
			t.Fatal("gleiche Person")
		}
		if b.gw.has("bestand") {
			t.Fatal("gleiche Person wurde neu geladen")
		}
		for _, logout := range []bool{false, true} {
			b.gw.reset()
			if logout {
				l.SignOut(s)
			} else {
				person.Changed = true
				l.Start()
			}
			waitStored()
			if !pump(t, s, 20*time.Second, func() bool { return !l.busy }) {
				t.Fatal("Wechsel haengt")
			}
			rows := strings.Join(b.gw.matching("bestand"), " ")
			if !b.gw.has(`"local":null`) || !b.gw.has(`"session":null`) || !b.gw.has(`"db":false`) || !b.gw.has(`"sw":0`) {
				t.Fatal("Browserdaten ueberleben", rows)
			}
		}
	})
}
