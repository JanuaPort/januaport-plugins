// Package panelio is the transport adapter. All display and command decisions
// stay in panel; production supplies only pipe.Dial as the verified transport.
package panelio

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/panel"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

type Event struct {
	Generation uint64
	State      panel.ConnectionState
	Raw        []byte
}
type Request struct {
	Generation uint64
	Commands   []pipe.Message
	Reconnect  bool
}

// emit delivers ordered events; the Windows adapter wakes its UI with PostMessage.
// Generations prevent clicks queued for an old server from reaching a new one.
func Run(ctx context.Context, dial func() (io.ReadWriteCloser, error), requests <-chan Request, emit func(Event) bool) {
	var generation uint64
	attempt := 0
	for ctx.Err() == nil {
		generation++
		c, err := dial()
		if err != nil {
			if c != nil {
				c.Close()
			}
			if !emit(Event{Generation: generation, State: panel.VerificationFailed}) {
				return
			}
		} else {
			if !emit(Event{Generation: generation, State: panel.Connected}) {
				c.Close()
				return
			}
			session(ctx, c, generation, requests, emit)
			if ctx.Err() != nil {
				return
			}
			if !emit(Event{Generation: generation, State: panel.Disconnected}) {
				return
			}
		}
		// Never a tight loop, including servers that accept and immediately drop.
		timer := time.NewTimer(panel.RetryDelay(attempt))
		attempt++
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func session(ctx context.Context, c io.ReadWriteCloser, generation uint64, requests <-chan Request, emit func(Event) bool) {
	sessionCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); c.Close(); wg.Wait() }()
	raw := make(chan []byte)
	failed := make(chan struct{}, 2)
	write := make(chan []pipe.Message)
	written := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			b, err := pipe.ReadFrame(c)
			if err != nil {
				failed <- struct{}{}
				return
			}
			select {
			case raw <- b:
			case <-sessionCtx.Done():
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case batch := <-write:
				for _, m := range batch {
					if pipe.WriteFrame(c, m) != nil {
						failed <- struct{}{}
						return
					}
				}
				select {
				case written <- struct{}{}:
				case <-sessionCtx.Done():
					return
				}
			case <-sessionCtx.Done():
				return
			}
		}
	}()
	// The runner sends a status every second. A silent/blocking peer must not
	// leave a stale healthy icon indefinitely, or pin a write goroutine forever.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	writeTimer := time.NewTimer(5 * time.Second)
	writeTimer.Stop()
	defer writeTimer.Stop()
	var writeDeadline <-chan time.Time
	for {
		input := requests
		if writeDeadline != nil {
			input = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-failed:
			return
		case <-timer.C:
			return
		case <-writeDeadline:
			return
		case <-written:
			writeTimer.Stop()
			writeDeadline = nil
		case b := <-raw:
			timer.Reset(5 * time.Second)
			if !emit(Event{Generation: generation, Raw: b}) {
				return
			}
		case r := <-input:
			if r.Generation != generation {
				continue
			}
			if r.Reconnect {
				return
			}
			select {
			case write <- r.Commands:
				writeTimer.Reset(5 * time.Second)
				writeDeadline = writeTimer.C
			case <-ctx.Done():
				return
			case <-failed:
				return
			case <-timer.C:
				return
			}
		}
	}
}
