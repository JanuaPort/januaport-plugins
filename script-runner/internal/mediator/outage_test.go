package mediator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/fetcher"
)

type interruptedBranchGit struct {
	branchTestGit
	operation string
}

func (g *interruptedBranchGit) Run(ctx context.Context, env []string, args ...string) error {
	if g.operation != "" && slices.Contains(args, g.operation) {
		return errors.New("git unavailable")
	}
	return g.branchTestGit.Run(ctx, env, args...)
}

func (g *interruptedBranchGit) Output(ctx context.Context, env []string, args ...string) ([]byte, error) {
	if g.operation == "ls-remote" {
		return nil, errors.New("git unavailable")
	}
	return g.branchTestGit.Output(ctx, env, args...)
}

func TestApprovedPinsRemainReadyDuringGitOutage(t *testing.T) {
	for _, operation := range []string{"ls-remote", "fetch", "merge-base"} {
		t.Run(operation, func(t *testing.T) {
			e := newEnv(t, nil)
			remote := filepath.Join(t.TempDir(), "remote.git")
			e.fx.git(e.fx.work, "clone", "-q", "--bare", e.fx.work, remote)
			e.fx.git(e.fx.work, "--git-dir="+remote, "update-ref", "refs/heads/trunk", e.fx.commit)
			e.fx.git(e.fx.work, "--git-dir="+remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
			key := filepath.Join(t.TempDir(), "runtime-key")
			if err := os.WriteFile(key, []byte("test-key"), 0o600); err != nil {
				t.Fatal(err)
			}
			e.writePins(e.quickPin())
			_ = dialGuard(t, e.sock, "i1", 2)
			e.waitReady("quick")
			g := &interruptedBranchGit{branchTestGit: branchTestGit{remote: remote}}
			abholer := fetcher.New(fetcher.Config{StoreDir: e.fx.store, KeyPath: key, TmpDir: t.TempDir(), Git: g})
			abholer.Once(context.Background())
			e.waitReady("quick")
			g.operation = operation
			abholer.Once(context.Background())
			// Evaluate synchronously as well, so stale polling state cannot pass.
			if v := e.m.fetchVerdict(e.fx.commit); v.state != contract.StateReady || v.reason != "" {
				t.Fatalf("approved pin lost during %s outage: %s/%s", operation, v.state, v.reason)
			}
			waitPinState(t, e, "quick", contract.StateReady, "")
			e.waitReady("quick")
		})
	}
}
