package storagepins

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestZeroSequenceQuarantineAllResetPaths(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "changed", "restart"} {
		t.Run(mode, func(t *testing.T) {
			start := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
			now := start
			root := t.TempDir()
			p, k := testKey(t)
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			if mode == "corrupt" {
				if e := os.WriteFile(filepath.Join(root, "state.json"), []byte("secret-canary"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			j := NewJournal(root, func() time.Time { return now }, logger)
			if mode == "changed" {
				other, _ := testKey(t)
				if e := j.SetKey(other); e != nil {
					t.Fatal(e)
				}
			}
			if e := j.SetKey(p); e != nil {
				t.Fatal(e)
			}
			if mode == "restart" {
				now = now.Add(time.Minute)
				start = now
				j = NewJournal(root, func() time.Time { return now }, logger)
				if e := j.SetKey(p); e != nil {
					t.Fatal(e)
				}
			}
			old := signList(t, k, 9, start.Add(-time.Minute))
			for _, d := range []time.Duration{0, time.Minute, 12*time.Minute - time.Nanosecond, 12 * time.Minute} {
				now = start.Add(d)
				if _, _, e := j.Check(old); e == nil {
					t.Fatal("old list bypassed quarantine", d)
				}
				if _, _, e := j.Check(signList(t, k, 9, now)); e == nil {
					t.Fatal("fresh list bypassed quarantine", d)
				}
			}
			now = start.Add(12*time.Minute + time.Nanosecond)
			signed, list, e := j.Check(signList(t, k, 9, now))
			if e != nil {
				t.Fatal(e)
			}
			if e = j.Publish(signed, list, nil, nil); e != nil {
				t.Fatal(e)
			}
			j = NewJournal(root, func() time.Time { return now }, logger)
			if e = j.SetKey(p); e != nil {
				t.Fatal(e)
			}
			if j.State().HighestSeq != 9 {
				t.Fatal("sequence lost")
			}
			if _, _, e = j.Check(signList(t, k, 8, now)); e == nil {
				t.Fatal("restart replay")
			}
			if strings.Contains(logs.String(), "secret-canary") {
				t.Fatal("log echo")
			}
			if mode == "corrupt" && !strings.Contains(logs.String(), "storage_state_reset") {
				t.Fatal("reset code missing")
			}
			if mode == "changed" && !strings.Contains(logs.String(), "storage_key_changed") {
				t.Fatal("change code missing")
			}
		})
	}
}

func TestKeyBackAndForthRestartsQuarantine(t *testing.T) {
	now := time.Now()
	a, ka := testKey(t)
	b, _ := testKey(t)
	j := NewJournal(t.TempDir(), func() time.Time { return now }, nil)
	for _, p := range [][]byte{a, b, a} {
		if e := j.SetKey(p); e != nil {
			t.Fatal(e)
		}
		now = now.Add(3 * time.Minute)
	}
	if _, _, e := j.Check(signList(t, ka, 1, now)); e == nil {
		t.Fatal("A-B-A replay")
	}
}

func TestExpiryDerivedOnlyFromAcceptedManifest(t *testing.T) {
	p, k := testKey(t)
	now := time.Now().UTC().Truncate(time.Second)
	raw := signList(t, k, 1, now)
	s, _, e := Verify(raw, p, 0, time.Time{}, now)
	if e != nil {
		t.Fatal(e)
	}
	st := State{Version: 1, Accepted: &s}
	if !st.Fresh(now.Add(10*time.Minute)) || st.Fresh(now.Add(10*time.Minute+time.Nanosecond)) {
		t.Fatal("expiry boundary")
	}
	st.Accepted = nil
	if st.Fresh(now) {
		t.Fatal("missing accepted")
	}
}

func TestStateRejectsUntrustedFieldsAndFixedFailureCodes(t *testing.T) {
	now := time.Now()
	p, _ := testKey(t)
	root := t.TempDir()
	// No unverified persisted bytes may be copied back out on reset.
	for _, raw := range []string{`{"version":1,"expiry":"2099-01-01","approved":["secret-canary"]}`, `{"version":1,"highest_seq":999,"key_fpr":"secret-canary"}`} {
		if e := os.WriteFile(filepath.Join(root, "state.json"), []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		j := NewJournal(root, func() time.Time { return now }, nil)
		if e := j.SetKey(p); e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join(root, "state.json"))
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(b, []byte("secret-canary")) {
			t.Fatal("echo")
		}
		var st State
		if json.Unmarshal(b, &st) != nil || st.HighestSeq != 0 || len(st.Approved) != 0 {
			t.Fatal("not reset")
		}
	}
}
