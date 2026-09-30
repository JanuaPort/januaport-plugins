// Package fetcher ist der abholer: Er holt genau die gewünschten Commits
// per ssh in den Bare-Store (Vertrag §8, Nachtrag 2a). Er vertraut dem
// vermittler nicht: jede Zeile aus wanted/ und die repo_url prüft er selbst
// (V2, M7). Es gibt keinen Weg zurück zum Git-Anbieter außer fetch.
package fetcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

// Runner führt git mit genau dieser Umgebung aus.
type Runner interface {
	Run(ctx context.Context, env []string, args ...string) error
}

// ExecGit ruft das git des Images. Stderr ist nur für Tests; im Betrieb
// bleibt die Ausgabe von git ungelesen (sie kann Pfade und URLs tragen).
type ExecGit struct{ Stderr io.Writer }

// Run implementiert Runner.
func (g ExecGit) Run(ctx context.Context, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	cmd.Stderr = g.Stderr
	return cmd.Run()
}

// Config des abholers.
type Config struct {
	StoreDir   string        // gemeinsames Volume: wanted/, repo_url, store.git, failed/
	KeyPath    string        // Deploy-Key (…/script-runner-git/runtime-key)
	TmpDir     string        // tmpfs für die Schlüsselkopie
	Interval   time.Duration // Abstand der Durchläufe
	RetryAfter time.Duration // Wartezeit nach einem gescheiterten Fetch
	Git        Runner
	Logger     *slog.Logger
}

// Fetcher ist der abholer.
type Fetcher struct {
	cfg      Config
	rejected atomic.Int64
}

