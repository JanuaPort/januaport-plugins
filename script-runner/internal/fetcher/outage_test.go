package fetcher

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Faults interrupt real local-remote checks after an initial successful approval.
type outageGit struct {
	*localRemoteGit
	operation string
	err       error
}

func (g *outageGit) Run(ctx context.Context, env []string, args ...string) error {
	if g.operation != "" && slices.Contains(args, g.operation) {
		return g.err
	}
	return g.localRemoteGit.Run(ctx, env, args...)
}

func (g *outageGit) Output(ctx context.Context, env []string, args ...string) ([]byte, error) {
	if g.operation == "ls-remote" {
		return nil, g.err
	}
	return g.localRemoteGit.Output(ctx, env, args...)
}

func TestOutagePreservesOnlyApprovalsForSameRepository(t *testing.T) {
	r := newBranchRemote(t, "trunk")
	var bounded headOutput
	_, oversized := bounded.Write([]byte(strings.Repeat("x", 4097)))
	gitFailure := exec.Command("git", "--invalid-option").Run()
	var exit *exec.ExitError
	if !errors.As(gitFailure, &exit) || exit.ExitCode() == 1 {
		t.Fatal("expected a Git error distinct from a negative ancestry result")
	}
	for _, tc := range []struct {
		name, operation string
		err             error
	}{
		{"resolution-error", "ls-remote", errors.New("unavailable " + goodURL)},
		{"resolution-timeout", "ls-remote", context.DeadlineExceeded},
		{"resolution-oversized", "ls-remote", oversized},
		{"resolution-malformed", "ls-remote", nil},
		{"head-fetch-error", "fetch", errors.New("unavailable " + goodURL)},
		{"head-fetch-timeout", "fetch", context.DeadlineExceeded},
		{"ancestry-error", "merge-base", errors.New("cannot inspect " + goodURL)},
		{"ancestry-other-exit", "merge-base", gitFailure},
		{"ancestry-timeout", "merge-base", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, local := branchSetup(t, r, r.head)
			g := &outageGit{localRemoteGit: local}
			s.f.cfg.Git = g
			s.f.Once(context.Background())
			assertApproved(t, s, r.head)
			// The base is a valid but as yet unapproved pin; the side approval
			// belongs to another repository and must not survive this outage.
			for _, sha := range []string{r.base, r.side} {
				if err := os.WriteFile(filepath.Join(s.store, "wanted", sha), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.f.markApproved(r.side, goodURL+".other"); err != nil {
				t.Fatal(err)
			}
			var log strings.Builder
			s.f.cfg.Logger = slog.New(slog.NewTextHandler(&log, nil))
			g.operation, g.err = tc.operation, tc.err
			s.f.Once(context.Background())
			assertApproved(t, s, r.head)
			assertRejected(t, s, r.base, "fetch_failed")
			assertRejected(t, s, r.side, "fetch_failed")
			if !strings.Contains(log.String(), "level=WARN") || strings.Contains(log.String(), "git@") || strings.Contains(log.String(), goodURL) {
				t.Fatalf("missing or unsafe outage warning: %s", log.String())
			}
		})
	}
}

func TestOutageDoesNotRestoreRevokedApproval(t *testing.T) {
	r := newBranchRemote(t, "main")
	s, g := branchSetup(t, r, r.head)
	s.f.Once(context.Background())
	assertApproved(t, s, r.head)
	testGit(t, r.work, "--git-dir="+r.bare, "update-ref", "refs/heads/main", r.base)
	s.f.Once(context.Background())
	assertRejected(t, s, r.head, "not_on_default_branch")
	g.failLS = true
	s.f.Once(context.Background())
	// A later outage cannot restore the revoked pin.
	if _, err := os.Stat(filepath.Join(s.store, "approved", r.head)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revoked approval restored: %v", err)
	}
}

func TestRepositoryChangeClearsApprovalsEvenDuringOutage(t *testing.T) {
	r := newBranchRemote(t, "main")
	s, g := branchSetup(t, r, r.head)
	s.f.Once(context.Background())
	assertApproved(t, s, r.head)
	// Change to a different valid origin without modifying the remote adapter.
	if err := os.WriteFile(filepath.Join(s.store, "repo_url"), []byte(goodURL+".other"), 0o644); err != nil {
		t.Fatal(err)
	}
	g.failLS = true
	s.f.Once(context.Background())
	assertRejected(t, s, r.head, "fetch_failed")
}
