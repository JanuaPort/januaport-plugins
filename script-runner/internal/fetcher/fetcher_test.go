package fetcher

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const goodURL = "ssh://git@git.example.com/org/scripts.git"

var (
	sha40 = strings.Repeat("a", 40)
	sha64 = strings.Repeat("b", 64)
)

// fakeGit hält jeden git-Aufruf fest. cat-file meldet „fehlt“, damit der
// abholer holen will; alles andere gelingt, außer failFetch ist gesetzt.
type fakeGit struct {
	mu        sync.Mutex
	calls     [][]string
	envs      [][]string
	keyBody   []string
	failFetch bool
}

func (g *fakeGit) Run(_ context.Context, env []string, args ...string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, args)
	g.envs = append(g.envs, env)
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			if m := regexp.MustCompile(`-i (\S+)`).FindStringSubmatch(e); m != nil {
				b, _ := os.ReadFile(m[1])
				g.keyBody = append(g.keyBody, string(b))
			}
		}
	}
	if contains(args, "cat-file") {
		return errors.New("missing")
	}
	if contains(args, "fetch") && g.failFetch {
		return errors.New("fetch failed")
	}
	return nil
}

func (g *fakeGit) fetches() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out [][]string
	for _, c := range g.calls {
		if contains(c, "fetch") {
			out = append(out, c)
		}
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

type setup struct {
	store string
	key   string
	git   *fakeGit
	f     *Fetcher
}

func newSetup(t *testing.T, url string, wanted ...string) *setup {
	t.Helper()
	s := &setup{store: t.TempDir(), git: &fakeGit{}}
	keyDir := t.TempDir()
	s.key = filepath.Join(keyDir, "runtime-key")
	if err := os.WriteFile(s.key, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nnot-a-real-key\n-----END OPENSSH PRIVATE KEY-----"), 0o640); err != nil {
		t.Fatal(err)
	}
	if url != "" {
		_ = os.WriteFile(filepath.Join(s.store, "repo_url"), []byte(url+"\n"), 0o644)
	}
	_ = os.MkdirAll(filepath.Join(s.store, "wanted"), 0o755)
	for _, w := range wanted {
		if err := os.WriteFile(filepath.Join(s.store, "wanted", w), nil, 0o644); err != nil {
			t.Fatalf("wanted/%q: %v", w, err)
		}
	}
	s.f = New(Config{StoreDir: s.store, KeyPath: s.key, TmpDir: t.TempDir(), RetryAfter: time.Hour, Git: s.git})
	return s
}

// V2 (SEC): jede wanted-Zeile prüft der abholer selbst — ungültige Zeilen
// führen zu keinem git-Aufruf.
func TestWantedLinesTable(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		fetch bool
	}{
		{"40 Hex", sha40, true},
		{"64 Hex", sha64, true},
		{"--upload-pack", "--upload-pack=x", false},
		{"-oProxyCommand", "-oProxyCommand=x", false},
		{"kurzer Hash", "abc1234", false},
		{"Branch-Name", "main", false},
		{"41 Hex", sha40 + "a", false},
		{"Großbuchstaben", strings.ToUpper(sha40), false},
		{"Zeilenumbruch", sha40[:20] + "\n" + sha40[:19], false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSetup(t, goodURL, tt.entry)
			s.f.Once(context.Background())
			got := s.git.fetches()
			if tt.fetch {
				if len(got) != 1 {
					t.Fatalf("%d fetch-Aufrufe, want 1", len(got))
				}
				if n := len(got[0]); got[0][n-3] != "origin" || got[0][n-2] != "--" || got[0][n-1] != tt.entry {
					t.Fatalf("fetch-Argumente enden nicht auf origin -- <sha>: %v", got[0])
				}
				return
			}
			if len(s.git.calls) != 0 {
				t.Fatalf("git trotz ungültiger Zeile gerufen: %v", s.git.calls)
			}
			if s.f.Rejected() == 0 {
				t.Fatal("verworfene Zeile nicht gezählt")
			}
		})
	}
}

// M7 im abholer: die URL wird vor jedem Fetch erneut geprüft.
func TestBadRepoURLNoGitCallAndFailedMarker(t *testing.T) {
	for _, url := range []string{"", "file:///srv/repo.git", "ext::sh -c x", "-oProxyCommand=x", "https://git.example.com/r.git"} {
		t.Run(url, func(t *testing.T) {
			s := newSetup(t, url, sha40)
			s.f.Once(context.Background())
			if len(s.git.calls) != 0 {
				t.Fatalf("git gerufen: %v", s.git.calls)
			}
			if _, err := os.Stat(filepath.Join(s.store, "failed", sha40)); err != nil {
				t.Fatalf("failed/%s fehlt: %v", sha40, err)
			}
		})
	}
}

