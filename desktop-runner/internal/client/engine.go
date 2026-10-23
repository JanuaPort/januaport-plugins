package client

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

type Bundle struct {
	Manifest     ManifestState          `json:"manifest"`
	Config       Config                 `json:"config"`
	Environments map[string]Environment `json:"environments"`
}
type Engine struct {
	State        *State
	API          *API
	Credential   Credential
	Version      string
	Session      func() (Session, error)
	Spawn        Spawn
	ChildEnv     []string
	Config       Config
	mu           sync.Mutex
	runMu        sync.Mutex
	bundle       Bundle
	active       Process
	activeKey    string
	activeCancel context.CancelFunc
	cancel       context.CancelFunc
	syncErr      error
}

func envKey(p Pin) string      { return p.Commit + "-" + sum([]byte(p.Path)) }
func (e *Engine) view() Bundle { e.mu.Lock(); defer e.mu.Unlock(); return e.bundle }
func (e *Engine) Revoke() error {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	p := e.active
	e.mu.Unlock()
	if p != nil {
		p.Kill()
		p.Wait()
	}
	return e.State.Revoke()
}
func (e *Engine) Sync(ctx context.Context) error {
	config, err := e.API.Config(ctx)
	if err != nil {
		return err
	}
	if config.Device.ID != e.Credential.DeviceID {
		return ErrManifest
	}
	signed, err := e.API.Manifest(ctx)
	if err != nil {
		return err
	}
	old := e.view()
	var seq uint64
	if len(old.Manifest.Envelope.Manifest) > 0 {
		m, err := ParseManifest(old.Manifest.Envelope.Manifest)
		if err != nil {
			return err
		}
		seq = m.Seq
	}
	m, err := VerifyManifest(signed, e.Credential.Pubkey, e.Credential.DeviceID, seq)
	if err != nil {
		return err
	}
	envs := map[string]Environment{}
	for _, p := range m.Pins {
		key := envKey(p)
		env, ok := old.Environments[key]
		if !ok {
			loadErr := e.State.Load("env-"+key, &env)
			ok = loadErr == nil
			if loadErr != nil && !os.IsNotExist(loadErr) {
				return loadErr
			}
		}
		if ok {
			if env.Origin.Pin != p.Pin || env.Origin.Algo != p.Algo {
				return ErrManifest
			}
			if err = VerifyEnvironment(filepath.Join(e.State.Root, "envs", env.Dir), env.Files); err != nil {
				return err
			}
			if !sameSet(env.Tools, p.DeclaredTools) {
				return ErrManifest
			}
			envs[key] = env
			continue
		}
		raw, err := e.API.Package(ctx, p)
		if err != nil {
			return err
		}
		env = Environment{}
		err = e.State.Use(func() error {
			var x error
			env, x = Prepare(filepath.Join(e.State.Root, "envs"), raw, p)
			if x != nil {
				return x
			}
			dir := filepath.Join(e.State.Root, "packages")
			if x = os.MkdirAll(dir, 0700); x != nil {
				return x
			}
			return os.WriteFile(filepath.Join(dir, key), raw, 0600)
		})
		if err != nil {
			return err
		}
		if err = e.State.Save("env-"+key, env); err != nil {
			return err
		}
		envs[key] = env
	}
	next := Bundle{ManifestState{signed, time.Now().UTC()}, config, envs}
	if err = e.State.Save("manifest", next); err != nil {
		return err
	}
	e.mu.Lock()
	e.bundle = next
	e.Config = config
	e.mu.Unlock()
	return nil
}
func sameSet(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
func (e *Engine) isReady() bool {
	b := e.view()
	if len(b.Manifest.Envelope.Manifest) == 0 {
		return false
	}
	s, err := e.Session()
	if err != nil {
		return false
	}
	for _, env := range b.Environments {
		if VerifyEnvironment(filepath.Join(e.State.Root, "envs", env.Dir), env.Files) != nil {
			return false
		}
	}
	return Ready(s, b.Config, len(b.Environments) > 0)
}

func ReadFrame(r io.Reader) ([]byte, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > 8<<20 {
		return nil, ErrProtocol
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}
func WriteFrame(w io.Writer, v any) error {
	b := encode(v)
	if len(b) == 0 || len(b) > 8<<20 {
		return ErrProtocol
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(b)))
	if _, err := w.Write(head[:]); err != nil {
		return err
	}
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}
func finish(r Result, end, detail string) Result {
	r.End = end
	r.Detail = detail
	r.Log = ""
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	return r
}
func refused(r Result, err error) Result {
	detail := "hash"
	switch {
	case errors.Is(err, ErrRuntime):
		detail = "runtime"
	case errors.Is(err, ErrAccount):
		detail = "account"
	case errors.Is(err, ErrManifest):
		detail = "manifest"
	case errors.Is(err, ErrOffline):
		detail = "offline"
	}
	return finish(r, "refused", detail)
}
func (e *Engine) Execute(ctx context.Context, r Result, env Environment, input json.RawMessage, runToken string, wall, calls int) Result {
	if !e.runMu.TryLock() {
		return finish(r, "internal", "")
	}
	defer e.runMu.Unlock()
	s, err := e.Session()
	e.mu.Lock()
	config := e.Config
	e.mu.Unlock()
	if err != nil || !Ready(s, config, true) {
		return refused(r, ErrAccount)
	}
	if !safeName(env.Dir) || env.Dir != envKey(env.Origin) || env.Origin.Commit != r.Commit {
		return refused(r, ErrHash)
	}
	raw, err := os.ReadFile(filepath.Join(e.State.Root, "packages", env.Dir))
	if err != nil {
		return refused(r, ErrHash)
	}
	files, err := PackageFiles(raw, env.Origin)
	if err != nil {
		return refused(r, err)
	}
	pm, err := readPackageManifest(files, env.Origin)
	if err != nil {
		return refused(r, err)
	}
	if pm.Entry != env.Entry || pm.Timeout != env.Timeout || !sameSet(pm.Tools, env.Tools) {
		return refused(r, ErrManifest)
	}

	dir := filepath.Join(e.State.Root, "envs", env.Dir)
	if VerifyEnvironment(dir, env.Files) != nil {
		return refused(r, ErrHash)
	}
	if !inputOK(input) || wall < 1 || env.Timeout < 1 {
		return finish(r, "internal", "")
	}
	if len(env.Tools) > 0 && runToken == "" {
		return refused(r, ErrOffline)
	}
	if err = e.State.Mark(r, "started"); err != nil {
		return finish(r, "internal", "")
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(min(wall, env.Timeout))*time.Second)
	defer cancel()
	launch := Launch{Dir: filepath.Join(dir, "runtime"), Exe: filepath.Join(dir, "runtime", "python.exe"), Args: []string{"-I", "-B", "bootstrap.py"}, Env: slices.Clone(e.ChildEnv)}
	e.mu.Lock()
	p, err := e.Spawn(runCtx, launch)
	if err != nil {
		e.mu.Unlock()
		return finish(r, "internal", "")
	}
	e.active = p
	e.activeKey = r.Key()
	e.activeCancel = cancel
	e.mu.Unlock()
	defer func() {
		p.Close()
		e.mu.Lock()
		e.active = nil
		e.activeKey = ""
		e.activeCancel = nil
		e.mu.Unlock()
	}()
	if err = WriteFrame(p.Input(), struct {
		Entry string          `json:"entry"`
		Input json.RawMessage `json:"input"`
	}{env.Entry, input}); err != nil {
		return finish(r, "internal", "")
	}
	exited := make(chan Result, 1)
	go func() {
		used := 0
		var lastID int64
		for {
			b, err := ReadFrame(p.Output())
			if err != nil {
				exited <- finish(r, "internal", "")
				return
			}
			var kind struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(b, &kind) != nil {
				exited <- finish(r, "internal", "")
				return
			}
			switch kind.Type {
			case "exited":
				var v struct {
					Type string `json:"type"`
					Code int    `json:"code"`
				}
				if strict(b, &v) != nil {
					exited <- finish(r, "internal", "")
					return
				}
				if v.Code == 0 {
					exited <- finish(r, "ok", "")
				} else {
					exited <- finish(r, "error", "")
				}
				return
			case "tool_call":
				var v struct {
					Type      string          `json:"type"`
					ID        int64           `json:"id"`
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				}
				if strict(b, &v) != nil || v.ID <= lastID || !slices.Contains(env.Tools, v.Name) || !inputOK(v.Arguments) || runToken == "" {
					exited <- finish(r, "internal", "")
					return
				}
				lastID = v.ID
				if used >= calls {
					exited <- finish(r, "limit", "tool_calls")
					return
				}
				used++
				result, err := e.API.MCP(runCtx, runToken, v.ID, v.Name, v.Arguments)
				if err != nil {
					exited <- finish(r, "internal", "")
					return
				}
				if err = WriteFrame(p.Input(), struct {
					Type   string          `json:"type"`
					ID     int64           `json:"id"`
					Result json.RawMessage `json:"result"`
				}{"tool_result", v.ID, result}); err != nil {
					exited <- finish(r, "internal", "")
					return
				}
			default:
				exited <- finish(r, "internal", "")
				return // stdout/log are not accepted frames.
			}
		}
	}()
	select {
	case out := <-exited:
		if runCtx.Err() != nil {
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				out = finish(r, "timeout", "")
			} else {
				out = finish(r, "cancelled", "")
			}
		}
		p.Kill()
		p.Wait()
		return out
	case <-runCtx.Done():
		p.Kill()
		p.Wait()
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return finish(r, "timeout", "")
		}
		return finish(r, "cancelled", "")
	}
}

