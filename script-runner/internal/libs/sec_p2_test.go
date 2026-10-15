package libs

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

func TestCheckStructureRejectsCentralDirectoryGapOrTail(t *testing.T) {
	for _, tail := range []string{"\x00", "unused central directory tail"} {
		t.Run(tail, func(t *testing.T) {
			z := zipOf(t, zent{name: "a.py", data: []byte("x")})
			pos := bytes.LastIndex(z, []byte{'P', 'K', 5, 6})
			mutated := append(append(append([]byte{}, z[:pos]...), []byte(tail)...), z[pos:]...)
			eocd := mutated[pos+len(tail):]
			binary.LittleEndian.PutUint32(eocd[12:16], binary.LittleEndian.Uint32(eocd[12:16])+uint32(len(tail)))
			mustHaveReason(t, checkStructure(bytes.NewReader(mutated), int64(len(mutated)), DefaultLimits.MaxWheelEntries), contract.ReasonLibsInvalid)
		})
	}
}

type observingSource struct {
	Source
	entered chan struct{}
	gate    chan struct{}
}

func (s observingSource) Fetch(ctx context.Context, file string, dst io.Writer, max int64) error {
	close(s.entered)
	select {
	case <-s.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.Source.Fetch(ctx, file, dst, max)
}

func TestEnsureDuringResetStartsNoJob(t *testing.T) {
	f := newLibFixture(t, nil)
	f.m.Ensure(f.plan, f.src)
	waitState(t, f.m, f.plan.Lock.Hash, StatusReady)
	release, ok := f.m.Acquire(f.plan.Lock.Hash)
	if !ok {
		t.Fatal("no lease")
	}
	defer release()
	done := make(chan struct{})
	go func() { f.m.Reset(); close(done) }()
	deadline := time.After(5 * time.Second)
	for {
		f.m.mu.Lock()
		draining := f.m.draining
		f.m.mu.Unlock()
		if draining {
			break
		}
		select {
		case <-deadline:
			t.Fatal("reset did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	src := observingSource{Source: f.src, entered: make(chan struct{}), gate: make(chan struct{})}
	defer close(src.gate)
	next := f.planFor("helper", "2.1")
	st := f.m.Ensure(next, src)
	f.m.mu.Lock()
	jobs := len(f.m.jobs)
	f.m.mu.Unlock()
	if st.Status != StatusPreparing || jobs != 0 {
		t.Errorf("Ensure during reset: status=%v jobs=%d", st.Status, jobs)
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reset blocked")
	}
	select {
	case <-src.entered:
		t.Fatal("Ensure started fetch while draining")
	default:
	}
}

func TestStagingNeverVisibleInLibsDuringFetch(t *testing.T) {
	f := newLibFixture(t, nil)
	src := observingSource{Source: f.src, entered: make(chan struct{}), gate: make(chan struct{})}
	// One wheel: the source is called once.
	plan := f.planFor("demo", "1.0")
	f.m.Ensure(plan, src)
	<-src.entered
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, de := range entries {
		if filepath.Ext(de.Name()) == ".tmp" || filepath.Ext(de.Name()) == ".wheel" {
			t.Errorf("staging exposed: %s", de.Name())
		}
	}
	close(src.gate)
	waitState(t, f.m, plan.Lock.Hash, StatusReady)
	if err := verifyDir(f.m.Path(plan.Lock.Hash), plan.Lock.Hash); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsMissingOrVisibleStaging(t *testing.T) {
	dir := tempDir(t)
	for _, stage := range []string{"", dir, filepath.Join(dir, "staging"), filepath.Dir(dir)} {
		if m, err := Open(dir, stage, DefaultLimits, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
			m.Close()
			t.Fatalf("unsafe staging accepted: %q", stage)
		}
	}
}
