package panelio

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/panel"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

type rejected struct{ writes atomic.Int32 }

func (*rejected) Read([]byte) (int, error)      { return 0, io.EOF }
func (r *rejected) Write(b []byte) (int, error) { r.writes.Add(1); return len(b), nil }
func (*rejected) Close() error                  { return nil }

func TestServerpruefungSendetNichtsAmAdapter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &rejected{}
	requests := make(chan Request, 8)
	for _, v := range []string{"status", "anhalten", "weiter"} {
		requests <- Request{Generation: 1, Commands: []pipe.Message{{Type: v}}}
	}
	done := make(chan struct{})
	var calls atomic.Int32
	go func() {
		Run(ctx, func() (io.ReadWriteCloser, error) { calls.Add(1); return r, pipe.ErrDisturbance }, requests, func(e Event) bool {
			if e.State != panel.VerificationFailed {
				t.Error(e)
			}
			return true
		})
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Shutdown hängt")
	}
	if r.writes.Load() != 0 || calls.Load() != 1 {
		t.Fatal("ungeprüfte Pipe beschrieben oder ohne Abstand erneut gewählt", r.writes.Load(), calls.Load())
	}
}

type blockingConnection struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (c blockingConnection) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c blockingConnection) Write(b []byte) (int, error) { return c.w.Write(b) }
func (c blockingConnection) Close() error                { c.r.Close(); return c.w.Close() }

func TestVerbindungVerwirftAlteBefehleUndBrichtSchreibenAb(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()
	defer w1.Close()
	defer r2.Close()
	requests := make(chan Request, 4)
	events := make(chan Event, 4)
	done := make(chan struct{})
	go func() {
		Run(ctx, func() (io.ReadWriteCloser, error) { return blockingConnection{r1, w2}, nil }, requests, func(e Event) bool { events <- e; return true })
		close(done)
	}()
	e := <-events
	if e.State != panel.Connected {
		t.Fatal(e)
	}
	requests <- Request{Generation: e.Generation + 1, Commands: []pipe.Message{{Type: "anhalten"}}}
	requests <- Request{Generation: e.Generation, Commands: []pipe.Message{{Type: "status"}}}
	raw, err := pipe.ReadFrame(r2)
	if err != nil || string(raw) != `{"type":"status"}` {
		t.Fatal(string(raw), err)
	}
	requests <- Request{Generation: e.Generation, Commands: []pipe.Message{{Type: "status"}}}
	// No reader for this write. Cancel must close the actual connection and
	// release both I/O goroutines, without waiting for the peer to cooperate.
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blockiertes Schreiben hält UI/Shutdown")
	}
}

func TestBlockiertesSchreibenTrotzHeartbeatWirdGetrennt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()
	defer w1.Close()
	defer r2.Close()
	requests := make(chan Request, 1)
	events := make(chan Event, 32)
	done := make(chan struct{})
	go func() {
		Run(ctx, func() (io.ReadWriteCloser, error) { return blockingConnection{r1, w2}, nil }, requests, func(e Event) bool { events <- e; return true })
		close(done)
	}()
	e := <-events
	requests <- Request{Generation: e.Generation, Commands: []pipe.Message{{Type: "status"}}}
	go func() {
		for {
			if pipe.WriteFrame(w1, pipe.Message{Type: "zustand", Zustand: "arbeitet"}) != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}()
	deadline := time.After(6 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Raw == nil && e.State == panel.Disconnected {
				cancel()
				<-done
				return
			}
		case <-deadline:
			cancel()
			<-done
			t.Fatal("Schreiben ohne Grenze trotz Heartbeat")
		}
	}
}
