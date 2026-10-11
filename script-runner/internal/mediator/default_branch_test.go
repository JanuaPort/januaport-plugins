package mediator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/fetcher"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// Local transport is permitted only in this test adapter.
type branchTestGit struct{ remote string }

func (g branchTestGit) cmd(ctx context.Context, env []string, args ...string) *exec.Cmd {
	args = slices.Clone(args)
	for i, arg := range args {
		if arg == "remote.origin.url" && i+1 < len(args) {
			args[i+1] = g.remote
		}
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "protocol.file.allow=always"}, args...)...)
	cmd.Env = env
	return cmd
}

func (g branchTestGit) Run(ctx context.Context, env []string, args ...string) error {
	return g.cmd(ctx, env, args...).Run()
}

func (g branchTestGit) Output(ctx context.Context, env []string, args ...string) ([]byte, error) {
	return g.cmd(ctx, env, args...).Output()
}

func waitPinState(t *testing.T, e *env, name, state, reason string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p, _, found := e.m.findPin(name)
		if found && p.state == state && p.reason == reason {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	p, _, _ := e.m.findPin(name)
	t.Fatalf("pin %s = %s/%s; want %s/%s", name, p.state, p.reason, state, reason)
}

func assertNoSandboxJob(t *testing.T, g *fakeGuard) {
	t.Helper()
	_ = g.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if b, err := frame.Read(g.conn, 1<<20); err == nil {
		t.Fatalf("rejected commit reached sandbox: %s", b)
	}
}

// SEC L3: a PR SHA requested through the gateway must never be executable,
// even after fetching its object into store.git. The remote advertises it
// only under refs/pull/1/head, while trunk still points to reviewed code.
func TestPRCommitNeverExecutable(t *testing.T) {
	e := newEnv(t, nil)
	f := e.fx
	f.git(f.work, "checkout", "-q", "--detach", f.commit)
	f.write("quick/main.py", "print('unreviewed PR')\n")
	f.git(f.work, "add", "quick/main.py")
	f.git(f.work, "commit", "-q", "-m", "PR only")
	pr := f.git(f.work, "rev-parse", "HEAD")
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.git(f.work, "clone", "-q", "--bare", f.work, remote)
	f.git(f.work, "--git-dir="+remote, "update-ref", "refs/heads/trunk", f.commit)
	f.git(f.work, "--git-dir="+remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
	f.git(f.work, "--git-dir="+remote, "update-ref", "refs/pull/1/head", pr)
	key := filepath.Join(t.TempDir(), "runtime-key")
	if err := os.WriteFile(key, []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.writePins([]pinRow{{Name: "quick", Path: "quick", Commit: pr}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(f.store, "wanted", pr)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mediator did not request PR commit")
		}
		time.Sleep(20 * time.Millisecond)
	}
	abholer := fetcher.New(fetcher.Config{
		StoreDir: f.store, KeyPath: key, TmpDir: t.TempDir(), Git: branchTestGit{remote: remote},
	})
	abholer.Once(context.Background())
	g := dialGuard(t, e.sock, "i1", 2)
	waitPinState(t, e, "quick", contract.StateInvalid, "not_on_default_branch")
	listed := e.listTools()
	if len(listed.Tools) != 0 || !strings.Contains(mustJSON(listed.Meta), `"reason":"not_on_default_branch"`) {
		t.Fatalf("PR pin exposed as a tool or missing its reason: %+v", listed)
	}
	res, err := e.call("quick", map[string]any{}, pr)
	if err != nil {
		t.Fatal(err)
	}
	assertEnd(t, res, contract.EndRefused, contract.DetailState)
	assertNoSandboxJob(t, g)
}

func TestLegacyStoredObjectWithoutApprovalIsPending(t *testing.T) {
	e := newEnv(t, nil)
	if err := os.Remove(filepath.Join(e.fx.store, "approved", e.fx.commit)); err != nil {
		t.Fatal(err)
	}
	e.writePins(e.quickPin())
	g := dialGuard(t, e.sock, "i1", 2)
	waitPinState(t, e, "quick", contract.StatePending, "")
	assertEnd(t, e.mustCall("quick", map[string]any{}), contract.EndRefused, contract.DetailState)
	assertNoSandboxJob(t, g)
}

func TestFailureRevokesCachedReadyPin(t *testing.T) {
	for _, reason := range []string{"not_on_default_branch", "fetch_failed"} {
		t.Run(reason, func(t *testing.T) {
			e := newEnv(t, nil)
			e.writePins(e.quickPin())
			g := dialGuard(t, e.sock, "i1", 2)
			e.waitReady("quick")
			if err := os.MkdirAll(filepath.Join(e.fx.store, "failed"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(e.fx.store, "failed", e.fx.commit), []byte(reason), 0o644); err != nil {
				t.Fatal(err)
			}
			waitPinState(t, e, "quick", contract.StateInvalid, reason)
			assertEnd(t, e.mustCall("quick", map[string]any{}), contract.EndRefused, contract.DetailState)
			assertNoSandboxJob(t, g)
		})
	}
}

func TestCachedPinCannotRunAfterApprovalRemoved(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	g := dialGuard(t, e.sock, "i1", 2)
	e.waitReady("quick")
	if err := os.Remove(filepath.Join(e.fx.store, "approved", e.fx.commit)); err != nil {
		t.Fatal(err)
	}
	// Freeze the listed state at ready to exercise the per-run check directly.
	e.m.mu.Lock()
	var p pinState
	for _, candidate := range e.m.pins {
		if candidate.pin.Name == "quick" {
			p = candidate
		}
	}
	e.m.mu.Unlock()
	p.state = contract.StateReady
	result, _ := e.m.run(context.Background(), p, []byte(`{}`), testRunToken)
	if result.End != contract.EndRefused || result.Detail != contract.DetailState {
		t.Fatalf("cached pin ran after approval removal: %+v", result)
	}
	assertNoSandboxJob(t, g)
}

func TestApprovalForDifferentRepositoryIsPending(t *testing.T) {
	e := newEnv(t, nil)
	if err := os.WriteFile(filepath.Join(e.fx.store, "approved", e.fx.commit), []byte("ssh://git@git.example.com/org/old.git"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.writePins(e.quickPin())
	_ = dialGuard(t, e.sock, "i1", 2)
	waitPinState(t, e, "quick", contract.StatePending, "")
}
