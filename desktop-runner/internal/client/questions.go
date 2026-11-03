package client

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

// H4: contract 4 Pin has no label field. Use its runner-bound manifest pin name,
// bounded to the title's 40-character wire limit; never take text from stdout.
func questionTitle(pin Pin) string {
	r := []rune(pin.Pin)
	return string(r[:min(len(r), 40)])
}

type runQuestions struct {
	relay   *pipe.Questions
	title   string
	mu      sync.Mutex
	pending bool
	wg      sync.WaitGroup
}

func newRunQuestions(relay *pipe.Questions, pin Pin) *runQuestions {
	return &runQuestions{relay: relay, title: questionTitle(pin)}
}

type scriptAnswer struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Wahl *int   `json:"wahl"`
}

func (q *runQuestions) start(ctx context.Context, raw []byte, write func(any) error) error {
	// Reuse the closed pipe schema without decoding into an open map. Adding
	// the trusted field before decoding preserves duplicate-field rejection,
	// including any script-supplied titel. The Python helper is not a boundary.
	b := bytes.TrimSpace(raw)
	if len(b) < 2 || b[0] != '{' || b[len(b)-1] != '}' {
		return ErrProtocol
	}
	title, _ := json.Marshal(q.title)
	b = append(append(append([]byte{}, b[:len(b)-1]...), []byte(`,"titel":`)...), title...)
	b = append(b, '}')
	m, err := pipe.DecodeServer(b)
	if err != nil || m.Type != "frage" {
		return ErrProtocol
	}
	q.mu.Lock()
	if q.pending {
		q.mu.Unlock()
		return ErrProtocol
	}
	q.pending = true
	q.wg.Add(1)
	q.mu.Unlock()
	go func() {
		defer q.wg.Done()
		defer func() { q.mu.Lock(); q.pending = false; q.mu.Unlock() }()
		choice, err := q.relay.Ask(ctx, m)
		if err != nil {
			write(pipe.Message{Type: "fehler", Code: "abgewiesen"})
			return
		}
		if ctx.Err() == nil {
			write(scriptAnswer{"antwort", m.ID, choice})
		}
	}()
	return nil
}
func (q *runQuestions) wait() { q.wg.Wait() }
