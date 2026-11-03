package mediator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitobjects"
	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
)

func storageMeta(t *testing.T, e *env) contract.RunnerMeta {
	t.Helper()
	b, err := json.Marshal(e.listTools().Meta[contract.MetaRunner])
	if err != nil {
		t.Fatal(err)
	}
	var meta contract.RunnerMeta
	if err = json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

func addGitDemo(t *testing.T, x *storageAdapter) string {
	t.Helper()
	f := x.e.fx
	f.write("demo/jnpt-script.yaml", strings.Replace(quickManifest, "name: quick", "name: demo", 1))
	f.write("demo/main.py", "print('git demo')\n")
	f.git(f.work, "add", "demo/jnpt-script.yaml", "demo/main.py")
	f.git(f.work, "commit", "-q", "-m", "Git demo")
	commit := f.git(f.work, "rev-parse", "HEAD")
	f.git(filepath.Join(f.store, "store.git"), "fetch", "-q", f.work, "HEAD:refs/heads/demo")
	if err := os.WriteFile(filepath.Join(f.store, "approved", commit), []byte("ssh://git@git.example.com/org/scripts.git"), 0600); err != nil {
		t.Fatal(err)
	}
	x.e.writePins([]pinRow{{Name: "demo", Path: "demo", Commit: commit}})
	return commit
}

// F1: both otherwise valid sources collide at the MCP boundary, regardless
// of which commit the caller supplies. A rejected call sends no sandbox job.
func TestGitStorageNameCollisionAtMCP(t *testing.T) {
	x := newStorageAdapter(t)
	x.approve()
	g := dialGuard(t, x.e.sock, "collision", 2)
	x.e.waitReady("demo")
	gitCommit := addGitDemo(t, x)
	waitFor(t, "both collision entries published", func() bool { return len(storageMeta(t, x.e).Pins) == 2 })
	meta := storageMeta(t, x.e)
	for _, p := range meta.Pins {
		if p.Name != "demo" || p.State != contract.StateInvalid || p.Reason != contract.ReasonManifestInvalid {
			t.Fatalf("collision not rejected on both sources: %+v", meta.Pins)
		}
	}
	if len(x.e.listTools().Tools) != 0 {
		t.Fatal("colliding name listed as a tool")
	}
	for _, commit := range []string{gitCommit, x.pin.Commit} {
		r, err := x.e.call("demo", map[string]any{}, commit)
		if err != nil {
			t.Fatal(err)
		}
		run := runMeta(t, r)
		if run["end"] != contract.EndRefused {
			t.Fatalf("collision executed: %+v", run)
		}
	}
	assertNoSandboxJob(t, g)
	// Prove the Git side was not independently invalid before the collision.
	x.sign(t, 10, []storagepins.Pin{})
	x.transport()
	x.approve()
	x.e.waitReady("demo")
}

// H4: an unsigned inbox must not reserve a name or disable the real Git pin.
func TestUnsignedStorageNameDoesNotCollideAtMCP(t *testing.T) {
	x := newStorageAdapter(t)
	g := dialGuard(t, x.e.sock, "unsigned-collision", 2)
	x.mu.Lock()
	var signed storagepins.Signed
	if err := json.Unmarshal(x.body, &signed); err != nil {
		x.mu.Unlock()
		t.Fatal(err)
	}
	signed.Sig = "unsigned"
	x.body, _ = json.Marshal(signed)
	x.mu.Unlock()
	x.transport()
	x.approve()
	commit := addGitDemo(t, x)
	x.e.waitReady("demo")
	meta := storageMeta(t, x.e)
	ready := 0
	for _, p := range meta.Pins {
		if p.Commit == commit {
			if p.State != contract.StateReady || p.Reason != "" {
				t.Fatalf("unsigned name disabled Git: %+v", p)
			}
			ready++
		} else if p.State == contract.StateReady {
			t.Fatal("unsigned storage became ready")
		}
	}
	if ready != 1 || len(x.e.listTools().Tools) != 1 {
		t.Fatal("expected exactly the Git tool")
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = g.job(); g.exited(0) }()
	r, err := x.e.call("demo", map[string]any{}, commit)
	if err != nil {
		t.Fatal(err)
	}
	assertEnd(t, r, contract.EndOK, "")
	<-done
}

// H2: even a syntactically coherent state with approval plus failure is denied.
func TestStorageFailureOverridesApprovalAtMCP(t *testing.T) {
	for _, index := range []int{-1, 0} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			x := newStorageAdapter(t)
			x.approve()
			g := dialGuard(t, x.e.sock, "failure", 2)
			p := x.cached(t)
			root := filepath.Join(x.e.fx.store, "speicher")
			s, _, err := storagepins.ReadState(root)
			if err != nil {
				t.Fatal(err)
			}
			s.Failed = []storagepins.Failure{{Index: index, State: contract.StateInvalid, Reason: contract.ReasonFetchFailed}}
			raw, _ := json.Marshal(s)
			if err = storagepins.WriteFile(filepath.Join(root, "state.json"), raw, storagepins.MaxListBytes); err != nil {
				t.Fatal(err)
			}
			if err = x.e.m.pinSource(p).CheckStorage(); err == nil {
				t.Fatal("sink accepted approval with failure")
			}
			waitFor(t, "failure visible at MCP", func() bool {
				meta := storageMeta(t, x.e)
				return len(meta.Pins) == 1 && meta.Pins[0].State == contract.StateInvalid && meta.Pins[0].Reason == contract.ReasonFetchFailed
			})
			if len(x.e.listTools().Tools) != 0 {
				t.Fatal("failure remained ready")
			}
			r, err := x.e.call(x.pin.Name, map[string]any{}, x.pin.Commit)
			if err != nil {
				t.Fatal(err)
			}
			assertEnd(t, r, contract.EndRefused, contract.DetailState)
			assertNoSandboxJob(t, g)
		})
	}
}

