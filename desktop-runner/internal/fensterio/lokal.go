// Package fensterio hängt die reine Fenster-Policy (internal/fenster) an die
// vorhandenen Adapter: den HTTP-Adapter des Kerns (Gateway) und die lokale Pipe
// (Lokal). Es enthält keine Entscheidung, die nicht schon in fenster steht.
package fensterio

import (
	"context"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

// Exchange sendet genau einen Befehl über die bereits geprüfte Pipe des
// Bedienfelds und liefert den rohen Antwortrahmen. Eine zweite Pipe-Verbindung
// gibt es nicht: Der Läufer bedient je Benutzer nur ein Bedienfeld.
type Exchange func(ctx context.Context, cmd pipe.Message) ([]byte, error)

// NewLokal ist der Wrapper mit genau zwei Operationen. Er kann ausschließlich
// die Befehle "status" und "anhalten" senden; Pause, Fortsetzen, Setups und
// Antworten auf Rückfragen sind über ihn nicht erreichbar.
func NewLokal(ex Exchange) fenster.Lokal { return &lokal{exchange: ex} }

type lokal struct{ exchange Exchange }

func (l *lokal) Status(ctx context.Context) (fenster.Status, error) {
	m, err := l.ask(ctx, pipe.Message{Type: "status"})
	if err != nil {
		return fenster.Status{}, err
	}
	return fenster.Status{Zustand: m.Zustand, Bis: m.Bis, Netz: m.Netz}, nil
}

func (l *lokal) Anhalten(ctx context.Context) error {
	m, err := l.ask(ctx, pipe.Message{Type: "anhalten"})
	if err != nil {
		return err
	}
	if m.Zustand != "angehalten" {
		return fenster.ErrAbgelehnt
	}
	return nil
}

// ask liefert nur einen gültigen zustand-Rahmen; fehler und alles andere sind
// feste Codes, nie der Rahmentext.
func (l *lokal) ask(ctx context.Context, cmd pipe.Message) (pipe.Message, error) {
	raw, err := l.exchange(ctx, cmd)
	if err != nil {
		return pipe.Message{}, fenster.ErrNichtErreichbar
	}
	m, err := pipe.DecodeServer(raw)
	switch {
	case err != nil:
		return pipe.Message{}, fenster.ErrFehler
	case m.Type == "fehler":
		return pipe.Message{}, fenster.ErrAbgelehnt
	case m.Type != "zustand":
		return pipe.Message{}, fenster.ErrFehler
	}
	return m, nil
}