func (e *Engine) report(r Result) error {
	if err := e.State.Append(r); err != nil {
		return err
	}
	return nil
}
func (e *Engine) scheduled(ctx context.Context, p Pin, s Schedule, slot time.Time) error {
	b := e.view()
	m, err := ParseManifest(b.Manifest.Envelope.Manifest)
	if err != nil {
		return err
	}
	r := Result{RunKey: RunKey(p, s, slot), ManifestSeq: m.Seq, Commit: p.Commit}
	ok, err := e.State.ReserveRun(p.Pin+"/"+s.Name, r)
	if err != nil || !ok {
		return err
	}
	if e.syncErr != nil {
		if !IsOffline(e.syncErr) {
			return e.report(refused(r, e.syncErr))
		}
		if !OfflineAllowed(p, m.GraceHours, b.Manifest.LastSync, time.Now()) {
			return e.report(refused(r, ErrOffline))
		}
		r.RanOffline = true
	}
	env, ok := b.Environments[envKey(p)]
	if !ok {
		return e.report(refused(r, ErrRuntime))
	}
	var token string
	if len(p.DeclaredTools) > 0 {
		start, err := e.API.Start(ctx, r)
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.Status == 409 && httpErr.Class == "manifest" {
			// A stale start has not consumed a token. Re-sync once, bind the
			// same slot to the newly signed pin, then retry at most once.
			e.syncErr = e.Sync(ctx)
			if e.syncErr != nil {
				return e.report(refused(r, ErrManifest))
			}
			b = e.view()
			m, err = ParseManifest(b.Manifest.Envelope.Manifest)
			if err != nil {
				return e.report(refused(r, ErrManifest))
			}
			p, s, err = resolveSlot(m, p.Pin, s.Name, slot)
			if err != nil {
				return e.report(refused(r, ErrManifest))
			}
			r.ManifestSeq, r.Commit = m.Seq, p.Commit
			env, ok = b.Environments[envKey(p)]
			if !ok {
				return e.report(refused(r, ErrRuntime))
			}
			if err = e.State.Mark(r, "claimed"); err != nil {
				return err
			}
			if len(p.DeclaredTools) > 0 {
				start, err = e.API.Start(ctx, r)
			} else {
				start, err = StartReply{}, nil
			}
		}
		if err != nil {
			if errors.Is(err, ErrRevoked) {
				return err
			}
			if IsOffline(err) {
				return e.report(refused(r, ErrOffline))
			}
			if errors.As(err, &httpErr) && httpErr.Class == "manifest" {
				return e.report(refused(r, ErrManifest))
			}
			return e.report(finish(r, "internal", ""))
		}
		token = start.RunToken
	}
	return e.report(e.Execute(ctx, r, env, s.Input, token, env.Timeout, 10000))
}

