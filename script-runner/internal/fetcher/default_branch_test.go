package fetcher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// localRemoteGit substitutes only the already validated origin URL. The
// file transport exception exists solely in this test adapter.
type localRemoteGit struct {
	remote      string
	failLS      bool
	lsText      string
	beforeFetch func()
	calls       [][]string
}

func (g *localRemoteGit) command(ctx context.Context, env []string, args ...string) *exec.Cmd {
	g.calls = append(g.calls, slices.Clone(args))
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

func (g *localRemoteGit) Run(ctx context.Context, env []string, args ...string) error {
	if slices.Contains(args, "fetch") && g.beforeFetch != nil {
		fn := g.beforeFetch
		g.beforeFetch = nil
		fn()
	}
	return g.command(ctx, env, args...).Run()
}

func (g *localRemoteGit) Output(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd := g.command(ctx, env, args...)
	if g.failLS {
		return nil, errors.New("ls-remote failed")
	}
	if g.lsText != "" {
		return []byte(g.lsText), nil
	}
	return cmd.Output()
}

type branchRemote struct {
	work, bare, base, head, side, pr string
}

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newBranchRemote(t *testing.T, branch string) branchRemote {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("SEC L3 tests require git")
	}
	r := branchRemote{work: t.TempDir(), bare: filepath.Join(t.TempDir(), "remote.git")}
	testGit(t, r.work, "init", "-q", "-b", branch)
	testGit(t, r.work, "config", "user.name", "Test")
	testGit(t, r.work, "config", "user.email", "test@example.com")
	commit := func(text string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(r.work, "script.py"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		testGit(t, r.work, "add", "script.py")
		testGit(t, r.work, "commit", "-q", "-m", text)
		return testGit(t, r.work, "rev-parse", "HEAD")
	}
	r.base = commit("base")
	r.head = commit("default")
	testGit(t, r.work, "checkout", "-q", "-b", "side", r.base)
	r.side = commit("side")
	testGit(t, r.work, "checkout", "-q", "--detach", r.base)
	r.pr = commit("PR only")
	testGit(t, r.work, "update-ref", "refs/pull/1/head", r.pr)
	testGit(t, r.work, "checkout", "-q", branch)
	testGit(t, r.work, "clone", "-q", "--bare", r.work, r.bare)
	testGit(t, r.work, "--git-dir="+r.bare, "fetch", "-q", "--no-tags", r.work, "refs/pull/1/head:refs/pull/1/head")
	return r
}

func branchSetup(t *testing.T, r branchRemote, commits ...string) (*setup, *localRemoteGit) {
	t.Helper()
	s := newSetup(t, goodURL, commits...)
	g := &localRemoteGit{remote: r.bare}
	s.f.cfg.Git = g
	return s, g
}

func assertApproved(t *testing.T, s *setup, sha string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.store, "approved", sha))
	if err != nil || string(b) != goodURL {
		t.Fatalf("commit %s not approved for origin: %q, %v", sha, b, err)
	}
	if _, err := os.Stat(filepath.Join(s.store, "failed", sha)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("approved commit has failure marker: %v", err)
	}
}

func assertRejected(t *testing.T, s *setup, sha, reason string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.store, "failed", sha))
	if err != nil || string(b) != reason {
		t.Fatalf("commit %s: failure = %q, %v; want %s", sha, b, err, reason)
	}
	if _, err := os.Stat(filepath.Join(s.store, "approved", sha)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected commit remains approved: %v", err)
	}
}

func TestDefaultBranchAncestryRealRemote(t *testing.T) {
	for _, branch := range []string{"main", "trunk"} {
		t.Run(branch, func(t *testing.T) {
			r := newBranchRemote(t, branch)
			s, _ := branchSetup(t, r, r.base, r.head, r.side, r.pr)
			s.f.Once(context.Background())
			assertApproved(t, s, r.base)
			assertApproved(t, s, r.head)
			assertRejected(t, s, r.side, "not_on_default_branch")
			assertRejected(t, s, r.pr, "not_on_default_branch")
		})
	}
}

func TestDefaultBranchResolutionFailsClosed(t *testing.T) {
	r := newBranchRemote(t, "main")
	s, g := branchSetup(t, r, r.head)
	g.failLS = true
	s.f.Once(context.Background())
	assertRejected(t, s, r.head, "fetch_failed")
	for _, call := range g.calls {
		if slices.Contains(call, "fetch") {
			t.Fatal("fetched despite failed ls-remote")
		}
	}
	g.failLS = false
	s.f.Once(context.Background())
	assertApproved(t, s, r.head)
	g.failLS = true
	s.f.Once(context.Background())
	assertRejected(t, s, r.head, "fetch_failed")
}

