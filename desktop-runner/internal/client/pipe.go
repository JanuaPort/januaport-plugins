package client

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	panel "github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

func (e *Engine) observePanelNetwork(err error) {
	if e.Container != nil {
		return
	}
	e.mu.Lock()
	e.panelOnline = err == nil
	e.mu.Unlock()
}

func (e *Engine) panelStatus() panel.Message {
	e.mu.Lock()
	e.haltedLocked(time.Now())
	b := e.bundle
	m := panel.Message{Type: "zustand", Zustand: e.halt.State, Bis: e.halt.Until, Netz: e.panelOnline, Setups: []panel.Setup{}}
	if e.haltFault {
		m.Zustand = "stoerung"
	}
	e.mu.Unlock()
	if e.runMu.TryLock() {
		e.runMu.Unlock()
	} else {
		m.Beschaeftigt = true
	}
	if e.admission.TryLock() {
		e.admission.Unlock()
	} else {
		m.Beschaeftigt = true
	}
	manifest, err := VerifyManifest(b.Manifest.Envelope, e.Credential.Pubkey, e.Credential.DeviceID, 0)
	if err != nil || manifest.Contract != 4 {
		m.Zustand = "stoerung"
		return m
	}
	m.Test = manifest.TestDevice
	ready := false
	if e.Session != nil {
		session, err := e.Session()
		ready = err == nil && startRefusal(session, b.Config) == nil
	}
	now := time.Now()
	if !m.Netz {
		var err error
		now, err = e.State.ObserveTime(now)
		if err != nil {
			m.FristAbgelaufen = true
		}
	}
	for _, p := range manifest.Pins {
		grace := manifest.GraceHours
		if p.GraceHours != nil {
			grace = *p.GraceHours
		}
		if !m.Netz && now.Sub(b.Manifest.LastSync) >= time.Duration(grace)*time.Hour {
			m.FristAbgelaufen = true
		}
		for _, s := range p.Setups {
			_, present := b.Environments[envKey(p)]
			m.Setups = append(m.Setups, panel.Setup{Pin: p.Pin, Setup: s.Name, Label: s.Label, BrauchtEingabe: len(s.Frei) > 0, Startbar: len(s.Frei) == 0 && m.Zustand == "arbeitet" && !m.Beschaeftigt && m.Netz && ready && present})
		}
	}
	// The manifest can contain more setups than the local 4 KiB protocol fits.
	// Fail closed with a fixed disturbance, never truncate silently or spill data.
	if _, err := json.Marshal(m); err != nil {
		return panel.Message{Type: "fehler", Code: "stoerung"}
	}
	return m
}

func (e *Engine) panelCommand(ctx context.Context, m panel.Message) panel.Message {
	var err error
	switch m.Type {
	case "status":
	case "pause":
		err = e.Pause(*m.Minuten)
	case "weiter", "fortsetzen":
		err = e.Halt("arbeitet", time.Time{})
	case "anhalten":
		err = e.Halt("angehalten", time.Time{})
		e.Questions.Cancel()
	case "setup_start":
		err = e.startPanelSetup(ctx, m.Pin, m.Setup)
	case "antwort":
		err = e.Questions.Answer(m.ID, *m.Wahl)
	default:
		err = panel.ErrProtocol
	}
	if err != nil {
		return panel.Message{Type: "fehler", Code: "abgewiesen"}
	}
	return e.panelStatus()
}

// HandlePanel is the runner-side adapter. Every raw frame is checked here;
// neither the UI nor a Python helper can supply authorization decisions.
func (e *Engine) HandlePanel(ctx context.Context, r io.Reader, w io.Writer) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if closer, ok := r.(io.Closer); ok {
		defer closer.Close()
	}
	events, detach, err := e.Questions.Attach()
	if err != nil {
		panel.WriteFrame(w, panel.Message{Type: "fehler", Code: "stoerung"})
		return
	}
	defer detach()
	type frame struct {
		raw []byte
		err error
	}
	frames := make(chan frame)
	go func() {
		for {
			raw, err := panel.ReadFrame(r)
			select {
			case frames <- frame{raw, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		var out panel.Message
		select {
		case <-ctx.Done():
			return
		case out = <-events:
		case <-tick.C:
			out = e.panelStatus()
		case f := <-frames:
			if f.err == io.EOF {
				return
			}
			m, err := panel.DecodeClient(f.raw)
			if f.err != nil || err != nil {
				panel.WriteFrame(w, panel.Message{Type: "fehler", Code: "protokoll"})
				return
			}
			out = e.panelCommand(ctx, m)
		}
		if panel.WriteFrame(w, out) != nil {
			return
		}
	}
}

// StartPanel tries exactly once. No random suffix, second listener, or process
// failure on name occupation. The fixed code is the local B2 signal.
func (e *Engine) StartPanel(ctx context.Context, listen func() (panel.Acceptor, error)) func() {
	if e.Container != nil {
		return func() {}
	}
	l, err := listen()
	if err != nil {
		e.logCode(LogPipeFault, 1)
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			c, err := l.Accept(ctx)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
			e.HandlePanel(context.WithValue(ctx, panelLifetimeKey{}, ctx), c, c)
			c.Close()
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); l.Close(); <-done; e.WaitPanelRuns() }) }
}

