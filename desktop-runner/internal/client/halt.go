package client

import (
	"context"
	"os"
	"time"
)

// haltState uses State.Save/Load: atomic replacement and the existing cipher
// (DPAPI CurrentUser on Windows). Fault is local, never a new wire state.
type haltState struct {
	State string `json:"state"`
	Until string `json:"until,omitempty"`
}

func (h haltState) valid() bool {
	return (h.State == "arbeitet" || h.State == "pausiert" || h.State == "angehalten") &&
		(h.Until == "" || h.State == "pausiert" && utc(h.Until))
}

// loadHaltLocked precedes every entry point, including Run's first tick.
// L2-2: only a missing file means normal operation; all other errors stop.
func (e *Engine) loadHaltLocked(now time.Time) {
	if e.haltLoaded || e.Container != nil {
		return
	}
	e.haltLoaded = true
	err := e.State.Load("halt", &e.halt)
	if os.IsNotExist(err) {
		e.halt = haltState{State: "arbeitet"}
		return
	}
	e.haltThrough = now.UTC().Truncate(time.Minute)
	e.haltVersion++ // replay the latest durable state after every restart
	e.haltPending = true
	if err != nil || !e.halt.valid() {
		e.haltFaultLocked()
	}
}

func (e *Engine) haltFaultLocked() {
	e.halt = haltState{State: "angehalten"}
	if !e.haltFault {
		e.logCode(LogHaltFault, 1)
	}
	e.haltFault = true
	e.stopForHaltLocked()
}

// L2-1: state change and Kill share exactly Execute's spawn mutex.
// Cancellation first ensures an EOF from TerminateJobObject ends cancelled.
func (e *Engine) stopForHaltLocked() {
	if e.claimCancel != nil {
		e.claimCancel()
	}
	if e.halt.State == "angehalten" {
		if e.activeCancel != nil {
			e.activeCancel()
		}
		if e.active != nil {
			_ = e.active.Kill()
		}
	}
}

func (e *Engine) setHaltLocked(h haltState, now time.Time) error {
	e.halt = h
	e.haltThrough = now.UTC().Truncate(time.Minute)
	e.haltVersion++
	e.haltPending = true
	e.stopForHaltLocked() // local stop does not wait for disk or network
	if err := e.State.Save("halt", h); err != nil {
		e.haltFaultLocked()
		return err
	}
	e.haltFault = false
	return nil
}

func (e *Engine) haltedLocked(now time.Time) bool {
	if e.Container != nil {
		return false
	}
	e.loadHaltLocked(now)
	if e.halt.State == "pausiert" && e.halt.Until != "" {
		until, _ := time.Parse(time.RFC3339Nano, e.halt.Until)
		if !now.Before(until) {
			_ = e.setHaltLocked(haltState{State: "arbeitet"}, now)
		}
	}
	return e.halt.State != "arbeitet"
}

