package pipe

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

type pendingQuestion struct {
	id       string
	count    int
	answer   chan *int
	answered bool
	deadline time.Time
}

// Questions relays at most one question to one attached panel, entirely locally.
// L5 passes its run context: termination closes the question without a selection.
type Questions struct {
	mu           sync.Mutex
	events       chan Message
	disconnected chan struct{}
	pending      *pendingQuestion
}

func (h *Questions) Attach() (<-chan Message, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.events != nil {
		return nil, nil, ErrProtocol
	}
	events := make(chan Message, 8)
	gone := make(chan struct{})
	h.events = events
	h.disconnected = gone
	var once sync.Once
	detach := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.events == events {
				h.events = nil
				close(gone)
			}
		})
	}
	return events, detach, nil
}

func (h *Questions) Ask(ctx context.Context, q Message) (*int, error) {
	if q.Type != "frage" {
		return nil, ErrProtocol
	}
	if _, err := json.Marshal(q); err != nil {
		return nil, ErrProtocol
	}
	h.mu.Lock()
	if h.pending != nil {
		h.mu.Unlock()
		return nil, ErrProtocol
	}
	if h.events == nil {
		h.mu.Unlock()
		return nil, nil
	}
	p := &pendingQuestion{id: q.ID, count: len(q.Optionen), answer: make(chan *int, 1), deadline: time.Now().Add(time.Duration(q.FristS) * time.Second)}
	events, gone := h.events, h.disconnected
	// Reserve the matching removal event before publishing a dialog.
	if len(events) > cap(events)-2 {
		h.mu.Unlock()
		return nil, nil
	}
	select {
	case events <- q:
	default:
		h.mu.Unlock()
		return nil, nil
	}
	h.pending = p
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.pending = nil
		if h.events == events {
			select {
			case events <- Message{Type: "frage_weg", ID: q.ID}:
			default:
			}
		}
	}()
	timer := time.NewTimer(time.Duration(q.FristS) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, nil
	case <-gone:
		return nil, nil
	case <-timer.C:
		return nil, nil
	case v := <-p.answer:
		return v, nil
	}
}

func (h *Questions) Answer(id string, wahl int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pending == nil || h.pending.id != id || wahl < 0 || wahl >= h.pending.count || h.pending.answered || !time.Now().Before(h.pending.deadline) {
		return ErrProtocol
	}
	select {
	case h.pending.answer <- &wahl:
		h.pending.answered = true
		return nil
	default:
		return ErrProtocol
	}
}

// Cancel is also called by the pipe halt path; L5 must additionally use the
// execution context so all termination paths (revocation, timeout) are covered.
func (h *Questions) Cancel() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pending != nil {
		select {
		case h.pending.answer <- nil:
		default:
		}
	}
}
