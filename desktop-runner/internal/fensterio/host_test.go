package fensterio

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

var page = fenster.Source{MainFrame: true, URL: "https://app-demo-pc.jnpt.invalid/index.html"}

type hangGateway struct {
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
	calls   atomic.Int32
}

func newHangGateway() *hangGateway { return &hangGateway{release: make(chan struct{})} }

func (g *hangGateway) wait(ctx context.Context) (json.RawMessage, error) {
	g.calls.Add(1)
	n := g.active.Add(1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	defer g.active.Add(-1)
	select {
	case <-g.release:
		return json.RawMessage(`{"ok":1}`), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (g *hangGateway) Werkzeuge(ctx context.Context) (json.RawMessage, error) { return g.wait(ctx) }
func (g *hangGateway) Werkzeug(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return g.wait(ctx)
}

type countLokal struct {
	hang      chan struct{}
	statusN   atomic.Int32
	anhaltenN atomic.Int32
}

func (l *countLokal) Status(ctx context.Context) (fenster.Status, error) {
	l.statusN.Add(1)
	if l.hang != nil {
		select {
		case <-l.hang:
		case <-ctx.Done():
			return fenster.Status{}, ctx.Err()
		}
	}
	return fenster.Status{Zustand: "arbeitet", Netz: true}, nil
}
func (l *countLokal) Anhalten(context.Context) error { l.anhaltenN.Add(1); return nil }

type hostBed struct {
	h      *Host
	g      *hangGateway
	l      *countLokal
	wakes  atomic.Int32
	closed atomic.Int32
}

func newHostBed(t *testing.T, mutate func(*HostConfig)) *hostBed {
	t.Helper()
	b := &hostBed{g: newHangGateway(), l: &countLokal{}}
	cfg := HostConfig{App: "demo", Gateway: b.g, Lokal: b.l, Wake: func() { b.wakes.Add(1) }, OnClosed: func() { b.closed.Add(1) }, MaxGateway: 4, MaxLokal: 2}
	if mutate != nil {
		mutate(&cfg)
	}
	var err error
	if b.h, err = NewHost(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-b.g.release:
		default:
			close(b.g.release)
		}
		b.h.Close()
		b.h.Wait()
	})
	return b
}

func msg(id, befehl, args string) string {
	return fmt.Sprintf(`{"id":%q,"befehl":%q,"args":%s}`, id, befehl, args)
}

func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s blockiert (UI-Thread-Verstoß)", what)
	}
}

