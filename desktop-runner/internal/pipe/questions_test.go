package pipe

import (
	"context"
	"testing"
	"time"
)

func TestFrageNurLokal(t *testing.T) {
	var h Questions
	q := Message{Type: "frage", ID: "q1", Text: "Fortfahren?", Optionen: []string{"Ja", "Nein"}, FristS: 5}
	if got, err := h.Ask(context.Background(), q); err != nil || got != nil {
		t.Fatal("ohne Bedienfeld", got, err)
	}
	events, detach, err := h.Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer detach()
	if _, _, err = h.Attach(); err == nil {
		t.Fatal("zweites Bedienfeld")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *int, 1)
	go func() { v, _ := h.Ask(ctx, q); done <- v }()
	select {
	case m := <-events:
		if m.Type != "frage" {
			t.Fatal(m)
		}
	case <-time.After(time.Second):
		t.Fatal("Frage fehlt")
	}
	if _, err = h.Ask(ctx, q); err == nil {
		t.Fatal("zweite offene Frage")
	}
	if h.Answer("fremd", 0) == nil || h.Answer("q1", 2) == nil {
		t.Fatal("falsche Antwort")
	}
	if err = h.Answer("q1", 1); err != nil {
		t.Fatal(err)
	}
	if v := <-done; v == nil || *v != 1 {
		t.Fatal(v)
	}
	if m := <-events; m.Type != "frage_weg" || m.ID != "q1" {
		t.Fatal(m)
	}
	if h.Answer("q1", 0) == nil {
		t.Fatal("Antwort nach Ende")
	}
	go func() { v, _ := h.Ask(ctx, q); done <- v }()
	<-events
	cancel()
	if v := <-done; v != nil {
		t.Fatal("Abbruch liefert Antwort")
	}
	<-events
	q.FristS = 0
	if _, err = h.Ask(context.Background(), q); err == nil {
		t.Fatal("Frist ungeprüft")
	}
}

func TestFrageFristUndDisconnect(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		var h Questions
		events, detach, _ := h.Attach()
		q := Message{Type: "frage", ID: "q1", Text: "Frage", Optionen: []string{"a", "b"}, FristS: 5}
		done := make(chan *int, 1)
		go func() { v, _ := h.Ask(context.Background(), q); done <- v }()
		<-events
		if disconnect {
			detach()
		}
		select {
		case v := <-done:
			if v != nil {
				t.Fatal("Frist/Disconnect liefert Wahl")
			}
		case <-time.After(6 * time.Second):
			t.Fatal("Frage hängt")
		}
		detach()
	}
}

func TestFrageAntwortNachFristUndDoppelt(t *testing.T) {
	var h Questions
	events, detach, _ := h.Attach()
	defer detach()
	q := Message{Type: "frage", ID: "q1", Text: "Frage", Optionen: []string{"a", "b"}, FristS: 5}
	done := make(chan struct{})
	go func() { h.Ask(context.Background(), q); close(done) }()
	<-events
	// Force expiration while Ask still owns the pending question; do not rely
	// on scheduling the timer goroutine to reject a late response.
	h.mu.Lock()
	h.pending.deadline = time.Now().Add(-time.Second)
	h.mu.Unlock()
	if h.Answer("q1", 0) == nil {
		t.Fatal("abgelaufene Antwort")
	}
	h.mu.Lock()
	h.pending.deadline = time.Now().Add(time.Hour)
	h.mu.Unlock()
	if h.Answer("q1", 0) != nil {
		t.Fatal("gültige Antwort")
	}
	if h.Answer("q1", 1) == nil {
		t.Fatal("zweite Antwort")
	}
	<-done
}

func TestFrageQueueReserviertEntfernung(t *testing.T) {
	var h Questions
	events, detach, _ := h.Attach()
	defer detach()
	h.mu.Lock()
	for i := 0; i < cap(h.events)-1; i++ {
		h.events <- Message{Type: "frage_weg", ID: "old"}
	}
	h.mu.Unlock()
	q := Message{Type: "frage", ID: "q1", Text: "Frage", Optionen: []string{"a", "b"}, FristS: 5}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if answer, err := h.Ask(ctx, q); err != nil || answer != nil {
		t.Fatal(answer, err)
	}
	if len(events) != 7 {
		t.Fatal("Frage ohne Platz für Entfernung publiziert")
	}
}
