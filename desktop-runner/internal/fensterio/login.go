package fensterio

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/anmeldung"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
)

// LoginView läuft ausschließlich auf dem UI-Thread. Quiesce beendet das alte Dokument,
// bevor ClearBrowsingData startet; bloßes Verstecken ließe Skripte Daten nachschreiben.
type LoginView interface {
	Quiesce(func(bool)) error
	ClearBrowsingData(func(bool)) error
	Reload() error
	Account(name, message string, busy, signed bool)
	FailLogin()
}
type loginResult struct {
	person     client.PersonSession
	err        error
	generation uint64
}
type loginError string

func (e loginError) Error() string { return anmeldung.Message(string(e)) }

// Login trennt Netzwerk/Browserstart von nativen UI-Operationen. Nur Start/Pump/SignOut
// laufen auf dem UI-Thread, Close darf zusätzlich von Host.Close kommen.
type Login struct {
	gateway    *Gateway
	host       *Host
	run        func(context.Context) (client.PersonSession, error)
	wake       func()
	out        chan loginResult
	mu         sync.Mutex
	generation uint64
	cancel     context.CancelFunc
	closed     bool
	busy       bool
	signed     bool
	name       string
	message    string
}

func (l *Login) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "Login(***)") }

func NewLogin(g *Gateway, h *Host, run func(context.Context) (client.PersonSession, error), wake func()) *Login {
	return &Login{gateway: g, host: h, run: run, wake: wake, out: make(chan loginResult, 1)}
}
func (l *Login) Start() {
	l.mu.Lock()
	if l.closed || l.busy {
		l.mu.Unlock()
		return
	}
	l.busy = true
	l.generation++
	select {
	case <-l.out:
	default:
	}
	gen := l.generation
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	l.message = anmeldung.Message("wartet")
	l.mu.Unlock()
	go func() {
		p, e := l.run(ctx)
		cancel()
		l.mu.Lock()
		if !l.closed && gen == l.generation {
			select {
			case l.out <- loginResult{p, e, gen}:
			default:
			}
		}
		l.mu.Unlock()
		if l.wake != nil {
			l.wake()
		}
	}()
}
func (l *Login) current(gen uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.closed && gen == l.generation
}
func (l *Login) Pump(v LoginView) {
	if l.signed && l.gateway.personalExpired() {
		l.signed = false
		l.name = ""
		l.message = anmeldung.Message("zeitablauf")
	}
	select {
	case r := <-l.out:
		if !l.current(r.generation) {
			return
		}
		if r.err != nil {
			l.busy = false
			l.message = loginMessage(r.err)
		} else if r.person.Changed || !l.signed {
			l.switchPerson(v, r.person, false, r.generation)
		} else {
			l.gateway.install(r.person.Session)
			l.name = anmeldung.DisplayName(r.person.Name)
			l.signed = true
			l.busy = false
			l.message = anmeldung.Message("angemeldet")
		}
	default:
	}
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if !closed {
		v.Account(l.name, l.message, l.busy, l.signed)
	}
}
func loginMessage(e error) string {
	var le loginError
	var he *client.HTTPError
	if errors.As(e, &le) {
		return anmeldung.Message(string(le))
	}
	if errors.As(e, &he) {
		return anmeldung.Message(he.Class)
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return anmeldung.Message("zeitablauf")
	}
	return anmeldung.Message("")
}
func (l *Login) SignOut(v LoginView) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.generation++
	select {
	case <-l.out:
	default:
	}
	gen := l.generation
	if l.cancel != nil {
		l.cancel()
	}
	l.busy = true
	l.mu.Unlock()
	l.switchPerson(v, client.PersonSession{}, true, gen)
}
func (l *Login) switchPerson(v LoginView, p client.PersonSession, logout bool, gen uint64) {
	l.gateway.Forget()
	if l.host != nil {
		l.host.Suspend()
	}
	l.name = ""
	l.signed = false
	v.Account("", l.message, true, false)
	fail := func() {
		if !l.current(gen) {
			return
		}
		l.busy = false
		l.message = anmeldung.Message("")
		v.Account("", l.message, false, false)
		v.FailLogin()
	}
	finish := func(ok bool) {
		if !l.current(gen) {
			return
		}
		if !ok {
			fail()
			return
		}
		if !logout {
			l.gateway.install(p.Session)
			l.name = anmeldung.DisplayName(p.Name)
			l.signed = true
			l.message = anmeldung.Message("angemeldet")
		} else {
			l.message = anmeldung.Message("abgemeldet")
		}
		if l.host != nil {
			l.host.Resume()
		}
		if v.Reload() != nil {
			l.gateway.Forget()
			fail()
			return
		}
		l.busy = false
		v.Account(l.name, l.message, false, l.signed)
	}
	if e := v.Quiesce(func(ok bool) {
		if !l.current(gen) {
			return
		}
		if !ok {
			fail()
			return
		}
		if e := v.ClearBrowsingData(finish); e != nil {
			fail()
		}
	}); e != nil {
		fail()
	}
}
func (l *Login) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	l.generation++
	if l.cancel != nil {
		l.cancel()
	}
	l.mu.Unlock()
	l.gateway.Forget()
}