func TestDefaultBranchMissingOrMalformedHEADFailsClosed(t *testing.T) {
	r := newBranchRemote(t, "main")
	for _, response := range []string{
		r.head + "\tHEAD\n", // Detached remote HEAD is not a default branch.
		"ref: refs/pull/1/head\tHEAD\n" + r.pr + "\tHEAD\n",
		"ref: refs/heads/main\tHEAD\nnot-a-hash\tHEAD\n",
		"ref: refs/heads/main\tHEAD\n" + r.head + "\tHEAD\n" + r.side + "\tHEAD\n",
		"ref: refs/heads/main\tHEAD\nref: refs/heads/side\tHEAD\n" + r.head + "\tHEAD\n",
	} {
		t.Run(response, func(t *testing.T) {
			s, g := branchSetup(t, r, r.head)
			g.lsText = response
			s.f.Once(context.Background())
			assertRejected(t, s, r.head, "fetch_failed")
		})
	}
}

func TestDefaultBranchHeadFetchFailsClosed(t *testing.T) {
	r := newBranchRemote(t, "main")
	s, g := branchSetup(t, r, r.base)
	g.beforeFetch = func() {
		testGit(t, r.work, "--git-dir="+r.bare, "config", "remote.origin.url", "irrelevant")
		// Removing the remote after discovery makes the actual head fetch fail.
		if err := os.Rename(r.bare, r.bare+".offline"); err != nil {
			t.Fatal(err)
		}
	}
	s.f.Once(context.Background())
	assertRejected(t, s, r.base, "fetch_failed")
}

func TestForceRewriteRevokesStoredCommitAndCanRecover(t *testing.T) {
	r := newBranchRemote(t, "main")
	s, _ := branchSetup(t, r, r.head)
	s.f.Once(context.Background())
	assertApproved(t, s, r.head)
	testGit(t, r.work, "--git-dir="+r.bare, "update-ref", "refs/heads/main", r.base)
	s.f.Once(context.Background())
	assertRejected(t, s, r.head, "not_on_default_branch")
	if !s.f.hasCommit(context.Background(), r.head) {
		t.Fatal("test must exercise an object still present in the store")
	}
	testGit(t, r.work, "--git-dir="+r.bare, "update-ref", "refs/heads/main", r.head)
	s.f.Once(context.Background())
	assertApproved(t, s, r.head)
}

func TestLegacyStoredCommitRequiresAncestryCheck(t *testing.T) {
	r := newBranchRemote(t, "main")
	s, _ := branchSetup(t, r, r.side)
	if err := s.f.initStore(context.Background(), goodURL, r.side); err != nil {
		t.Fatal(err)
	}
	if err := s.f.fetch(context.Background(), r.side); err != nil {
		t.Fatal(err)
	}
	s.f.Once(context.Background())
	assertRejected(t, s, r.side, "not_on_default_branch")
}

func TestNewGitCallsHaveLiteralHardeningAndLSUsesDeployKey(t *testing.T) {
	s := newSetup(t, goodURL, sha40)
	s.f.Once(context.Background())
	seen := map[string]bool{}
	for i, call := range s.git.calls {
		for _, name := range []string{"ls-remote", "fetch", "merge-base"} {
			if !slices.Contains(call, name) {
				continue
			}
			seen[name] = true
			for _, config := range []string{
				"protocol.allow=never", "protocol.ssh.allow=always",
				"transfer.fsckObjects=true", "fetch.fsckObjects=true",
				"core.hooksPath=/dev/null", "submodule.recurse=false", "fetch.recurseSubmodules=false",
			} {
				j := slices.Index(call, config)
				if j < 1 || call[j-1] != "-c" {
					t.Errorf("%s lacks literal -c %s: %v", name, config, call)
				}
			}
			if name == "ls-remote" {
				if !slices.Equal(call[len(call)-4:], []string{"ls-remote", "--symref", "origin", "HEAD"}) {
					t.Errorf("default branch not freshly resolved through origin HEAD: %v", call)
				}
				env := strings.Join(s.git.envs[i], "\n")
				for _, option := range []string{"GIT_SSH_COMMAND=ssh -i ", "IdentitiesOnly=yes", "BatchMode=yes", "StrictHostKeyChecking=accept-new", "UserKnownHostsFile="} {
					if !strings.Contains(env, option) {
						t.Errorf("ls-remote lacks deploy-key option %s", option)
					}
				}
			}
		}
	}
	for _, name := range []string{"ls-remote", "fetch", "merge-base"} {
		if !seen[name] {
			t.Errorf("missing %s call", name)
		}
	}
}
