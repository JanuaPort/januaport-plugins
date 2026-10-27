package client

import (
	"context"
	"errors"
	"time"
)

func (e *Engine) recoverContainers(ctx context.Context) error {
	if e.Container != nil {
		if err := e.Container.Recover(ctx); err != nil {
			return err
		}
	}
	return e.State.Recover(time.Now())
}
func containerResult(r Result) Result {
	if r.MountedSecretNames == nil {
		empty := []string{}
		r.MountedSecretNames = &empty
	}
	return r
}

func (e *Engine) executeContainer(ctx context.Context, r Result, p Pin, allowed []string, input []byte, token string, wall int) Result {
	r = containerResult(r)
	if !e.runMu.TryLock() {
		return finish(r, "interrupted", "")
	}
	defer e.runMu.Unlock()
	if e.Container == nil || !validContainerPin(p) || p.Commit != r.Commit {
		return refused(r, ErrHash)
	}
	_, err := e.Container.MountNames(p, allowed)
	if err != nil {
		return refused(r, err)
	}
	// A crash marker must not claim that a merely planned mount happened.
	// Recovery reports an empty (unknown) subset; the pin declaration remains
	// part of the core's audit. Completed execution reports observed mounts.
	if err = e.State.Mark(r, "started"); err != nil {
		return finish(r, "interrupted", "")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	done := make(chan struct{})
	e.activeDone = done
	e.activeKey = r.Key()
	e.activeCancel = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.activeKey = ""
		e.activeCancel = nil
		e.activeDone = nil
		close(done)
		e.mu.Unlock()
	}()
	c := *p.Container
	p.Container = &c
	if wall > 0 {
		c.WallS = min(wall, c.WallS)
	}
	names, err := e.Container.Run(ctx, r.Key(), p, allowed, input, token)
	r.MountedSecretNames = &names
	switch {
	case err == nil:
		return finish(r, "ok", "")
	case errors.Is(err, ErrWall):
		return finish(r, "limit", "wall")
	case errors.Is(err, ErrInterrupted):
		return finish(r, "interrupted", "")
	case errors.Is(err, errContainerExit):
		return finish(r, "error", "")
	default:
		return refused(r, err)
	}
}

func (e *Engine) scheduledContainer(ctx context.Context, r Result, p Pin, s Schedule) error {
	// K1 rejects runs/start for empty declared_tools, including containers.
	// As on contract 1, the signed schedule itself is the grant. No run token
	// is issued; offline still uses the historical grace window.
	return e.report(e.executeContainer(ctx, containerResult(r), p, p.Container.SecretNames, s.Input, "", p.Container.WallS))
}
