package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Cipher interface {
	Seal([]byte) ([]byte, error)
	Open([]byte) ([]byte, error)
}
type Marker struct {
	Result Result `json:"result"`
	Phase  string `json:"phase"`
}
type Journal struct {
	Results []Result          `json:"results"`
	Dropped uint64            `json:"dropped"`
	Marker  *Marker           `json:"marker,omitempty"`
	Slots   map[string]string `json:"slots"`
}
type State struct {
	Root    string
	cipher  Cipher
	mu      sync.Mutex
	dead    bool
	journal Journal
}

const journalMax = 16 << 20

func OpenState(root string, cipher Cipher) (*State, error) {
	abs, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(abs, 0700); e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(abs)
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".state-") && !entry.IsDir() {
			if e := os.Remove(filepath.Join(abs, entry.Name())); e != nil {
				return nil, e
			}
		}
	}
	s := &State{Root: abs, cipher: cipher, journal: Journal{Results: []Result{}, Slots: map[string]string{}}}
	e = s.Load("journal", &s.journal)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if s.journal.Slots == nil {
		s.journal.Slots = map[string]string{}
	}
	return s, nil
}
func (s *State) path(name string) (string, error) {
	if !safeName(name) {
		return "", ErrProtocol
	}
	return filepath.Join(s.Root, name), nil
}
func (s *State) Load(name string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return ErrRevoked
	}
	p, e := s.path(name)
	if e != nil {
		return e
	}
	info, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() > 20<<20 {
		return ErrProtocol
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	b, e := s.cipher.Open(raw)
	if e != nil {
		return e
	}
	return strict(b, v)
}
func (s *State) Save(name string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return ErrRevoked
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	b, e := s.cipher.Seal(raw)
	if e != nil {
		return e
	}
	return s.write(name, b)
}
func (s *State) write(name string, b []byte) error {
	p, e := s.path(name)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(s.Root, ".state-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return replaceFile(tmp, p)
}
func (s *State) saveJournal(j Journal) error {
	for {
		b, e := s.cipher.Seal(encode(j))
		if e != nil {
			return e
		}
		if len(j.Results) <= 200 && len(b) <= journalMax {
			if e = s.write("journal", b); e != nil {
				return e
			}
			s.journal = j
			return nil
		}
		if len(j.Results) == 0 {
			return ErrProtocol
		}
		j.Results = j.Results[1:]
		j.Dropped++
	}
}
func (s *State) Journal() Journal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var j Journal
	json.Unmarshal(encode(s.journal), &j)
	return j
}
func (s *State) Append(r Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return ErrRevoked
	}
	if len(encode(r)) > 80<<10 {
		return ErrProtocol
	}
	j := s.cloneJournal()
	j.Results = append(j.Results, r)
	if j.Marker != nil && j.Marker.Result.Key() == r.Key() {
		j.Marker = nil
	}
	return s.saveJournal(j)
}
func (s *State) cloneJournal() Journal {
	var j Journal
	json.Unmarshal(encode(s.journal), &j)
	return j
}
func (s *State) Mark(r Result, phase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return ErrRevoked
	}
	if phase != "claimed" && phase != "started" {
		return ErrProtocol
	}
	j := s.cloneJournal()
	if j.Marker != nil && j.Marker.Result.Key() != r.Key() {
		return ErrBusy
	}
	j.Marker = &Marker{r, phase}
	return s.saveJournal(j)
}
func (s *State) ReserveSlot(schedule, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return false, ErrRevoked
	}
	j := s.cloneJournal()
	if j.Slots[schedule] >= key {
		return false, nil
	}
	if len(j.Slots) >= 4096 && j.Slots[schedule] == "" {
		return false, ErrProtocol
	}
	j.Slots[schedule] = key
	return true, s.saveJournal(j)
}
func (s *State) Recover(now time.Time) error {
	j := s.Journal()
	if j.Marker == nil {
		return nil
	}
	r := j.Marker.Result
	r.End = "interrupted"
	r.Detail = ""
	r.Log = ""
	r.FinishedAt = now.UTC().Format(time.RFC3339)
	return s.Append(r)
}
func (s *State) Deliver(send func(Result) error) error {
	for {
		j := s.Journal()
		if len(j.Results) == 0 {
			return nil
		}
		r := j.Results[0]
		if e := send(r); e != nil {
			return e
		}
		s.mu.Lock()
		if s.dead {
			s.mu.Unlock()
			return ErrRevoked
		}
		next := s.cloneJournal()
		if len(next.Results) > 0 && string(encode(next.Results[0])) == string(encode(r)) {
			next.Results = next.Results[1:]
		}
		e := s.saveJournal(next)
		s.mu.Unlock()
		if e != nil {
			return e
		}
	}
}

// DeliverOne bounds background delivery so a backlog cannot starve the clock.
func (s *State) DeliverOne(send func(Result) error) error {
	j := s.Journal()
	if len(j.Results) == 0 {
		return nil
	}
	r := j.Results[0]
	if err := send(r); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return ErrRevoked
	}
	next := s.cloneJournal()
	if len(next.Results) > 0 && string(encode(next.Results[0])) == string(encode(r)) {
		next.Results = next.Results[1:]
	}
	return s.saveJournal(next)
}
func (s *State) Revoke() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dead = true
	s.journal = Journal{}
	// Root is fixed at construction, never derived from a server field.
	if s.Root == "" || filepath.Dir(s.Root) == s.Root {
		return errors.New("state_root")
	}
	return os.RemoveAll(s.Root)
}

// Slot reservation and the claimed marker form one durable transaction.
func (s *State) ReserveRun(schedule string, r Result) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return false, ErrRevoked
	}
	j := s.cloneJournal()
	if j.Slots[schedule] >= r.RunKey {
		return false, nil
	}
	if j.Marker != nil {
		return false, ErrBusy
	}
	if len(j.Slots) >= 4096 && j.Slots[schedule] == "" {
		return false, ErrProtocol
	}
	j.Slots[schedule] = r.RunKey
	j.Marker = &Marker{r, "claimed"}
	return true, s.saveJournal(j)
}
