// Package fetcher ist der abholer: Nur Vorfahren des frisch aufgelösten
// Remote-Standard-Zweigkopfs werden freigegeben (SEC L3, #1083). Er holt Objekte
// per ssh in den Bare-Store (Vertrag §8, Nachtrag 2a). Er vertraut dem
// vermittler nicht: jede Zeile aus wanted/ und die repo_url prüft er selbst
// (V2, M7). Der Git-Anbieter wird nur mit ls-remote und fetch gelesen.
package fetcher

import (
	"bytes"
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

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

// Runner führt git mit genau dieser Umgebung aus.
type Runner interface {
	Run(ctx context.Context, env []string, args ...string) error
	Output(ctx context.Context, env []string, args ...string) ([]byte, error)
}

// ExecGit ruft das git des Images. Stderr ist nur für Tests; im Betrieb
// bleibt die Ausgabe von git ungelesen (sie kann Pfade und URLs tragen).
type ExecGit struct{ Stderr io.Writer }

// Run implementiert Runner.
func (g ExecGit) Run(ctx context.Context, env []string, args ...string) error {
	return g.command(ctx, env, args...).Run()
}

func (g ExecGit) command(ctx context.Context, env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	cmd.Stderr = g.Stderr
	return cmd
}

// Output liest ausschließlich die begrenzte HEAD-Antwort von ls-remote.
func (g ExecGit) Output(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd := g.command(ctx, env, args...)
	var out headOutput
	cmd.Stdout = &out
	err := cmd.Run()
	return out.buf.Bytes(), err
}

type headOutput struct{ buf bytes.Buffer }

func (b *headOutput) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > 4096 {
		return 0, errors.New("fetcher: HEAD-Antwort zu groß")
	}
	return b.buf.Write(p)
}

// Config des abholers.
type Config struct {
	StoreDir   string        // gemeinsames Volume: wanted/, repo_url, store.git, approved/, failed/
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
			f.markFailed(sha, contract.ReasonFetchFailed)
		}
		return
	}
	if err := f.ensureStore(ctx, url, wanted[0]); err != nil {
		f.cfg.Logger.Warn("Store nicht bereit")
		f.failAll(wanted)
		return
	}
	key, err := f.copyKey()
	if err != nil {
		f.failAll(wanted)
		return
	}
	defer func() { _ = os.Remove(key) }()
	// Auch gespeicherte und zuletzt abgewiesene Pins werden frisch geprüft.
	// Der Fetch holt exakt den angekündigten Hash, selbst wenn HEAD danach
	// schon weitergezogen ist.
	head, err := f.defaultHead(ctx, key)
	if err != nil || f.fetchWithKey(ctx, head, key) != nil {
		f.failAll(wanted)
		return
	}
	var fetched, failed int
	for _, sha := range wanted {
		if sha != head && !f.hasCommit(ctx, sha) {
			if f.recentlyFailed(sha) {
				continue
			}
			if err := f.fetchWithKey(ctx, sha, key); err != nil {
				f.markFailed(sha, contract.ReasonFetchFailed)
				failed++
				continue
			}
		}
		if err := f.isAncestor(ctx, sha, head); err != nil {
			reason := contract.ReasonFetchFailed
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				reason = contract.ReasonNotOnDefaultBranch
			}
			f.markFailed(sha, reason)
			failed++
			continue
		}
		if err := f.markApproved(sha, url); err != nil {
			f.markFailed(sha, contract.ReasonFetchFailed)
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
	for _, p := range []string{filepath.Join(f.cfg.StoreDir, "approved"), f.gitDir(), filepath.Join(f.cfg.StoreDir, "failed"), filepath.Join(f.cfg.StoreDir, "store.url")} {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	format := "sha1"
	if len(sha) == 64 {
		format = "sha256"
	}
	env := f.env("")
	if err := f.cfg.Git.Run(ctx, env, append(append([]string{}, hardening...), "init", "-q", "--bare", "--object-format="+format, f.gitDir())...); err != nil {
		return err
	}
	if err := f.cfg.Git.Run(ctx, env, f.gitArgs("config", "remote.origin.url", url)...); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.cfg.StoreDir, "store.url"), []byte(url), 0o600)
}