func replies(t *testing.T, h *Host) []map[string]json.RawMessage {
	t.Helper()
	var out []map[string]json.RawMessage
	for _, b := range h.Drain() {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("Zeitüberschreitung: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// B1: Receive läuft auf dem UI-Thread und kehrt immer sofort zurück, auch bei hängendem Gateway.
func TestHostReceiveBlockiertNie(t *testing.T) {
	b := newHostBed(t, nil)
	within(t, 2*time.Second, "Receive", func() {
		for i := 0; i < 500; i++ {
			b.h.Receive(page, msg(fmt.Sprint("w", i), "werkzeug", `{"name":"x","arguments":{}}`))
		}
	})
	waitFor(t, "Gateway ausgelastet", func() bool { return b.g.active.Load() == 4 })
	if b.g.peak.Load() > 4 {
		t.Fatalf("Gateway-Grenze verletzt: %d gleichzeitig", b.g.peak.Load())
	}
	// Überzählige bekommen den festen Code, nie ein Hängen.
	waitFor(t, "zu_viele", func() bool {
		for _, r := range replies(t, b.h) {
			if string(r["fehler"]) == `"zu_viele"` {
				return true
			}
		}
		return false
	})
}

// B1: Anhalten geht bei hängendem Gateway weiter durch.
func TestHostAnhaltenBleibtBeiHaengendemGatewayBedienbar(t *testing.T) {
	b := newHostBed(t, nil)
	for i := 0; i < 20; i++ {
		b.h.Receive(page, msg(fmt.Sprint("w", i), "werkzeuge", `{}`))
	}
	waitFor(t, "Gateway ausgelastet", func() bool { return b.g.active.Load() == 4 })
	within(t, time.Second, "Receive(anhalten)", func() { b.h.Receive(page, msg("halt", "anhalten", `{}`)) })
	waitFor(t, "Anhalten ausgeführt", func() bool { return b.l.anhaltenN.Load() == 1 })
	waitFor(t, "Antwort auf anhalten", func() bool {
		for _, r := range replies(t, b.h) {
			if string(r["id"]) == `"halt"` && string(r["ok"]) == "true" {
				return true
			}
		}
		return false
	})
}

// B1: Auch ein hängender lokaler Pfad blockiert nur seine eigene Klasse.
func TestHostHaengenderLokalerPfadBlockiertNichtDasGateway(t *testing.T) {
	b := newHostBed(t, nil)
	b.l.hang = make(chan struct{})
	for i := 0; i < 10; i++ {
		b.h.Receive(page, msg(fmt.Sprint("s", i), "status", `{}`))
	}
	waitFor(t, "Lokal ausgelastet", func() bool { return b.l.statusN.Load() == 2 })
	close(b.g.release)
	b.h.Receive(page, msg("g", "werkzeuge", `{}`))
	waitFor(t, "Gateway antwortet", func() bool {
		for _, r := range replies(t, b.h) {
			if string(r["id"]) == `"g"` && string(r["ok"]) == "true" {
				return true
			}
		}
		return false
	})
	close(b.l.hang)
}

func TestHostWeckeBeiAntwort(t *testing.T) {
	b := newHostBed(t, nil)
	b.h.Receive(page, msg("s", "status", `{}`))
	waitFor(t, "Wecker", func() bool { return b.wakes.Load() >= 1 })
	if got := b.h.Drain(); len(got) != 1 || !strings.Contains(string(got[0]), `"zustand":"arbeitet"`) {
		t.Fatalf("Antworten: %q", got)
	}
	if b.h.Drain() != nil {
		t.Fatal("Drain leert nicht")
	}
}

func TestHostAntwortschlangeBegrenztUndBlockiertNie(t *testing.T) {
	b := newHostBed(t, func(c *HostConfig) { c.MaxLokal = 64 })
	within(t, 2*time.Second, "Receive", func() {
		for i := 0; i < 300; i++ {
			b.h.Receive(page, msg(fmt.Sprint("s", i), "status", `{}`))
		}
	})
	done := make(chan struct{})
	go func() { b.h.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Antwortgoroutinen hängen an einer vollen Schlange")
	}
	if n := len(b.h.Drain()); n == 0 || n > queueSize {
		t.Fatalf("%d Antworten, Grenze %d", n, queueSize)
	}
}

// B1: ProcessFailed bricht Laufendes ab und schaltet das Fenster ab; nichts davon berührt Tray oder Pipe.
func TestHostFailBrichtAbUndNimmtNichtsMehrAn(t *testing.T) {
	b := newHostBed(t, nil)
	for i := 0; i < 4; i++ {
		b.h.Receive(page, msg(fmt.Sprint("w", i), "werkzeuge", `{}`))
	}
	waitFor(t, "Gateway ausgelastet", func() bool { return b.g.active.Load() == 4 })
	within(t, time.Second, "Fail", b.h.Fail)
	waitFor(t, "laufende Aufrufe abgebrochen", func() bool { return b.g.active.Load() == 0 })
	if !b.h.Closed() || b.closed.Load() != 1 {
		t.Fatal("nicht geschlossen")
	}
	b.h.Fail()
	b.h.Close()
	if b.closed.Load() != 1 {
		t.Fatalf("OnClosed %d-mal", b.closed.Load())
	}
	before := b.l.statusN.Load()
	b.h.Receive(page, msg("s", "status", `{}`))
	b.h.Wait()
	if b.l.statusN.Load() != before || b.h.Drain() != nil {
		t.Fatal("nach Fail noch Arbeit oder Antwort")
	}
}

func TestHostUnerlaubteQuelleBleibtStumm(t *testing.T) {
	b := newHostBed(t, nil)
	for _, src := range []fenster.Source{{MainFrame: false, URL: page.URL}, {MainFrame: true, URL: "https://evil.example/"}, {}} {
		b.h.Receive(src, msg("a", "anhalten", `{}`))
	}
	b.h.Wait()
	if b.l.anhaltenN.Load() != 0 || b.h.Drain() != nil || b.g.calls.Load() != 0 {
		t.Fatal("unerlaubte Quelle erreichte die Brücke")
	}
}

func TestHostAntwortenOhneTraeger(t *testing.T) {
	g := &spiegelGateway{}
	b := newHostBed(t, func(c *HostConfig) { c.Gateway = g })
	b.h.Receive(page, msg("a", "werkzeuge", `{}`))
	b.h.Wait()
	got := b.h.Drain()
	if len(got) != 1 || strings.Contains(string(got[0]), "jnpt") || !strings.Contains(string(got[0]), "antwort_ungueltig") {
		t.Fatalf("%q", got)
	}
}

type spiegelGateway struct{}

func (spiegelGateway) Werkzeuge(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"echo":"` + "jn" + "ptpc_" + strings.Repeat("A", 30) + `"}`), nil
}
func (spiegelGateway) Werkzeug(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

func TestNewHostPrueftEingaben(t *testing.T) {
	g, l := newHangGateway(), &countLokal{}
	for _, cfg := range []HostConfig{
		{App: "", Gateway: g, Lokal: l},
		{App: "Demo", Gateway: g, Lokal: l},
		{App: "demo", Lokal: l},
		{App: "demo", Gateway: g},
	} {
		if _, err := NewHost(cfg); err == nil {
			t.Errorf("Konfiguration %+v akzeptiert", cfg.App)
		}
	}
}
