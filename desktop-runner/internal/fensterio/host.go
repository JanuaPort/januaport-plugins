package fensterio

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

const (
	queueSize   = 64              // wartende Antworten an die Seite; mehr wird verworfen, nie blockiert
	maxInflight = 128             // gleichzeitig laufende Nachrichten; mehr bleibt unbeantwortet
	localFrist  = 5 * time.Second // der lokale Pfad ist ein Gespräch mit dem Läufer, kein Netz
)

// HostConfig fasst zusammen, was ein Fenster braucht. Alles Netz liegt hinter Gateway und Lokal.
type HostConfig struct {
	App     string
	Gateway fenster.Gateway
	Lokal   fenster.Lokal
	// Wake weckt den UI-Thread, wenn Antworten bereitliegen. Es darf aus jeder Goroutine kommen und blockiert nie.
	Wake func()
	// OnClosed läuft genau einmal bei Close oder Fail.
	OnClosed func()
	// Frist je Befehl (Standard 60 s); MaxGateway/MaxLokal begrenzen die gleichzeitigen Aufrufe je Klasse.
	Frist      time.Duration
	MaxGateway int
	MaxLokal   int
}

// Host trennt den UI-Thread von allem, was blockieren kann (SEC K6b-2 B1). Receive und Drain sind für den
// UI-Thread: Receive startet nur eine Goroutine, Drain nimmt fertige Antworten ab. Gateway und Lokal haben
// getrennte Plätze: Ein hängendes Gateway hält Status und Anhalten nie auf.
type Host struct {
	app      string
	bridge   *fenster.Bruecke
	wake     func()
	onClosed func()

	mu        sync.Mutex
	out       [][]byte
	closed    bool
	suspended bool
	epoch     uint64
	cfg       HostConfig

	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	inflight atomic.Int32
	once     sync.Once
}

// NewHost verlangt eine gültige App und beide Gegenstellen.
func NewHost(cfg HostConfig) (*Host, error) {
	if !fenster.ValidAppName(cfg.App) || cfg.Gateway == nil || cfg.Lokal == nil {
		return nil, fenster.ErrArgs
	}
	if cfg.Frist <= 0 {
		cfg.Frist = 60 * time.Second
	}
	if cfg.MaxGateway <= 0 {
		cfg.MaxGateway = 8
	}
	if cfg.MaxLokal <= 0 {
		cfg.MaxLokal = 4
	}
	h := &Host{cfg: cfg, app: cfg.App, wake: cfg.Wake, onClosed: cfg.OnClosed}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	b, err := fenster.NewBruecke(cfg.App, &limitedGateway{inner: cfg.Gateway, sem: make(chan struct{}, cfg.MaxGateway)}, &limitedLokal{inner: cfg.Lokal, sem: make(chan struct{}, cfg.MaxLokal)}, hostView{h: h, epoch: h.epoch})
	if err != nil {
		return nil, err
	}
	b.Frist = cfg.Frist
	h.bridge = b
	return h, nil
}

// Receive nimmt eine Seitennachricht an. Es kehrt immer sofort zurück (UI-Thread).
func (h *Host) Receive(src fenster.Source, raw string) {
	h.mu.Lock()
	if h.closed || h.suspended || !fenster.SourceAllowed(h.app, src) {
		h.mu.Unlock()
		return
	}
	if h.inflight.Add(1) > maxInflight {
		h.inflight.Add(-1)
		h.mu.Unlock()
		return
	}
	bridge, ctx := h.bridge, h.ctx
	h.wg.Add(1)
	h.mu.Unlock()
	go func() { defer h.wg.Done(); defer h.inflight.Add(-1); bridge.Empfangen(ctx, src, []byte(raw)) }()
}

// Suspend verwirft alte Antworten auch dann, wenn ein Adapter den Abbruch ignoriert.
func (h *Host) Suspend() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.suspended = true
	h.epoch++
	h.out = nil
	h.cancel()
}
func (h *Host) Resume() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	b, _ := fenster.NewBruecke(h.app, &limitedGateway{inner: h.cfg.Gateway, sem: make(chan struct{}, h.cfg.MaxGateway)}, &limitedLokal{inner: h.cfg.Lokal, sem: make(chan struct{}, h.cfg.MaxLokal)}, hostView{h: h, epoch: h.epoch})
	b.Frist = h.cfg.Frist
	h.bridge = b
	h.suspended = false
}

// Drain übergibt die bereitliegenden Antworten (UI-Thread) und leert die Warteschlange.
func (h *Host) Drain() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.out) == 0 {
		return nil
	}
	out := h.out
	h.out = nil
	return out
}

// Close beendet das Fenster geordnet; Fail beim Absturz der WebView. Beides bricht laufende Aufrufe ab,
// nimmt nichts mehr an und berührt weder Tray noch Pipe.
func (h *Host) Close() { h.shutdown() }
func (h *Host) Fail()  { h.shutdown() }

func (h *Host) shutdown() {
	h.once.Do(func() {
		h.mu.Lock()
		h.closed = true
		h.out = nil
		h.mu.Unlock()
		h.cancel()
		if h.onClosed != nil {
			h.onClosed()
		}
	})
}

func (h *Host) Closed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// Wait wartet auf alle laufenden Nachrichten (Aufräumen und Tests).
func (h *Host) Wait() { h.wg.Wait() }

type hostView struct {
	h     *Host
	epoch uint64
}

func (v hostView) Senden(b []byte) error {
	h := v.h
	h.mu.Lock()
	if h.closed || h.suspended || v.epoch != h.epoch || len(h.out) >= queueSize {
		h.mu.Unlock()
		return fenster.ErrZuViele
	}
	h.out = append(h.out, append([]byte(nil), b...))
	h.mu.Unlock()
	if h.wake != nil {
		h.wake()
	}
	return nil
}

// limitedGateway hat eine feste Zahl Plätze und wartet nie auf einen: voll heißt „zu viele“.
type limitedGateway struct {
	inner fenster.Gateway
	sem   chan struct{}
}

func (g *limitedGateway) take() bool {
	select {
	case g.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *limitedGateway) Werkzeuge(ctx context.Context) (json.RawMessage, error) {
	if !g.take() {
		return nil, fenster.ErrZuViele
	}
	defer func() { <-g.sem }()
	return g.inner.Werkzeuge(ctx)
}

func (g *limitedGateway) Werkzeug(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	if !g.take() {
		return nil, fenster.ErrZuViele
	}
	defer func() { <-g.sem }()
	return g.inner.Werkzeug(ctx, name, arguments)
}

type limitedLokal struct {
	inner fenster.Lokal
	sem   chan struct{}
}

func (l *limitedLokal) take() bool {
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *limitedLokal) Status(ctx context.Context) (fenster.Status, error) {
	if !l.take() {
		return fenster.Status{}, fenster.ErrZuViele
	}
	defer func() { <-l.sem }()
	ctx, cancel := context.WithTimeout(ctx, localFrist)
	defer cancel()
	return l.inner.Status(ctx)
}

func (l *limitedLokal) Anhalten(ctx context.Context) error {
	if !l.take() {
		return fenster.ErrZuViele
	}
	defer func() { <-l.sem }()
	ctx, cancel := context.WithTimeout(ctx, localFrist)
	defer cancel()
	return l.inner.Anhalten(ctx)
}
