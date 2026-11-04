package fensterio

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

type sendLog struct {
	mu   sync.Mutex
	cmds []string
	ok   bool
}

func (s *sendLog) send(m pipe.Message) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmds = append(s.cmds, m.Type)
	return s.ok
}
func (s *sendLog) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

func zustandFrame(t *testing.T, state string) []byte {
	t.Helper()
	b, err := json.Marshal(pipe.Message{Type: "zustand", Zustand: state, Netz: true})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPanelExchangeAntwortAufDenBefehl(t *testing.T) {
	s := &sendLog{ok: true}
	x := NewPanelExchange(s.send)
	want := zustandFrame(t, "pausiert")
	go func() {
		for len(s.sent()) == 0 {
			time.Sleep(time.Millisecond)
		}
		x.Deliver(want)
	}()
	got, err := x.Exchange(context.Background(), pipe.Message{Type: "status"})
	if err != nil || string(got) != string(want) {
		t.Fatalf("%q %v", got, err)
	}
	if sent := s.sent(); len(sent) != 1 || sent[0] != "status" {
		t.Fatal(sent)
	}
}

func TestPanelExchangeIgnoriertRahmenOhneWartenden(t *testing.T) {
	s := &sendLog{ok: true}
	x := NewPanelExchange(s.send)
	within(t, time.Second, "Deliver ohne Wartenden", func() {
		for i := 0; i < 100; i++ {
			x.Deliver(zustandFrame(t, "arbeitet"))
		}
	})
	// Ein davor gelieferter Rahmen beantwortet einen späteren Befehl nicht.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := x.Exchange(ctx, pipe.Message{Type: "status"}); err == nil {
		t.Fatal("alter Rahmen beantwortete den Befehl")
	}
}

func TestPanelExchangeNurZustandUndFehlerBeantworten(t *testing.T) {
	s := &sendLog{ok: true}
	x := NewPanelExchange(s.send)
	result := make(chan []byte, 1)
	go func() {
		got, _ := x.Exchange(context.Background(), pipe.Message{Type: "status"})
		result <- got
	}()
	for len(s.sent()) == 0 {
		time.Sleep(time.Millisecond)
	}
	// Rückfragen und Unbrauchbares sind nie die Antwort.
	frage, _ := json.Marshal(pipe.Message{Type: "frage", ID: "q1", Titel: "t", Text: "x", Optionen: []string{"a", "b"}, FristS: 5})
	weg, _ := json.Marshal(pipe.Message{Type: "frage_weg", ID: "q1"})
	x.Deliver(frage)
	x.Deliver(weg)
	x.Deliver([]byte(`nope`))
	select {
	case <-result:
		t.Fatal("falscher Rahmentyp beantwortete den Befehl")
	case <-time.After(30 * time.Millisecond):
	}
	fehler, _ := json.Marshal(pipe.Message{Type: "fehler", Code: "abgewiesen"})
	x.Deliver(fehler)
	select {
	case got := <-result:
		if string(got) != string(fehler) {
			t.Fatalf("%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("fehler-Rahmen kam nicht an")
	}
}

func TestPanelExchangeSendenFehlgeschlagen(t *testing.T) {
	s := &sendLog{ok: false}
	x := NewPanelExchange(s.send)
	within(t, time.Second, "Exchange ohne Verbindung", func() {
		if _, err := x.Exchange(context.Background(), pipe.Message{Type: "anhalten"}); err == nil {
			t.Error("Senden ohne Verbindung gilt als gesendet")
		}
	})
}

func TestPanelExchangeAbbruchUndFrist(t *testing.T) {
	s := &sendLog{ok: true}
	x := NewPanelExchange(s.send)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := x.Exchange(ctx, pipe.Message{Type: "status"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	// Der Platz ist wieder frei.
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	go func() {
		for len(s.sent()) < 2 {
			time.Sleep(time.Millisecond)
		}
		x.Deliver(zustandFrame(t, "arbeitet"))
	}()
	if _, err := x.Exchange(ctx2, pipe.Message{Type: "status"}); err != nil {
		t.Fatal(err)
	}
}

func TestPanelExchangeSerialisiertGleichzeitigeBefehle(t *testing.T) {
	s := &sendLog{ok: true}
	x := NewPanelExchange(s.send)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = x.Exchange(context.Background(), pipe.Message{Type: "status"})
		}()
	}
	for len(s.sent()) < 1 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(s.sent()); n != 1 {
		t.Fatalf("%d Befehle gleichzeitig unterwegs", n)
	}
	for n := 1; n <= 3; n++ {
		for len(s.sent()) < n {
			time.Sleep(time.Millisecond)
		}
		x.Deliver(zustandFrame(t, "arbeitet"))
	}
	wg.Wait()
}

func TestPanelExchangeVerbindungsverlustWeckt(t *testing.T) {
	s := &sendLog{ok: true}
	x := NewPanelExchange(s.send)
	errc := make(chan error, 1)
	go func() {
		_, err := x.Exchange(context.Background(), pipe.Message{Type: "status"})
		errc <- err
	}()
	for len(s.sent()) == 0 {
		time.Sleep(time.Millisecond)
	}
	within(t, time.Second, "Reset", x.Reset)
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("Verbindungsverlust ohne Fehler")
		}
	case <-time.After(time.Second):
		t.Fatal("Wartender nicht geweckt")
	}
}

// Zusammen mit dem Wrapper: Anhalten über die geteilte Verbindung.
func TestPanelExchangeMitLokalAnhalten(t *testing.T) {
	s := &sendLog{ok: true}
	x := NewPanelExchange(s.send)
	go func() {
		n := 0
		for {
			for len(s.sent()) <= n {
				time.Sleep(time.Millisecond)
			}
			n++
			state := "arbeitet"
			if n >= 2 {
				state = "angehalten"
			}
			x.Deliver(zustandFrame(t, state))
		}
	}()
	var l fenster.Lokal = NewLokal(x.Exchange)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := l.Anhalten(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range s.sent() {
		if c != "anhalten" && c != "status" {
			t.Fatalf("unerlaubter Befehl %q", c)
		}
	}
}
