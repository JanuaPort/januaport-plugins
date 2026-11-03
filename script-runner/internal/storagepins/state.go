package storagepins

import (
	"crypto/ed25519"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"time"
)

type Approval struct {
	Name          string `json:"name"`
	Commit        string `json:"commit"`
	Path          string `json:"path"`
	PackageSHA256 string `json:"package_sha256"`
}

// Failure contains only fixed codes. Index identifies a pin in the verified
// accepted list; -1 denotes the list/key/transport as a whole. No raw input.
type Failure struct {
	Index  int    `json:"index"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type State struct {
	Version    int        `json:"version"`
	KeyFpr     string     `json:"key_fpr"`
	HighestSeq uint64     `json:"highest_seq"`
	Accepted   *Signed    `json:"accepted"`
	Approved   []Approval `json:"approved"`
	Failed     []Failure  `json:"failed"`
}

func (s State) IssuedAt() time.Time {
	if s.Accepted == nil {
		return time.Time{}
	}
	var m Manifest
	if exact(s.Accepted.Manifest, &m) != nil {
		return time.Time{}
	}
	issued, _ := time.Parse(time.RFC3339, m.IssuedAt)
	return issued
}

// Fresh is also the expiry interface for the sink: no separate expiry
// field can extend an approval. Key/signature and package validation remain required.
func (s State) Fresh(now time.Time) bool {
	issued := s.IssuedAt()
	return !issued.IsZero() && !issued.After(now.Add(Skew)) && now.Sub(issued) <= MaxAge
}

// Journal has one owner: the independent storage goroutine. Its quarantine
// clock lives only in this process; no timestamp from the volume sets it.
type Journal struct {
	root   string
	now    func() time.Time
	logger *slog.Logger
	start  time.Time
	loaded bool
	public ed25519.PublicKey
	state  State
}

func NewJournal(root string, now func() time.Time, logger *slog.Logger) *Journal {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Journal{root: root, now: now, logger: logger, start: now()}
}

func emptyState(fpr string) State {
	return State{Version: 1, KeyFpr: fpr, Approved: []Approval{}, Failed: []Failure{}}
}

// State returns a detached snapshot, never mutable journal storage.
func (j *Journal) State() State {
	b, _ := json.Marshal(j.state)
	var s State
	_ = json.Unmarshal(b, &s)
	return s
}

func (j *Journal) write(s State) error {
	b, e := json.Marshal(s)
	if e != nil {
		return ErrInvalid
	}
	if e = atomicWrite(j.root, b, randomName); e != nil {
		return e
	}
	j.state = s
	return nil
}

// SetKey loads once, and immediately publishes empty approvals on missing or
// changed keys. A failed publication is retried on the next call.
func (j *Journal) SetKey(public ed25519.PublicKey) error {
	fpr := ""
	if len(public) > 0 {
		if !validPublicKey(public) {
			return ErrInvalid
		}
		fpr = fingerprint(public)
	}
	if !j.loaded {
		b, e := ReadFile(filepath.Join(j.root, "state.json"), MaxListBytes)
		var s State
		if e == nil && exact(b, &s) == nil && validState(s, public) {
			j.state = s
		} else {
			j.state = emptyState(fpr)
			// Absence and corruption have the same quarantine. The log remains fixed.
			j.logger.Warn("storage_state_reset")
		}
		j.loaded = true
	}
	if j.state.KeyFpr != fpr {
		j.start = j.now()
		j.state = emptyState(fpr)
		j.logger.Warn("storage_key_changed")
	}
	j.public = append(ed25519.PublicKey(nil), public...)
	if fpr == "" {
		j.logger.Warn("storage_key_missing")
		j.state.Approved = []Approval{}
		j.state.Failed = []Failure{{-1, "invalid", "fetch_failed"}}
	}
	// Also materialize a reset on first load (including a zero-sequence restart).
	return j.write(j.state)
}

func validState(s State, public ed25519.PublicKey) bool {
	if s.Version != 1 || s.Approved == nil || s.Failed == nil {
		return false
	}
	if s.HighestSeq == 0 {
		return s.Accepted == nil && len(s.Approved) == 0 && validFailures(s.Failed, 0)
	}
	// On a changed key only the old fingerprint is used to trigger reset; none
	// of its approval data survives SetKey.
	if s.KeyFpr != fingerprint(public) {
		return len(s.KeyFpr) == 64
	}
	if s.Accepted == nil {
		return false
	}
	raw, e := json.Marshal(s.Accepted)
	if e != nil {
		return false
	}
	_, m, e := Verify(raw, public, s.HighestSeq, time.Time{}, s.IssuedAt())
	return e == nil && m.Seq == s.HighestSeq && validApprovals(s.Approved, m) && validFailures(s.Failed, len(m.Pins))
}

func validApprovals(approved []Approval, m Manifest) bool {
	prev := ""
	for _, a := range approved {
		if a.Name <= prev || !hexDigest(a.PackageSHA256) {
			return false
		}
		prev = a.Name
		found := false
		for _, p := range m.Pins {
			if a.Name == p.Name && a.Commit == p.Commit && a.Path == p.Path {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func validFailures(failed []Failure, n int) bool {
	for _, f := range failed {
		if f.Index < -1 || f.Index >= n {
			return false
		}
		if f.State == "pending" && f.Reason == "" {
			continue
		}
		if f.State != "invalid" || (f.Reason != "fetch_failed" && f.Reason != "hash_mismatch") {
			return false
		}
	}
	return true
}

func (j *Journal) Check(raw []byte) (Signed, Manifest, error) {
	now := j.now()
	if len(j.public) == 0 || (j.state.HighestSeq == 0 && !now.After(j.start.Add(MaxAge+Skew))) {
		return Signed{}, Manifest{}, ErrInvalid
	}
	return Verify(raw, j.public, j.state.HighestSeq, j.state.IssuedAt(), now)
}

// Publish commits the sequence, signed list, approvals and fixed failures in
// one durable rename. Even callers inside the module cannot bypass Check.
func (j *Journal) Publish(s Signed, m Manifest, approved []Approval, failed []Failure) error {
	raw, e := json.Marshal(s)
	if e != nil {
		return ErrInvalid
	}
	checked, verified, e := j.Check(raw)
	if e != nil {
		return e
	}
	a, _ := json.Marshal(m)
	b, _ := json.Marshal(verified)
	if string(a) != string(b) || !validApprovals(approved, m) || !validFailures(failed, len(m.Pins)) {
		return ErrInvalid
	}
	if approved == nil {
		approved = []Approval{}
	}
	if failed == nil {
		failed = []Failure{}
	}
	return j.write(State{1, j.state.KeyFpr, m.Seq, &checked, approved, failed})
}

// Fail never persists an input body or an error string. A failed list yields
// no approvals; the last accepted envelope and sequence still prevent replay.
func (j *Journal) Fail() error {
	s := j.State()
	s.Approved = []Approval{}
	s.Failed = []Failure{{-1, "invalid", "fetch_failed"}}
	return j.write(s)
}
