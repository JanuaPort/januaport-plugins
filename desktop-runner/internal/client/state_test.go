package client

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testCipher struct{}

func (testCipher) Seal(b []byte) ([]byte, error) { return append([]byte("TEST-CIPHER:"), b...), nil }
func (testCipher) Open(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte("TEST-CIPHER:")) {
		return nil, errors.New("cipher")
	}
	return b[12:], nil
}
func TestJournalDeckelBestaetigungNeustart(t *testing.T) {
	st, e := OpenState(t.TempDir(), testCipher{})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 201; i++ {
		r := Result{RunKey: time.Unix(int64(i*60), 0).UTC().Format(time.RFC3339), End: "ok"}
		if e = st.Append(r); e != nil {
			t.Fatal(e)
		}
	}
	j := st.Journal()
	if len(j.Results) != 200 || j.Dropped != 1 {
		t.Fatalf("len=%d dropped=%d", len(j.Results), j.Dropped)
	}
	if e = st.Deliver(func(Result) error { return errors.New("409") }); e == nil {
		t.Fatal("fehlende Bestätigung")
	}
	if len(st.Journal().Results) != 200 {
		t.Fatal("vor Bestätigung gelöscht")
	}
	if e = st.Deliver(func(Result) error { return nil }); e != nil || len(st.Journal().Results) != 0 {
		t.Fatal(e)
	}
	if e = st.Mark(Result{JobID: "job", ManifestSeq: 1, Commit: "commit"}, "started"); e != nil {
		t.Fatal(e)
	}
	st, e = OpenState(st.Root, testCipher{})
	if e != nil {
		t.Fatal(e)
	}
	if e = st.Recover(time.Now()); e != nil {
		t.Fatal(e)
	}
	j = st.Journal()
	if len(j.Results) != 1 || j.Results[0].End != "interrupted" || j.Marker != nil {
		t.Fatal(j)
	}
}
func TestWiderrufAllesWeg(t *testing.T) {
	st, e := OpenState(t.TempDir(), testCipher{})
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"credential", "manifest", "journal", "packages/a", "envs/a/x"} {
		f := filepath.Join(st.Root, p)
		os.MkdirAll(filepath.Dir(f), 0700)
		os.WriteFile(f, []byte("private"), 0600)
	}
	if e = st.Revoke(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(st.Root); !os.IsNotExist(e) {
		t.Fatal("Daten bleiben")
	}
	if e = st.Append(Result{}); e != ErrRevoked {
		t.Fatal("Widerruf wiederbelebt", e)
	}
}

func TestErgebnisrueckstauUndAtomarerSlot(t *testing.T) {
	st, _ := OpenState(t.TempDir(), testCipher{})
	r := Result{RunKey: "demo/daily/2026-10-23T10:00:00Z"}
	ok, e := st.ReserveRun("demo/daily", r)
	if !ok || e != nil {
		t.Fatal(e)
	}
	st, e = OpenState(st.Root, testCipher{})
	if e != nil {
		t.Fatal(e)
	}
	if e = st.Recover(time.Now()); e != nil {
		t.Fatal(e)
	}
	if ok, e = st.ReserveRun("demo/daily", r); ok || e != nil {
		t.Fatal("Slot erneut reserviert", e)
	}
	if e = st.Append(Result{JobID: "second"}); e != nil {
		t.Fatal(e)
	}
	calls := 0
	if e = st.DeliverOne(func(Result) error { calls++; return errors.New("409") }); e == nil || len(st.Journal().Results) != 2 {
		t.Fatal("ohne Bestätigung gelöscht")
	}
	if e = st.DeliverOne(func(Result) error { calls++; return nil }); e != nil || len(st.Journal().Results) != 1 || calls != 2 {
		t.Fatal("Rückstau blockiert Takt", e)
	}
}