// New setzt Standards für leere Felder.
func New(cfg Config) *Fetcher {
	if cfg.Interval == 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.RetryAfter == 0 {
		cfg.RetryAfter = 5 * time.Minute
	}
	if cfg.Git == nil {
		cfg.Git = ExecGit{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Fetcher{cfg: cfg}
}

// Rejected zählt verworfene wanted-Einträge.
func (f *Fetcher) Rejected() int64 { return f.rejected.Load() }

// Run läuft bis ctx endet.
func (f *Fetcher) Run(ctx context.Context) error {
	t := time.NewTicker(f.cfg.Interval)
	defer t.Stop()
	for {
		f.Once(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (f *Fetcher) gitDir() string { return filepath.Join(f.cfg.StoreDir, "store.git") }

// Once ist ein Durchlauf.
func (f *Fetcher) Once(ctx context.Context) {
	wanted := f.wanted()
	if len(wanted) == 0 {
		return
	}
	url := f.repoURL()
	if gitref.CheckRepoURL(url) != nil {
		f.cfg.Logger.Warn("repo_url abgewiesen", "wanted", len(wanted))
		for _, sha := range wanted {
			f.markFailed(sha)
		}
		return
	}
	if err := f.ensureStore(ctx, url, wanted[0]); err != nil {
		f.cfg.Logger.Warn("Store nicht bereit")
		return
	}
	var fetched, failed int
	for _, sha := range wanted {
		if f.hasCommit(ctx, sha) || f.recentlyFailed(sha) {
			continue
		}
		if err := f.fetch(ctx, sha); err != nil {
			f.markFailed(sha)
			failed++
			continue
		}
		_ = os.Remove(filepath.Join(f.cfg.StoreDir, "failed", sha))
		fetched++
	}
	if fetched+failed > 0 {
		f.cfg.Logger.Info("Durchlauf", "fetched", fetched, "failed", failed, "rejected_total", f.Rejected())
	}
}

// wanted liest die Einträge aus wanted/. Jeder Name wird selbst geprüft
// (V2): nur 40 oder 64 kleine Hex-Zeichen, sonst verworfen und gezählt.
func (f *Fetcher) wanted() []string {
	entries, err := os.ReadDir(filepath.Join(f.cfg.StoreDir, "wanted"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.Type().IsRegular() || !gitref.ValidCommit(e.Name()) {
			f.rejected.Add(1)
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

func (f *Fetcher) repoURL() string {
	b, err := os.ReadFile(filepath.Join(f.cfg.StoreDir, "repo_url"))
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

// ensureStore legt den Store an und leert ihn, wenn sich die repo_url
// geändert hat (Nachtrag 2a, zweite Linie).
func (f *Fetcher) ensureStore(ctx context.Context, url, firstSha string) error {
	current, err := os.ReadFile(filepath.Join(f.cfg.StoreDir, "store.url"))
	if err == nil && string(current) == url {
		return nil
	}
	return f.initStore(ctx, url, firstSha)
}

// initStore legt einen leeren Bare-Store für url an. Das Objektformat folgt
// der Hashlänge (40 → sha1, 64 → sha256).
func (f *Fetcher) initStore(ctx context.Context, url, sha string) error {
	for _, p := range []string{f.gitDir(), filepath.Join(f.cfg.StoreDir, "failed"), filepath.Join(f.cfg.StoreDir, "store.url")} {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	format := "sha1"
	if len(sha) == 64 {
		format = "sha256"
	}
	env := f.env("")
	if err := f.cfg.Git.Run(ctx, env, "init", "-q", "--bare", "--object-format="+format, f.gitDir()); err != nil {
		return err
	}
	if err := f.cfg.Git.Run(ctx, env, "--git-dir="+f.gitDir(), "config", "remote.origin.url", url); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.cfg.StoreDir, "store.url"), []byte(url), 0o600)
}

func (f *Fetcher) hasCommit(ctx context.Context, sha string) bool {
	return f.cfg.Git.Run(ctx, f.env(""), "--git-dir="+f.gitDir(), "cat-file", "-e", sha+"^{commit}") == nil
}

func (f *Fetcher) recentlyFailed(sha string) bool {
	st, err := os.Stat(filepath.Join(f.cfg.StoreDir, "failed", sha))
	return err == nil && time.Since(st.ModTime()) < f.cfg.RetryAfter
}

func (f *Fetcher) markFailed(sha string) {
	dir := filepath.Join(f.cfg.StoreDir, "failed")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, sha), nil, 0o644)
}

// hardening gilt für jeden Fetch: nur ssh, jedes Objekt geprüft, keine
// Hooks, keine Submodule. Der Store ist bare; es gibt keinen Checkout und
// damit weder LFS-Filter noch Arbeitsbaum.
var hardening = []string{
	"-c", "protocol.allow=never",
	"-c", "protocol.ssh.allow=always",
	"-c", "transfer.fsckObjects=true",
	"-c", "fetch.fsckObjects=true",
	"-c", "core.hooksPath=/dev/null",
	"-c", "submodule.recurse=false",
	"-c", "fetch.recurseSubmodules=false",
}

// fetch holt genau einen Commit: `git fetch origin -- <sha>` (V2).
func (f *Fetcher) fetch(ctx context.Context, sha string) error {
	if !gitref.ValidCommit(sha) {
		return errors.New("fetcher: ungültiger Commit")
	}
	key, err := f.copyKey()
	if err != nil {
		return err
	}
	defer os.Remove(key)
	args := append(append([]string{}, hardening...),
		"--git-dir="+f.gitDir(), "fetch", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head",
		"origin", "--", sha)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return f.cfg.Git.Run(ctx, f.env(key), args...)
}

// copyKey legt eine Kopie des Deploy-Keys mit 0600 und abschließendem
// Zeilenumbruch an. ssh verweigert sonst Schlüssel mit offenen Rechten oder
// ohne Zeilenende.
func (f *Fetcher) copyKey() (string, error) {
	b, err := os.ReadFile(f.cfg.KeyPath)
	if err != nil {
		return "", errors.New("fetcher: Deploy-Key nicht lesbar")
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	rnd := make([]byte, 8)
	_, _ = rand.Read(rnd)
	p := filepath.Join(f.cfg.TmpDir, "id-"+hex.EncodeToString(rnd))
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", err
	}
	return p, nil
}

func (f *Fetcher) env(key string) []string {
	env := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + f.cfg.TmpDir,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	}
	if key != "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -i "+key+" -F /dev/null -o IdentitiesOnly=yes -o BatchMode=yes"+
			" -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="+filepath.Join(f.cfg.StoreDir, "known_hosts"))
	}
	return env
}
