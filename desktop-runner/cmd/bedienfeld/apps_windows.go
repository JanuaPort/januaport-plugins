//go:build windows

package main

import (
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fensterio"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/panelio"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

// K6b-2: Anbindung der App-Fenster. Dieser Thread ruft nie Gateway, Brücke, Zugangsdaten-Lesen oder das
// Pipe-Gespräch auf (TestBedienfeldUIThreadOhneNetzUndBruecke): Er startet fensterio-Goroutinen und holt
// deren Ergebnisse über Kanäle ab, geweckt per PostMessage.

type openResult struct {
	name string
	o    *fensterio.Opened
	err  error
}

// wake weckt den UI-Thread aus jeder Goroutine und blockiert nie.
func (a *application) wake() { postMessage.Call(a.window, wmEvents, 0, 0) }

// sendCommand stellt einen Befehl in die Anfragen der bestehenden Pipe-Verbindung (keine zweite Verbindung).
func (a *application) sendCommand(m pipe.Message) bool {
	select {
	case a.requests <- panelio.Request{Generation: a.gen.Load(), Commands: []pipe.Message{m}}:
		return true
	default:
		return false
	}
}

// drainApps nimmt Ergebnisse der Hintergrundarbeit ab: App-Liste, geöffnete Apps, Antworten an die Seiten.
func (a *application) drainApps() {
	// NewSurface pumpt beim Start der WebView Nachrichten; währenddessen nichts Neues anfangen.
	if a.embedding {
		return
	}
	for {
		select {
		case l := <-a.appLists:
			a.apps = l
			// Verschwindet eine App (Widerruf), schließt ihr Fenster; offene Fenster sperren die Dateien der WebView.
			fensterio.CloseMissing(a.surfaces, l.Names)
			for _, s := range a.surfaces {
				s.UpdateApps(l)
			}
			a.model.SetApps(l.Names)
			a.render()
		case r := <-a.opened:
			a.handleOpened(r)
		default:
			for _, s := range a.surfaces {
				s.Pump()
			}
			return
		}
	}
}

func (a *application) openApp(name string) {
	if a.rt == nil {
		return
	}
	if s := a.surfaces[name]; s != nil {
		s.Focus()
		return
	}
	if a.opening[name] {
		return
	}
	a.opening[name] = true
	a.rt.OpenAsync(name, func(o *fensterio.Opened, err error) {
		select {
		case a.opened <- openResult{name, o, err}:
			a.wake()
		case <-a.ctx.Done():
			if o != nil {
				o.Close()
			}
		}
	})
}

func (a *application) handleOpened(r openResult) {
	// Erst nach dem Start freigeben: ein zweiter Klick während NewSurface öffnet kein zweites Fenster auf demselben Profil.
	defer delete(a.opening, r.name)
	if r.err != nil || r.o == nil {
		a.notice("JanuaPort – App", "Die App „"+r.name+"“ ist nicht verfügbar.")
		return
	}
	name := r.name
	a.embedding = true
	s, err := fensterio.NewSurface(r.o, fensterio.SurfaceConfig{OnLink: a.askLink, OnClosed: func() { delete(a.surfaces, name) }})
	a.embedding = false
	if err != nil {
		r.o.Close()
		a.notice("JanuaPort – App", "Die App „"+r.name+"“ kann nicht angezeigt werden (WebView2).")
		return
	}
	a.surfaces[name] = s
	s.UpdateApps(a.apps)
}

func (a *application) closeSurfaces() {
	for _, s := range a.surfaces {
		s.Close()
	}
}

// askLink fragt nativ, ob ein externer https-Link im Browser öffnen soll. Höchstens ein Dialog zugleich.
func (a *application) askLink(l fensterio.Link) {
	if a.aux != 0 {
		return
	}
	a.auxLink = &l
	a.openAux("JanuaPort – Link", fenster.LinkPromptText(l.Host), []string{"Öffnen", "Abbrechen"})
}

func (a *application) notice(title, text string) {
	if a.aux != 0 {
		return
	}
	a.auxLink = nil
	a.openAux(title, text, []string{"OK"})
}

// openAux zeigt einen kleinen nativen Dialog nach denselben Regeln wie die Rückfrage: nie aktivierend,
// gewöhnliche Knöpfe, kein Standardknopf, kein Anfangsfokus.
func (a *application) openAux(title, text string, options []string) {
	sw, _, _ := getSystemMetrics.Call(0)
	sh, _, _ := getSystemMetrics.Call(1)
	x, y := max(0, (int(sw)-640)/2), max(0, (int(sh)-300)/2)
	h := createWindow(dialogExStyle, "JanuaPortBedienfeld", title, 0x80000000|0x00c00000|0x00080000, x, y, 640, 300, a.window, 0)
	if h == 0 {
		a.auxLink = nil
		return
	}
	a.aux = h
	font, _, _ := getStockObject.Call(17)
	label := createWindow(0, "STATIC", text, 0x40000000|0x10000000|0x80, 20, 20, 590, 120, h, 0)
	sendMessage.Call(label, 0x30, font, 0)
	for i, option := range options {
		b := createWindow(0, "BUTTON", option, buttonStyle, 20+i*300, 170, 285, 40, h, uintptr(200+i))
		if b == 0 {
			a.closeAux()
			return
		}
		sendMessage.Call(b, 0x30, font, 0)
	}
	showWindow.Call(h, showNoActivate)
}

func (a *application) closeAux() {
	h := a.aux
	a.aux = 0
	a.auxLink = nil
	if h != 0 {
		destroyWindow.Call(h)
	}
}

// auxCommand: Knopf 0 ist bei der Linkfrage „Öffnen“. Alles andere schließt nur.
func (a *application) auxCommand(button int) {
	link := a.auxLink
	a.closeAux()
	if link != nil && button == 0 {
		_ = fensterio.OpenInBrowser(link.URL)
	}
}