func (f *Fetcher) hasCommit(ctx context.Context, sha string) bool {
	return f.cfg.Git.Run(ctx, f.env(""), f.gitArgs("cat-file", "-e", sha+"^{commit}")...) == nil
}

func (f *Fetcher) recentlyFailed(sha string) bool {
	st, err := os.Stat(filepath.Join(f.cfg.StoreDir, "failed", sha))
	return err == nil && time.Since(st.ModTime()) < f.cfg.RetryAfter
}

func (f *Fetcher) failAll(wanted []string) {
	for _, sha := range wanted {
		f.markFailed(sha, contract.ReasonFetchFailed)
	}
}

func (f *Fetcher) markFailed(sha, reason string) {
	_ = os.Remove(filepath.Join(f.cfg.StoreDir, "approved", sha))
	dir := filepath.Join(f.cfg.StoreDir, "failed")
	if err := writeMarker(dir, sha, reason); err != nil {
		f.cfg.Logger.Warn("Fehlerzustand nicht gespeichert")
	}
}

// Freigaben sind an die geprüfte repo_url gebunden. Vorhandene Objekte
// allein reichen dem vermittler nicht; alte Stores starten ohne Freigabe.
func (f *Fetcher) markApproved(sha, url string) error {
	return writeMarker(filepath.Join(f.cfg.StoreDir, "approved"), sha, url)
}

func writeMarker(dir, sha, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, sha)
	if err := os.WriteFile(path+".tmp", []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// hardening gilt für jeden Git-Aufruf: nur ssh, jedes Objekt geprüft, keine
// Hooks, keine Submodule. Der Store ist bare; es gibt keinen Checkout und
// damit weder LFS-Filter noch Arbeitsbaum.
// Diese Liste wird auch allen lokalen Prüfungen und ls-remote mitgegeben.
var hardening = []string{
	"-c", "protocol.allow=never",
	"-c", "protocol.ssh.allow=always",
	"-c", "transfer.fsckObjects=true",
	"-c", "fetch.fsckObjects=true",
	"-c", "core.hooksPath=/dev/null",
	"-c", "submodule.recurse=false",
	"-c", "fetch.recurseSubmodules=false",
}

func (f *Fetcher) gitArgs(args ...string) []string {
	return append(append(append([]string{}, hardening...), "--git-dir="+f.gitDir()), args...)
}

func (f *Fetcher) defaultHead(ctx context.Context, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	b, err := f.cfg.Git.Output(ctx, f.env(key), f.gitArgs("ls-remote", "--symref", "origin", "HEAD")...)
	if err != nil {
		return "", err
	}
	var branch, head string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || fields[1] != "HEAD" {
			return "", errors.New("fetcher: ungültige Remote-HEAD-Antwort")
		}
		switch {
		case strings.HasPrefix(fields[0], "ref: refs/heads/") && len(fields[0]) > len("ref: refs/heads/") && branch == "":
			branch = fields[0]
		case gitref.ValidCommit(fields[0]) && head == "":
			head = fields[0]
		default:
			return "", errors.New("fetcher: ungültige Remote-HEAD-Antwort")
		}
	}
	if branch == "" || head == "" {
		return "", errors.New("fetcher: kein Standard-Zweig")
	}
	return head, nil
}

func (f *Fetcher) isAncestor(ctx context.Context, sha, head string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return f.cfg.Git.Run(ctx, f.env(""), f.gitArgs("merge-base", "--is-ancestor", sha, head)...)
}

// fetch holt ein Objekt per Hash. Ausführbar wird es erst nach dem
// Vorfahr-Nachweis gegen den frisch aufgelösten Standard-Zweigkopf (L3).
func (f *Fetcher) fetch(ctx context.Context, sha string) error {
	if !gitref.ValidCommit(sha) {
		return errors.New("fetcher: ungültiger Commit")
	}
	key, err := f.copyKey()
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(key) }()
	return f.fetchWithKey(ctx, sha, key)
}

func (f *Fetcher) fetchWithKey(ctx context.Context, sha, key string) error {
	if !gitref.ValidCommit(sha) {
		return errors.New("fetcher: ungültiger Commit")
	}
	args := f.gitArgs("fetch", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", "origin", "--", sha)
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