func resolveSlot(m Manifest, pin, name string, slot time.Time) (Pin, Schedule, error) {
	if !time.Now().UTC().Truncate(time.Minute).Equal(slot) {
		return Pin{}, Schedule{}, ErrManifest
	}
	for _, p := range m.Pins {
		if p.Pin == pin {
			for _, s := range p.Schedules {
				if s.Name == name {
					c, err := parseSchedule(s)
					if err == nil && c.Next(slot.Add(-time.Nanosecond)).Equal(slot) {
						return p, s, nil
					}
				}
			}
		}
	}
	return Pin{}, Schedule{}, ErrManifest
}
func (e *Engine) adhoc(ctx context.Context, j Job) error {
	r := Result{JobID: j.JobID, ManifestSeq: j.ManifestSeq, Commit: j.Commit}
	if err := e.State.Mark(r, "claimed"); err != nil {
		return err
	}
	deadline, _ := time.Parse(time.RFC3339, j.StartBy)
	if !time.Now().Before(deadline) {
		return e.report(finish(r, "internal", ""))
	}
	b := e.view()
	m, err := ParseManifest(b.Manifest.Envelope.Manifest)
	if err != nil {
		return e.report(refused(r, ErrManifest))
	}
	var pin *Pin
	for _, p := range m.Pins {
		if p.Pin == j.Pin && p.Commit == j.Commit && p.Path == j.Path && p.Algo == j.Algo {
			x := p
			pin = &x
			break
		}
	}
	if pin == nil || m.Seq < j.ManifestSeq {
		return e.report(refused(r, ErrManifest))
	}
	env, ok := b.Environments[envKey(*pin)]
	if !ok {
		return e.report(refused(r, ErrRuntime))
	}
	start, err := e.API.Start(ctx, r)
	if err != nil {
		return e.report(finish(r, "internal", ""))
	}
	return e.report(e.Execute(ctx, r, env, j.Input, start.RunToken, j.Limits.WallS, j.Limits.ToolCalls))
}
func (e *Engine) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()
	if err := e.State.Recover(time.Now()); err != nil {
		return err
	}
	var cached Bundle
	if err := e.State.Load("manifest", &cached); err == nil {
		if _, err = VerifyManifest(cached.Manifest.Envelope, e.Credential.Pubkey, e.Credential.DeviceID, 0); err != nil {
			return err
		}
		e.mu.Lock()
		e.bundle = cached
		e.Config = cached.Config
		e.mu.Unlock()
	} else if !os.IsNotExist(err) {
		return err
	}
	syncNeeded := make(chan struct{}, 1)
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			b := e.view()
			var seq uint64
			if m, err := ParseManifest(b.Manifest.Envelope.Manifest); err == nil {
				seq = m.Seq
			}
			h, err := e.API.Heartbeat(ctx, Heartbeat{1, e.isReady(), e.Version, seq})
			if err == nil {
				e.mu.Lock()
				p, key, cancel := e.active, e.activeKey, e.activeCancel
				e.mu.Unlock()
				if p != nil && slices.Contains(h.Cancel, key) {
					if cancel != nil {
						cancel()
					}
					p.Kill()
				}
				if h.ManifestSeqCurrent > seq {
					select {
					case syncNeeded <- struct{}{}:
					default:
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	e.syncErr = e.Sync(ctx)
	var presynced time.Time
	for ctx.Err() == nil {
		select {
		case <-syncNeeded:
			e.syncErr = e.Sync(ctx)
		default:
		}
		reportDeadline := min(5*time.Second, time.Until(time.Now().UTC().Truncate(time.Minute).Add(time.Minute)))
		reportCtx, reportStop := context.WithTimeout(ctx, reportDeadline)
		_ = e.State.DeliverOne(func(r Result) error { return e.API.Report(reportCtx, r) })
		reportStop()
		now := time.Now()
		minute := now.UTC().Truncate(time.Minute)
		b := e.view()
		m, err := ParseManifest(b.Manifest.Envelope.Manifest)
		if err == nil {
			// The same minute boundary drives pre-sync (slot - 1 minute) and due work.
			canRun := presynced.Equal(minute.Add(-time.Minute))
			presync := false
			for _, p := range m.Pins {
				for _, s := range p.Schedules {
					cron, _ := parseSchedule(s)
					if cron.Next(minute).Equal(minute.Add(time.Minute)) {
						presync = true
					}
				}
			}
			if presync && !presynced.Equal(minute) {
				e.syncErr = e.Sync(ctx)
				presynced = minute
				b = e.view()
				m, _ = ParseManifest(b.Manifest.Envelope.Manifest)
			}
			for _, p := range m.Pins {
				for _, s := range p.Schedules {
					cron, _ := parseSchedule(s)
					if canRun && time.Now().UTC().Truncate(time.Minute).Equal(minute) && cron.Next(minute.Add(-time.Nanosecond)).Equal(minute) {
						if err = e.scheduled(ctx, p, s, minute); err != nil && errors.Is(err, ErrRevoked) {
							return err
						}
					}
				}
			}
		}
		// Claim only in the same serial loop as scheduled execution. Its deadline
		// wakes the loop for the next local minute, independent of the box timer.
		wait := min(26*time.Second, time.Until(minute.Add(time.Minute)))
		if wait <= 0 {
			continue
		}
		if e.isReady() {
			pollCtx, stop := context.WithTimeout(ctx, wait)
			job, err := e.API.Claim(pollCtx)
			stop()
			if err == nil && job != nil {
				if err = e.adhoc(ctx, *job); err != nil && errors.Is(err, ErrRevoked) {
					return err
				}
				continue
			}
			if err != nil && !IsOffline(err) && !errors.Is(err, context.DeadlineExceeded) {
				wait = min(wait, 5*time.Second)
			}
		}
		timer := time.NewTimer(min(wait, time.Second))
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

// Use serializes cache writes against revocation; no writer can resurrect data.
func (s *State) Use(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return ErrRevoked
	}
	return fn()
}
