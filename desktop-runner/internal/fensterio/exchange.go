package fensterio

import (
	"context"
	"errors"
	"sync"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

var errKeineVerbindung = errors.New("pipe_nicht_erreichbar")

// PanelExchange ist der Exchange über die vorhandene Bedienfeld-Verbindung (panelio): Es gibt keine zweite
// Pipe-Verbindung. Der Aufrufer reicht Befehle über send in die Anfragen des Bedienfelds; die UI-Seite gibt
// jeden eingehenden Rahmen an Deliver weiter. Ein Befehl ist unterwegs, der nächste wartet auf den Platz.
type PanelExchange struct {
	send func(pipe.Message) bool
	slot chan struct{}

	mu     sync.Mutex
	waiter *waiter
}

type waiter struct {
	frames chan []byte
	gone   chan struct{}
	once   sync.Once
}

func (w *waiter) lost() { w.once.Do(func() { close(w.gone) }) }

// NewPanelExchange: send stellt den Befehl in die Warteschlange des Bedienfelds und meldet, ob das ging.
func NewPanelExchange(send func(pipe.Message) bool) *PanelExchange {
	return &PanelExchange{send: send, slot: make(chan struct{}, 1)}
}

// Exchange sendet genau einen Befehl und liefert den nächsten zustand- oder fehler-Rahmen danach.
func (x *PanelExchange) Exchange(ctx context.Context, cmd pipe.Message) ([]byte, error) {
	select {
	case x.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-x.slot }()
	w := &waiter{frames: make(chan []byte, 1), gone: make(chan struct{})}
	x.mu.Lock()
	x.waiter = w
	x.mu.Unlock()
	defer func() {
		x.mu.Lock()
		if x.waiter == w {
			x.waiter = nil
		}
		x.mu.Unlock()
	}()
	// Der Wartende steht vor dem Senden bereit: Die Antwort kann nicht vor ihm eintreffen.
	if !x.send(cmd) {
		return nil, errKeineVerbindung
	}
	select {
	case raw := <-w.frames:
		return raw, nil
	case <-w.gone:
		return nil, errKeineVerbindung
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Deliver wird auf dem UI-Thread für jeden rohen Rahmen aufgerufen und blockiert nie. Nur zustand und fehler
// beantworten einen Befehl; Rückfragen und Unbrauchbares werden ignoriert.
func (x *PanelExchange) Deliver(raw []byte) {
	x.mu.Lock()
	w := x.waiter
	x.mu.Unlock()
	if w == nil {
		return
	}
	m, err := pipe.DecodeServer(raw)
	if err != nil || (m.Type != "zustand" && m.Type != "fehler") {
		return
	}
	select {
	case w.frames <- append([]byte(nil), raw...):
	default:
	}
}

// Reset weckt einen Wartenden, wenn die Verbindung abbricht.
func (x *PanelExchange) Reset() {
	x.mu.Lock()
	w := x.waiter
	x.waiter = nil
	x.mu.Unlock()
	if w != nil {
		w.lost()
	}
}

// Func liefert Exchange als Wert, damit der UI-Thread den Wrapper verdrahten kann, ohne das Wort Exchange zu rufen.
func (x *PanelExchange) Func() Exchange { return x.Exchange }
