package mediator

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// sandboxes hält HÖCHSTENS EINE Verbindung zum Wächter (Fassung 2, V1).
// Eine neue nimmt er erst an, nachdem er von der vorigen EOF gelesen hat;
// jede weitere schließt er sofort, und ein laufender Lauf endet dann als
// limit/sandbox_lost.
type sandboxes struct {
	mu      sync.Mutex
	current *sbConn
	idle    chan *sbConn
	onHello func(isolation string, seccomp int, profile string) bool
	log     *slog.Logger
}

func newSandboxes(onHello func(string, int, string) bool, log *slog.Logger) *sandboxes {
	return &sandboxes{idle: make(chan *sbConn, 1), onHello: onHello, log: log}
}

// sbConn ist eine Verbindung = eine Instanz = höchstens ein Lauf (M4).
type sbConn struct {
	c         *net.UnixConn
	isolation string
	eof       chan struct{}

	mu       sync.Mutex
	att      *attachment
	lost     chan struct{}
	lostOnce sync.Once
}

// attachment verbindet einen Lauf mit dem Lesestrom. Ist done geschlossen,
// verwirft der Leser jeden weiteren Rahmen.
type attachment struct {
	frames chan inFrame
	done   chan struct{}
}

type inFrame struct {
	payload []byte
	err     error
}

func (s *sandboxes) accept(ln *net.UnixListener) error {
	for {
		c, err := ln.AcceptUnix()
		if err != nil {
			return nil
		}
		s.mu.Lock()
		cur := s.current
		if cur == nil {
			sc := &sbConn{c: c, eof: make(chan struct{}), lost: make(chan struct{})}
			s.current = sc
			s.mu.Unlock()
			go s.serve(sc)
			continue
		}
		s.mu.Unlock()
		_ = c.Close()
		cur.markLost()
		s.log.Warn("zweite Sandbox-Verbindung geschlossen")
	}
}

var instanceRe = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

func (s *sandboxes) serve(sc *sbConn) {
	defer func() {
		close(sc.eof)
		s.mu.Lock()
		if s.current == sc {
			s.current = nil
		}
		s.mu.Unlock()
	}()
	if !s.handshake(sc) {
		_ = sc.c.CloseWrite()
		_, _ = io.Copy(io.Discard, sc.c)
		return
	}
	for {
		payload, err := frame.Read(sc.c, frame.MaxFromSandbox)
		sc.deliver(inFrame{payload: payload, err: err})
		if err != nil {
			// Nach einem zu großen Rahmen ist der Strom nicht mehr synchron:
			// bis EOF verwerfen. Erst dann ist die Instanz nachweislich leer.
			_, _ = io.Copy(io.Discard, sc.c)
			return
		}
	}
}

// handshake liest hello. Nur eine Instanz mit Seccomp 2 kommt in den Slot.
func (s *sandboxes) handshake(sc *sbConn) bool {
	_ = sc.c.SetReadDeadline(time.Now().Add(10 * time.Second))
	payload, err := frame.Read(sc.c, frame.MaxFromSandbox)
	_ = sc.c.SetReadDeadline(time.Time{})
	if err != nil {
		return false
	}
	var h frame.Hello
	if json.Unmarshal(payload, &h) != nil || h.Type != frame.TypeHello {
		return false
	}
	usable := s.onHello(h.Isolation, h.Seccomp, h.Profile)
	instance := h.Instance
	if !instanceRe.MatchString(instance) {
		instance = "unlesbar"
	}
	profile := h.Profile
	if profile != "jnpt" {
		profile = "other"
	}
	s.log.Info("sandbox registered", "instance", instance, "isolation", h.Isolation, "seccomp", h.Seccomp,
		"profile", profile, "usable", usable)
	if !usable {
		// Die Instanz bleibt verbunden, bekommt aber nie einen job (S2).
		return true
	}
	sc.isolation = h.Isolation
	select {
	case s.idle <- sc:
	default:
	}
	return true
}

// acquire wartet höchstens wait auf eine registrierte, unbenutzte Instanz.
func (s *sandboxes) acquire(done <-chan struct{}, wait time.Duration) (*sbConn, bool) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case sc := <-s.idle:
			select {
			case <-sc.eof:
				continue
			default:
				return sc, true
			}
		case <-timer.C:
			return nil, false
		case <-done:
			return nil, false
		}
	}
}

func (sc *sbConn) attach() *attachment {
	a := &attachment{frames: make(chan inFrame, 16), done: make(chan struct{})}
	sc.mu.Lock()
	sc.att = a
	sc.mu.Unlock()
	return a
}

// detach beendet den Lauf auf dieser Verbindung: alle weiteren Rahmen
// werden verworfen, der Wächter bekommt EOF und räumt ab.
func (sc *sbConn) detach(a *attachment) {
	sc.mu.Lock()
	sc.att = nil
	sc.mu.Unlock()
	close(a.done)
	_ = sc.c.CloseWrite()
}

func (sc *sbConn) deliver(f inFrame) {
	sc.mu.Lock()
	a := sc.att
	sc.mu.Unlock()
	if a == nil {
		return
	}
	select {
	case a.frames <- f:
	case <-a.done:
	}
}

// markLost beendet einen laufenden Lauf als limit/sandbox_lost. Ohne Lauf
// bleibt die Verbindung unberührt: dann lebt in der Instanz kein Skript.
func (sc *sbConn) markLost() {
	sc.mu.Lock()
	attached := sc.att != nil
	sc.mu.Unlock()
	if attached {
		sc.lostOnce.Do(func() { close(sc.lost) })
	}
}
