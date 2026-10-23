package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"time"
)

var (
	ErrHash     = errors.New("refused/hash")
	ErrRuntime  = errors.New("refused/runtime")
	ErrManifest = errors.New("refused/manifest")
	ErrAccount  = errors.New("refused/account")
	ErrOffline  = errors.New("refused/offline")
	ErrProtocol = errors.New("protocol")
	ErrRevoked  = errors.New("revoked")
	ErrBusy     = errors.New("busy")
)

// strict also rejects duplicate keys, null/missing required fields and trailing JSON.
func strict(b []byte, v any) error {
	if _, err := canonical(b); err != nil {
		return ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return ErrProtocol
	}
	if err := required(b, reflect.TypeOf(v).Elem()); err != nil {
		return err
	}
	return nil
}
func required(b []byte, t reflect.Type) error {
	if t == reflect.TypeOf(json.RawMessage{}) || t == reflect.TypeOf(time.Time{}) {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var m map[string]json.RawMessage
		if json.Unmarshal(b, &m) != nil || m == nil {
			return ErrProtocol
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if name == "" {
				name = f.Name
			}
			raw, ok := m[name]
			if !ok && strings.Contains(opts, "omitempty") {
				continue
			}
			if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return ErrProtocol
			}
			if err := required(raw, f.Type); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Struct {
			var a []json.RawMessage
			if json.Unmarshal(b, &a) != nil {
				return ErrProtocol
			}
			for _, x := range a {
				if err := required(x, t.Elem()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type Pin struct {
	Pin           string     `json:"pin"`
	Commit        string     `json:"commit"`
	Path          string     `json:"path"`
	Algo          string     `json:"algo"`
	DeclaredTools []string   `json:"declared_tools"`
	Schedules     []Schedule `json:"schedules"`
}
type Schedule struct {
	Name  string          `json:"name"`
	Cron  string          `json:"cron"`
	TZ    string          `json:"tz"`
	Input json.RawMessage `json:"input"`
}
type Manifest struct {
	Contract   int    `json:"contract"`
	DeviceID   string `json:"device_id"`
	Seq        uint64 `json:"seq"`
	IssuedAt   string `json:"issued_at"`
	GraceHours int    `json:"grace_hours"`
	Pins       []Pin  `json:"pins"`
}
type SignedManifest struct {
	Manifest json.RawMessage `json:"manifest"`
	Sig      string          `json:"sig"`
}
type ManifestState struct {
	Envelope SignedManifest `json:"envelope"`
	LastSync time.Time      `json:"last_sync"`
}
type Config struct {
	Contract int `json:"contract"`
	Device   struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"device"`
	ExpectedAccount string `json:"expected_account"`
	StartPolicy     struct {
		Mode        string `json:"mode"`
		IdleMinutes int    `json:"idle_minutes"`
	} `json:"start_policy"`
	HeartbeatS int `json:"heartbeat_s"`
	PollHoldS  int `json:"poll_hold_s"`
}
type Heartbeat struct {
	Contract      int    `json:"contract"`
	Ready         bool   `json:"ready"`
	RunnerVersion string `json:"runner_version"`
	ManifestSeq   uint64 `json:"manifest_seq"`
}
type HeartbeatReply struct {
	Cancel             []string `json:"cancel"`
	ManifestSeqCurrent uint64   `json:"manifest_seq_current"`
}
type Job struct {
	JobID       string          `json:"job_id"`
	Pin         string          `json:"pin"`
	Commit      string          `json:"commit"`
	Path        string          `json:"path"`
	Algo        string          `json:"algo"`
	ManifestSeq uint64          `json:"manifest_seq"`
	Input       json.RawMessage `json:"input"`
	Limits      struct {
		WallS     int `json:"wall_s"`
		ToolCalls int `json:"tool_calls"`
	} `json:"limits"`
	StartBy string `json:"start_by"`
}
type Result struct {
	RunKey      string `json:"run_key,omitempty"`
	JobID       string `json:"job_id,omitempty"`
	ManifestSeq uint64 `json:"manifest_seq"`
	Commit      string `json:"commit"`
	End         string `json:"end"`
	Detail      string `json:"detail,omitempty"`
	Log         string `json:"log"`
	RanOffline  bool   `json:"ran_offline"`
	FinishedAt  string `json:"finished_at"`
}

func (r Result) Key() string {
	if r.JobID != "" {
		return r.JobID
	}
	return r.RunKey
}

type Session struct {
	Active  bool
	Account string
	Idle    time.Duration
}

func Ready(s Session, c Config, verified bool) bool {
	return verified && s.Active && s.Account != "" && strings.EqualFold(s.Account, c.ExpectedAccount) && (c.StartPolicy.Mode == "always" || (c.StartPolicy.Mode == "idle" && s.Idle >= time.Duration(c.StartPolicy.IdleMinutes)*time.Minute))
}

// Launch has no run-token member; it is also used by the real Windows adapter.
type Launch struct {
	Dir  string
	Exe  string
	Args []string
	Env  []string
}
type Process interface {
	Input() io.WriteCloser
	Output() io.ReadCloser
	Wait() error
	Kill() error
	Close() error
}
type Spawn func(context.Context, Launch) (Process, error)