// H3: publish a real new graph through courier and fetcher, retain a stale
// pinState as a queued caller would, and prove it cannot authorize old code.
func TestStorageRenewalRejectsCachedOldCommit(t *testing.T) {
	x := newStorageAdapter(t)
	x.approve()
	g := dialGuard(t, x.e.sock, "renewal", 2)
	old := x.cached(t)
	pkg, commit := renewStoragePackage(t, x.pkg, x.pin)
	x.mu.Lock()
	x.pkg = pkg
	x.mu.Unlock()
	x.pin.Commit = commit
	x.sign(t, 10, []storagepins.Pin{x.pin})
	x.transport()
	x.approve()
	waitFor(t, "new commit ready", func() bool {
		meta := storageMeta(t, x.e)
		return len(meta.Pins) == 1 && meta.Pins[0].Commit == commit && meta.Pins[0].State == contract.StateReady
	})
	// Retain the old file so that refusal proves approval identity, not absence.
	oldBytes, err := os.ReadFile("testdata/storage.pkg")
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(x.e.fx.store, "speicher/inbox/pkg", old.pin.Name+"-"+old.pin.Commit+".pkg")
	if err = storagepins.WriteFile(oldPath, oldBytes, storagepins.MaxPackageBytes); err != nil {
		t.Fatal(err)
	}
	r, _ := x.e.m.run(context.Background(), old, []byte(`{}`), testRunToken, contract.RunLimits{WallS: 1, OutputBytes: 1048576, ToolCalls: 50})
	if r.End != contract.EndRefused || r.Detail != contract.DetailState {
		t.Fatalf("cached old commit authorized: %+v", r)
	}
	assertNoSandboxJob(t, g)
}

func renewStoragePackage(t *testing.T, pkg []byte, pin storagepins.Pin) ([]byte, string) {
	t.Helper()
	objects, err := gitobjects.Objects(pkg, pin.Commit, pin.Path)
	if err != nil {
		t.Fatal(err)
	}
	old := objects[pin.Commit]
	data := append(append([]byte(nil), old.Data...), []byte("renewal\n")...)
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "commit %d\x00", len(data))
	_, _ = h.Write(data)
	commit := hex.EncodeToString(h.Sum(nil))
	delete(objects, pin.Commit)
	objects[commit] = gitobjects.Object{Type: "commit", Data: data}
	var header map[string]any
	if err = json.Unmarshal(pkg[4:4+binary.BigEndian.Uint32(pkg)], &header); err != nil {
		t.Fatal(err)
	}
	header["commit"] = commit
	raw, _ := json.Marshal(header)
	var out bytes.Buffer
	write := func(b []byte) {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(b)))
		out.Write(size[:])
		out.Write(b)
	}
	write(raw)
	ids := make([]string, 0, len(objects))
	for id := range objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := objects[id]
		write(append([]byte{map[string]byte{"commit": 'c', "tree": 't', "blob": 'b'}[o.Type]}, o.Data...))
	}
	return out.Bytes(), commit
}

// H1: use the real sink and Unix sandbox adapter, with a private run clock
// so that background refresh cannot consume either of the two timestamps.
func TestStorageExpiryBetweenApprovalReadsRefusesRun(t *testing.T) {
	x := newStorageAdapter(t)
	x.approve()
	g := dialGuard(t, x.e.sock, "read-expiry", 2)
	p := x.cached(t)
	cfg := x.e.m.cfg
	reads := 0
	now := x.now()
	cfg.Now = func() time.Time {
		reads++
		if reads >= 2 {
			return now.Add(10*time.Minute + time.Nanosecond)
		}
		return now
	}
	m := &Mediator{cfg: cfg, store: x.e.m.store, sb: x.e.m.sb, cache: map[cacheKey]verdict{}}
	r, _ := m.run(context.Background(), p, []byte(`{}`), testRunToken, contract.RunLimits{WallS: 1, OutputBytes: 1048576, ToolCalls: 50})
	if r.End != contract.EndRefused || r.Detail != contract.DetailState || reads != 2 {
		t.Fatalf("expiry between approval reads: %+v, reads=%d", r, reads)
	}
	assertNoSandboxJob(t, g)
}