func TestFetchArgsHardening(t *testing.T) {
	s := newSetup(t, goodURL, sha40)
	s.f.Once(context.Background())
	f := s.git.fetches()
	if len(f) != 1 {
		t.Fatalf("%d fetches", len(f))
	}
	joined := strings.Join(f[0], " ")
	for _, want := range []string{
		"protocol.allow=never", "protocol.ssh.allow=always", "transfer.fsckObjects=true",
		"core.hooksPath=/dev/null", "submodule.recurse=false", "--no-recurse-submodules", "--no-tags",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("fetch ohne %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "not-a-real-key") {
		t.Fatal("Schlüssel in den Argumenten")
	}
}

func TestDeployKeyCopiedWithNewlineAndRemoved(t *testing.T) {
	s := newSetup(t, goodURL, sha40)
	s.f.Once(context.Background())
	if len(s.git.keyBody) == 0 {
		t.Fatal("kein GIT_SSH_COMMAND mit -i")
	}
	if !strings.HasSuffix(s.git.keyBody[0], "-----END OPENSSH PRIVATE KEY-----\n") {
		t.Fatalf("Schlüsselkopie ohne abschließenden Zeilenumbruch: %q", s.git.keyBody[0])
	}
	var ssh string
	for _, e := range s.git.envs[len(s.git.envs)-1] {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			ssh = e
		}
	}
	for _, want := range []string{"IdentitiesOnly=yes", "-F /dev/null", "BatchMode=yes"} {
		if !strings.Contains(ssh, want) {
			t.Errorf("GIT_SSH_COMMAND ohne %q: %s", want, ssh)
		}
	}
	entries, _ := os.ReadDir(s.f.cfg.TmpDir)
	if len(entries) != 0 {
		t.Fatalf("Schlüsselkopie liegen geblieben: %v", entries)
	}
}

func TestFetchFailureWritesMarkerAndWaitsBeforeRetry(t *testing.T) {
	s := newSetup(t, goodURL, sha40)
	s.git.failFetch = true
	s.f.Once(context.Background())
	if _, err := os.Stat(filepath.Join(s.store, "failed", sha40)); err != nil {
		t.Fatalf("failed-Marke fehlt: %v", err)
	}
	s.f.Once(context.Background())
	if n := len(s.git.fetches()); n != 1 {
		t.Fatalf("%d fetches, want 1 (RetryAfter nicht abgelaufen)", n)
	}
}

// Nachtrag 2a: ändert sich repo_url, leert der Läufer den Store.
func TestRepoURLChangeWipesStore(t *testing.T) {
	s := newSetup(t, goodURL, sha40)
	s.f.Once(context.Background())
	marker := filepath.Join(s.store, "store.git", "objects", "old")
	_ = os.MkdirAll(filepath.Dir(marker), 0o755)
	_ = os.WriteFile(marker, []byte("x"), 0o644)
	_ = os.MkdirAll(filepath.Join(s.store, "failed"), 0o755)
	_ = os.WriteFile(filepath.Join(s.store, "failed", sha64), nil, 0o644)

	_ = os.WriteFile(filepath.Join(s.store, "repo_url"), []byte("ssh://git@git.example.com/org/other.git\n"), 0o644)
	s.f.Once(context.Background())
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("alter Store nicht geleert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.store, "failed", sha64)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("failed/ nicht geleert")
	}
}

func TestObjectFormatFollowsHashLength(t *testing.T) {
	for _, tt := range []struct{ sha, format string }{{sha40, "sha1"}, {sha64, "sha256"}} {
		s := newSetup(t, goodURL, tt.sha)
		s.f.Once(context.Background())
		found := false
		for _, c := range s.git.calls {
			if contains(c, "init") && contains(c, "--object-format="+tt.format) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: kein init mit --object-format=%s: %v", tt.sha[:4], tt.format, s.git.calls)
		}
	}
}

// SEC-Zeile 2: git fetch mit file:/// scheitert im abholer — auch wenn der
// URL-Prüfer umgangen würde, weil protocol.allow=never gilt.
func TestRealGitRefusesFileProtocol(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git fehlt")
	}
	src := t.TempDir()
	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(src, "init", "-q", ".")
	_ = os.WriteFile(filepath.Join(src, "a"), []byte("a"), 0o644)
	run(src, "add", "a")
	run(src, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "c")
	sha := run(src, "rev-parse", "HEAD")

	store := t.TempDir()
	key := filepath.Join(t.TempDir(), "runtime-key")
	_ = os.WriteFile(key, []byte("dummy"), 0o600)
	var stderr strings.Builder
	f := New(Config{StoreDir: store, KeyPath: key, TmpDir: t.TempDir(), Git: ExecGit{Stderr: &stderr}})
	if err := f.initStore(context.Background(), "file://"+src, sha); err != nil {
		t.Fatalf("initStore: %v", err)
	}
	if err := f.fetch(context.Background(), sha); err == nil {
		t.Fatal("fetch über file:// gelungen")
	}
	// Der Grund ist die Protokoll-Sperre, nicht ein anderer Fehler.
	if !strings.Contains(stderr.String(), "transport 'file' not allowed") {
		t.Fatalf("git scheiterte aus anderem Grund:\n%s", stderr.String())
	}
	has := exec.Command("git", "--git-dir", filepath.Join(store, "store.git"), "cat-file", "-e", sha+"^{commit}")
	if has.Run() == nil {
		t.Fatal("Commit trotzdem im Store")
	}
}

// R8: kein Push-Pfad im Code. Der Wächter liest alle Go-Quellen des Moduls.
func TestNoPushPathInSource(t *testing.T) {
	root := "../.."
	bad := regexp.MustCompile(`(?i)\bpush\b|receive-pack|send-pack`)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if loc := bad.FindIndex(b); loc != nil {
			t.Errorf("%s enthält %q", path, b[loc[0]:loc[1]])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