// Halt is an internal Windows control interface. It never schedules execution.
// Resume only removes this restriction; account, manifest and K8 still apply.
func (e *Engine) Halt(state string, until time.Time) error {
	h := haltState{State: state}
	if !until.IsZero() {
		h.Until = until.UTC().Format(time.RFC3339Nano)
	}
	if !h.valid() || e.Container != nil {
		return ErrProtocol
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	e.loadHaltLocked(now)
	if state == "pausiert" && e.halt.State == "angehalten" {
		return ErrProtocol
	}
	if h == e.halt && !e.haltFault {
		return nil
	}
	return e.setHaltLocked(h, now)
}

// Pause accepts the closed set used by the later local control interface.
func (e *Engine) Pause(minutes int) error {
	if minutes != 0 && minutes != 15 && minutes != 60 {
		return ErrProtocol
	}
	var until time.Time
	if minutes != 0 {
		until = time.Now().UTC().Add(time.Duration(minutes) * time.Minute)
	}
	return e.Halt("pausiert", until)
}

// HaltStatus preserves the local fault until a durable explicit transition.
func (e *Engine) HaltStatus() (state string, until time.Time, fault bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Container != nil {
		return "arbeitet", time.Time{}, false
	}
	e.haltedLocked(time.Now())
	if e.halt.Until != "" {
		until, _ = time.Parse(time.RFC3339Nano, e.halt.Until)
	}
	return e.halt.State, until, e.haltFault
}

func (e *Engine) scheduleHalted(slot time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.scheduleHaltedLocked(slot)
}

func (e *Engine) scheduleHaltedLocked(slot time.Time) bool {
	if e.Container != nil {
		return false
	}
	return e.haltedLocked(time.Now()) || !slot.After(e.haltThrough)
}

// Admission and slot/marker publication are one operation with respect to Halt.
// The early scheduled check alone cannot protect a concurrent reservation.
func (e *Engine) reserveScheduled(schedule string, r Result, slot time.Time) (bool, error) {
	if e.Container != nil {
		return e.State.ReserveRun(schedule, r)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.scheduleHaltedLocked(slot) {
		return false, nil
	}
	return e.State.ReserveRun(schedule, r)
}

func (e *Engine) workHalted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.haltedLocked(time.Now())
}

// A halt also cancels an already outstanding long poll. A response crossing
// any transition is discarded, including a rapid halt/resume sequence.
func (e *Engine) claim(ctx context.Context) (*Job, error) {
	if e.Container != nil {
		return e.API.Claim(ctx)
	}
	e.mu.Lock()
	if e.haltedLocked(time.Now()) {
		e.mu.Unlock()
		return nil, nil
	}
	pollCtx, cancel := context.WithCancel(ctx)
	e.claimCancel = cancel
	version := e.haltVersion
	e.mu.Unlock()
	j, err := e.API.Claim(pollCtx)
	cancel()
	e.mu.Lock()
	e.claimCancel = nil
	blocked := e.haltedLocked(time.Now()) || version != e.haltVersion
	e.mu.Unlock()
	if blocked {
		return nil, nil
	}
	return j, err
}

// At most one attempt per ten seconds, including failures. One pending value
// coalesces all changes; an in-flight acknowledgement cannot clear a newer one.
// The sender never holds e.mu over HTTP (revocation calls back into Engine).
func (e *Engine) deliverHalt(ctx context.Context, now time.Time) {
	e.haltSendMu.Lock()
	defer e.haltSendMu.Unlock()
	e.mu.Lock()
	if e.Container != nil {
		e.mu.Unlock()
		return
	}
	e.haltedLocked(now)
	if !e.haltPending || e.API == nil || (!e.haltAttempt.IsZero() && now.Sub(e.haltAttempt) < 10*time.Second) {
		e.mu.Unlock()
		return
	}
	h, version := e.halt, e.haltVersion
	// Preserve the throttle over process restarts too. A failed timestamp write
	// cannot authorize an uncounted request. Clock rollback only delays delivery.
	var previous time.Time
	if err := e.State.Load("halt-attempt", &previous); err != nil && !os.IsNotExist(err) {
		e.mu.Unlock()
		return
	}
	if !previous.IsZero() && now.Sub(previous) < 10*time.Second {
		e.mu.Unlock()
		return
	}
	if err := e.State.Save("halt-attempt", now.UTC()); err != nil {
		e.mu.Unlock()
		return
	}
	e.haltAttempt = now
	e.mu.Unlock()
	err := e.API.Halt(ctx, h.State, h.Until)
	e.mu.Lock()
	if err == nil && version == e.haltVersion {
		e.haltPending = false
	}
	e.mu.Unlock()
}

// Separate from the serial execution loop: pause expiry and reporting continue
// during a running script, while ready heartbeats retain their own cadence.
func (e *Engine) runHalt(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		e.deliverHalt(sendCtx, time.Now())
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