func (e *Engine) WaitPanelRuns() { e.panelRuns.Wait() }

// All gates are in Go, before HTTP. admission also covers scheduled/adhoc
// preflight; runMu covers direct Execute and preparation before active is set.
func (e *Engine) startPanelSetup(ctx context.Context, pin, setup string) error {
	if e.Container != nil || !e.admission.TryLock() {
		return ErrProtocol
	}
	if !e.runMu.TryLock() {
		e.admission.Unlock()
		return ErrProtocol
	}
	accepted := false
	defer func() {
		if !accepted {
			e.runMu.Unlock()
			e.admission.Unlock()
		}
	}()
	e.mu.Lock()
	blocked := e.haltedLocked(time.Now()) || !e.panelOnline
	b, version := e.bundle, e.haltVersion
	e.mu.Unlock()
	if blocked || e.API == nil {
		return ErrProtocol
	}
	m, err := VerifyManifest(b.Manifest.Envelope, e.Credential.Pubkey, e.Credential.DeviceID, 0)
	if err != nil || m.Contract != 4 {
		return ErrManifest
	}
	var chosen *Pin
	for _, p := range m.Pins {
		if p.Pin == pin {
			for _, s := range p.Setups {
				if s.Name == setup && len(s.Frei) == 0 {
					x := p
					chosen = &x
				}
			}
		}
	}
	if chosen == nil {
		return ErrManifest
	}
	env, ok := b.Environments[envKey(*chosen)]
	if !ok {
		return ErrRuntime
	}
	session, err := e.Session()
	if err != nil {
		return ErrAccount
	}
	if err = startRefusal(session, b.Config); err != nil {
		return err
	}
	// Last local authorization point before the request; reject a halt transition
	// even if it was immediately resumed. HTTP never holds e.mu (revocation).
	e.mu.Lock()
	blocked = e.haltedLocked(time.Now()) || version != e.haltVersion || !e.panelOnline
	if !blocked {
		e.panelPending = true
		e.panelGeneration++
		if e.claimCancel != nil {
			e.claimCancel()
		}
	}
	e.mu.Unlock()
	if blocked {
		return ErrProtocol
	}
	accepted = true
	e.panelRuns.Add(1)
	// A completed command survives panel disconnect, but not runner shutdown.
	// StartPanel supplies the runner lifetime separately from the session reader.
	runCtx := context.WithoutCancel(ctx)
	if lifetime, ok := ctx.Value(panelLifetimeKey{}).(context.Context); ok {
		runCtx = lifetime
	}
	go func() {
		defer e.panelRuns.Done()
		defer e.admission.Unlock()
		defer e.runMu.Unlock()
		defer func() { e.mu.Lock(); e.panelPending = false; e.mu.Unlock() }()
		requestCtx, cancel := context.WithTimeout(runCtx, 15*time.Second)
		reply, err := e.API.StartSetup(requestCtx, b.Manifest.Envelope, e.Credential, pin, setup, nil)
		cancel()
		if err != nil {
			e.observePanelNetwork(err)
			e.logCode(LogPipeFault, 2)
			return
		} // never queue an offline setup
		r := Result{RunKey: reply.RunKey, ManifestSeq: m.Seq, Commit: chosen.Commit}
		e.mu.Lock()
		blocked = e.haltedLocked(time.Now()) || version != e.haltVersion
		current := e.bundle.Manifest.Envelope
		e.mu.Unlock()
		// A changed/withdrawn manifest while HTTP was in flight cannot authorize spawn.
		if blocked || string(current.Manifest) != string(b.Manifest.Envelope.Manifest) || current.Sig != b.Manifest.Envelope.Sig {
			e.report(finish(r, "cancelled", ""))
			return
		}
		expires, err := time.Parse(time.RFC3339, reply.ExpiresAt)
		if err != nil || !time.Now().Before(expires) {
			e.report(finish(r, "internal", ""))
			return
		}
		permit := panelPermit{envelope: b.Manifest.Envelope, haltVersion: version}
		e.report(e.executeReserved(context.WithValue(runCtx, panelPermitKey{}, permit), r, env, reply.Input, reply.RunToken, env.Timeout, 10000))
	}()
	return nil
}

type panelLifetimeKey struct{}

type panelPermitKey struct{}
type panelPermit struct {
	envelope    SignedManifest
	haltVersion uint64
}

// Called in Execute's existing spawn critical section. A withdrawal or a
// halt/resume during package preparation must not reopen an admitted setup.
func (e *Engine) panelPermitValidLocked(ctx context.Context) bool {
	p, ok := ctx.Value(panelPermitKey{}).(panelPermit)
	if !ok {
		return true
	}
	return p.haltVersion == e.haltVersion && p.envelope.Sig == e.bundle.Manifest.Envelope.Sig && string(p.envelope.Manifest) == string(e.bundle.Manifest.Envelope.Manifest)
}
