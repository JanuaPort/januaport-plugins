//go:build windows

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fensterio"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/panel"
)

// JanuaPort/januaport#1342: Die Tests stellen den Ablauf des UI-Threads ohne WebView nach. Offene Fenster ersetzt
// fakeSurface, das Öffnen ersetzt die Naht openAsync; Kanäle, Karten und drainApps sind die echten.

type fakeSurface struct {
	onClosed func()
	closed   int
}

func (s *fakeSurface) Focus()                       {}
func (s *fakeSurface) Pump()                        {}
func (s *fakeSurface) UpdateApps(fensterio.AppList) {}
func (s *fakeSurface) Close() {
	s.closed++
	s.onClosed() // wie OnClosed des echten Fensters
}

type stubGateway struct{}

func (stubGateway) Werkzeuge(context.Context) (json.RawMessage, error) { return nil, nil }
func (stubGateway) Werkzeug(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

type stubLokal struct{}

func (stubLokal) Status(context.Context) (fenster.Status, error) { return fenster.Status{}, nil }
func (stubLokal) Anhalten(context.Context) error                 { return nil }

// testRig ist der Aufbau, den openApp erwartet: echte Kanäle und Karten, gestellte Öffnung und Fenster.
type testRig struct {
	a        *application
	finish   func(*fensterio.Opened, error) // das Öffnungsergebnis, das fensterio sonst aus der Goroutine meldet
	surfaces []*fakeSurface
}

func newOpening(t *testing.T, assigned ...string) *testRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	o := &testRig{}
	o.a = &application{
		model:    panel.New(),
		ctx:      ctx,
		appLists: make(chan fensterio.AppList, 4),
		opened:   make(chan openResult, 8),
		surfaces: map[string]appSurface{},
		opening:  map[string]bool{},
		apps:     fensterio.AppList{Names: assigned},
	}
	o.a.openAsync = func(_ string, done func(*fensterio.Opened, error)) { o.finish = done }
	o.a.newSurface = func(_ *fensterio.Opened, cfg fensterio.SurfaceConfig) (appSurface, error) {
		s := &fakeSurface{onClosed: cfg.OnClosed}
		o.surfaces = append(o.surfaces, s)
		return s, nil
	}
	return o
}

func newOpened(t *testing.T, name string) *fensterio.Opened {
	t.Helper()
	host, err := fensterio.NewHost(fensterio.HostConfig{App: name, Gateway: stubGateway{}, Lokal: stubLokal{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	return &fensterio.Opened{Name: name, Host: host}
}

// Der Fehler aus #1342: Öffnen gestartet, dann kommt eine Liste ohne die App, dann das Öffnungsergebnis.
func TestOeffnenNachEntzugBautKeinFenster1342(t *testing.T) {
	o := newOpening(t, "kasse", "lager")
	a := o.a
	a.openApp("kasse")
	if o.finish == nil || !a.opening["kasse"] {
		t.Fatal("Öffnen nicht gestartet")
	}

	a.appLists <- fensterio.AppList{Names: []string{"lager"}, ManifestSeq: 2}
	a.drainApps() // CloseMissing findet „kasse“ noch nicht: noch kein Fenster

	opened := newOpened(t, "kasse")
	o.finish(opened, nil)
	a.drainApps()

	if len(a.surfaces) != 0 || len(o.surfaces) != 0 {
		t.Fatalf("Fenster für entzogene App gebaut: %d", len(a.surfaces))
	}
	if !opened.Host.Closed() {
		t.Fatal("Opened.Close wurde nicht gerufen")
	}
	if a.opening["kasse"] {
		t.Fatal("opening nicht freigegeben")
	}
}

// Gegenprobe und Gegenrichtung: zugewiesene App bekommt ihr Fenster; kommt die Liste erst nach dem Fenster,
// schließt CloseMissing es wie bisher.
func TestOeffnenZugewieseneAppUndSpaeterEntzug(t *testing.T) {
	o := newOpening(t, "kasse")
	a := o.a
	a.openApp("kasse")
	opened := newOpened(t, "kasse")
	o.finish(opened, nil)
	a.drainApps()

	if len(a.surfaces) != 1 || opened.Host.Closed() || a.opening["kasse"] {
		t.Fatalf("zugewiesene App: Fenster %d, geschlossen %v", len(a.surfaces), opened.Host.Closed())
	}

	a.appLists <- fensterio.AppList{ManifestSeq: 2}
	a.drainApps()
	if len(a.surfaces) != 0 || o.surfaces[0].closed != 1 {
		t.Fatalf("Entzug nach dem Öffnen: Fenster %d, Close %d", len(a.surfaces), o.surfaces[0].closed)
	}
}

// Kommt die Liste ohne die App, während NewSurface Nachrichten pumpt, bleibt sie im Kanal (drainApps kehrt bei
// embedding sofort zurück) und schließt das gerade gebaute Fenster danach über CloseMissing.
func TestListeWaehrendNewSurfaceSchliesstDasNeueFenster(t *testing.T) {
	o := newOpening(t, "kasse", "lager")
	a := o.a
	build := a.newSurface
	a.newSurface = func(op *fensterio.Opened, cfg fensterio.SurfaceConfig) (appSurface, error) {
		a.appLists <- fensterio.AppList{Names: []string{"lager"}, ManifestSeq: 2} // trifft in der Nachrichtenpumpe ein
		a.drainApps()
		if !a.embedding || len(a.appLists) != 1 {
			t.Error("drainApps muss bei embedding zurückkehren und die Liste im Kanal lassen")
		}
		return build(op, cfg)
	}
	a.openApp("kasse")
	o.finish(newOpened(t, "kasse"), nil)
	a.drainApps()

	if len(a.surfaces) != 0 || len(o.surfaces) != 1 || o.surfaces[0].closed != 1 {
		t.Fatalf("Fenster %d, gebaut %d, Close %d", len(a.surfaces), len(o.surfaces), o.surfaces[0].closed)
	}
}
