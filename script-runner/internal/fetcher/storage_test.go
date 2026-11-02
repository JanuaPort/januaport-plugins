//go:build linux

package fetcher

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/gitobjects"
	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
)

type storageFixture struct {
	t             *testing.T
	f             *Fetcher
	root, keyPath string
	clock         atomic.Int64
	key           ed25519.PrivateKey
	manifest      storagepins.Manifest
	pkg           []byte
	logs          bytes.Buffer
}

func newStorageFixture(t *testing.T) *storageFixture {
	t.Helper()
	x := &storageFixture{t: t, root: t.TempDir()}
	x.clock.Store(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC).UnixNano())
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	x.key = key
	keyDir := t.TempDir()
	x.keyPath = filepath.Join(keyDir, "runner-pins.pub")
	x.write(x.keyPath, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"))
	x.write(filepath.Join(keyDir, "runtime-key"), []byte("DEPLOY-KEY-CANARY"))
	b, e := os.ReadFile("../../contract/golden/storage/valid.json")
	if e != nil {
		t.Fatal(e)
	}
	var s storagepins.Signed
	if json.Unmarshal(b, &s) != nil || json.Unmarshal(s.Manifest, &x.manifest) != nil {
		t.Fatal("golden")
	}
	x.pkg, e = os.ReadFile("../../contract/golden/storage/valid.gitobjects")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(x.root, "speicher/inbox/pkg"), 0700); e != nil {
		t.Fatal(e)
	}
	x.f = New(Config{StoreDir: x.root, KeyPath: filepath.Join(keyDir, "runtime-key"), StorageNow: x.now, Logger: slog.New(slog.NewTextHandler(&x.logs, nil))})
	x.publish()
	return x
}
func (x *storageFixture) now() time.Time          { return time.Unix(0, x.clock.Load()).UTC() }
func (x *storageFixture) advance(d time.Duration) { x.clock.Add(int64(d)) }
func (x *storageFixture) write(path string, b []byte) {
	x.t.Helper()
	if e := os.WriteFile(path, b, 0600); e != nil {
		x.t.Fatal(e)
	}
}
func (x *storageFixture) packagePath() string {
	p := x.manifest.Pins[0]
	return filepath.Join(x.root, "speicher/inbox/pkg", p.Name+"-"+p.Commit+".pkg")
}
func (x *storageFixture) publish() {
	x.t.Helper()
	x.manifest.IssuedAt = x.now().Format(time.RFC3339)
	b, e := json.Marshal(x.manifest)
	if e != nil {
		x.t.Fatal(e)
	}
	b, e = gitobjects.Canonical(b)
	if e != nil {
		x.t.Fatal(e)
	}
	raw, e := json.Marshal(storagepins.Signed{Manifest: b, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(x.key, b))})
	if e != nil {
		x.t.Fatal(e)
	}
	x.write(filepath.Join(x.root, "speicher/inbox/list.json"), raw)
	if len(x.manifest.Pins) > 0 {
		x.write(x.packagePath(), x.pkg)
	}
}
func (x *storageFixture) state() storagepins.State {
	x.t.Helper()
	b, e := os.ReadFile(filepath.Join(x.root, "speicher/state.json"))
	if e != nil {
		x.t.Fatal(e)
	}
	var s storagepins.State
	if e = json.Unmarshal(b, &s); e != nil {
		x.t.Fatal(e)
	}
	return s
}
func (x *storageFixture) ready() {
	x.t.Helper()
	x.f.StorageOnce()
	x.advance(12*time.Minute + time.Second)
	x.publish()
	x.f.StorageOnce()
	if len(x.state().Approved) != 1 {
		x.t.Fatal("no approval", x.state())
	}
}
func (x *storageFixture) failed(reason string) {
	x.t.Helper()
	s := x.state()
	if len(s.Approved) != 0 || len(s.Failed) == 0 || s.Failed[0].State != "invalid" || s.Failed[0].Reason != reason {
		x.t.Fatalf("want invalid/%s without approval: %+v", reason, s)
	}
}

func TestStorageInboxAloneNeverApproves(t *testing.T) {
	x := newStorageFixture(t)
	x.f.StorageOnce()
	x.failed("fetch_failed")
}

func TestStorageApprovalRequiresKeyFreshnessAndHash(t *testing.T) {
	for _, which := range []string{"valid", "missing_key", "golden_key", "changed_key", "stale", "bad_signature", "package_hash", "wrong_path", "missing_package", "oversize_list", "oversize_package"} {
		t.Run(which, func(t *testing.T) {
			x := newStorageFixture(t)
			x.ready()
			reason := "fetch_failed"
			switch which {
			case "missing_key":
				if e := os.Remove(x.keyPath); e != nil {
					t.Fatal(e)
				}
			case "golden_key":
				b, e := os.ReadFile("../../contract/golden/storage/test-key.pub")
				if e != nil {
					t.Fatal(e)
				}
				x.write(x.keyPath, b)
			case "changed_key":
				p, _, e := ed25519.GenerateKey(rand.Reader)
				if e != nil {
					t.Fatal(e)
				}
				x.write(x.keyPath, []byte(base64.StdEncoding.EncodeToString(p)+"\n"))
			case "stale":
				x.advance(10*time.Minute + time.Second)
			case "bad_signature":
				x.write(filepath.Join(x.root, "speicher/inbox/list.json"), []byte(`{"body":"https://SECRET-URL/key-canary"}`))
			case "package_hash":
				b := bytes.Clone(x.pkg)
				b[len(b)-1] ^= 1
				x.write(x.packagePath(), b)
				reason = "hash_mismatch"
			case "wrong_path":
				x.manifest.Pins[0].Path = "scripts/other"
				x.publish()
				reason = "hash_mismatch"
			case "missing_package":
				if e := os.Remove(x.packagePath()); e != nil {
					t.Fatal(e)
				}
			case "oversize_list":
				x.write(filepath.Join(x.root, "speicher/inbox/list.json"), bytes.Repeat([]byte("x"), (1<<20)+1))
			case "oversize_package":
				f, e := os.OpenFile(x.packagePath(), os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				e = f.Truncate((128 << 20) + 1)
				_ = f.Close()
				if e != nil {
					t.Fatal(e)
				}
			}
			x.f.StorageOnce()
			s := x.state()
			if which == "valid" {
				h := sha256.Sum256(x.pkg)
				if len(s.Approved) != 1 || s.Approved[0].PackageSHA256 != hex.EncodeToString(h[:]) {
					t.Fatal("package byte hash missing")
				}
				return
			}
			x.failed(reason)
			if which == "changed_key" || which == "missing_key" || which == "golden_key" {
				if s.HighestSeq != 0 || s.Accepted != nil {
					t.Fatal("key reset retained sequence or accepted list")
				}
			}
			if which == "golden_key" && !strings.Contains(x.logs.String(), "storage_key_missing") {
				t.Fatal("golden trust key not blocked")
			}
			b, e := json.Marshal(s)
			if e != nil {
				t.Fatal(e)
			}
			for _, canary := range []string{"SECRET-URL", "key-canary", "DEPLOY-KEY-CANARY"} {
				if strings.Contains(string(b)+x.logs.String(), canary) {
					t.Fatal("echo", canary)
				}
			}
		})
	}
}

func TestStorageInboxSymlinksAtAdapter(t *testing.T) {
	for _, target := range []string{"runner-pins.pub", "runtime-key", "signed-list", "package", "parent"} {
		t.Run(target, func(t *testing.T) {
			x := newStorageFixture(t)
			x.ready()
			list := filepath.Join(x.root, "speicher/inbox/list.json")
			link := list
			dest := filepath.Join(filepath.Dir(x.keyPath), target)
			switch target {
			case "signed-list":
				b, e := os.ReadFile(list)
				if e != nil {
					t.Fatal(e)
				}
				x.write(dest, b)
			case "package":
				link = x.packagePath()
				dest = filepath.Join(filepath.Dir(x.keyPath), "package")
				x.write(dest, x.pkg)
			case "parent":
				link = filepath.Join(x.root, "speicher/inbox")
				dest = filepath.Join(x.root, "old-inbox")
				if e := os.Rename(link, dest); e != nil {
					t.Fatal(e)
				}
			}
			if target != "parent" {
				if e := os.Remove(link); e != nil {
					t.Fatal(e)
				}
			}
			if e := os.Symlink(dest, link); e != nil {
				t.Fatal(e)
			}
			x.f.StorageOnce()
			x.failed("fetch_failed")
			b, e := json.Marshal(x.state())
			if e != nil {
				t.Fatal(e)
			}
			for _, canary := range []string{"DEPLOY-KEY-CANARY", base64.StdEncoding.EncodeToString(x.key.Public().(ed25519.PublicKey))} {
				if strings.Contains(string(b)+x.logs.String(), canary) {
					t.Fatal("symlink content echoed")
				}
			}
		})
	}
}

func TestStorageNeverWritesInboxOrGitPaths(t *testing.T) {
	x := newStorageFixture(t)
	snap := func() map[string][]byte {
		out := map[string][]byte{}
		e := filepath.WalkDir(x.root, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() && !strings.HasSuffix(p, "state.json") {
				b, e := os.ReadFile(p)
				if e != nil {
					return e
				}
				out[p] = b
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	for _, name := range []string{"pins.json", "desktop-pins.json", "repo_url", "wanted/sentinel", "approved/sentinel", "failed/sentinel", "store.git/sentinel"} {
		p := filepath.Join(x.root, name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		x.write(p, []byte("sentinel"))
	}
	before := snap()
	x.f.StorageOnce()
	x.advance(13 * time.Minute)
	x.f.StorageOnce()
	after := snap()
	if len(before) != len(after) {
		t.Fatal("file set changed")
	}
	for p, b := range before {
		if !bytes.Equal(b, after[p]) {
			t.Fatal("written", p)
		}
	}
}

func TestStorageRefreshRevocationAndRestartReplay(t *testing.T) {
	x := newStorageFixture(t)
	x.ready()
	seq := x.state().HighestSeq
	for i := 0; i < 3; i++ {
		x.advance(9 * time.Minute)
		x.publish()
		x.f.StorageOnce()
		if len(x.state().Approved) != 1 || x.state().HighestSeq != seq {
			t.Fatal("unchanged pins expired")
		}
	}
	// Same sequence with an older but still fresh timestamp cannot roll back.
	x.advance(-time.Minute)
	x.publish()
	x.f.StorageOnce()
	x.failed("fetch_failed")
	x.advance(time.Minute)
	x.manifest.Seq++
	x.manifest.Pins = []storagepins.Pin{}
	x.publish()
	x.f.StorageOnce()
	if len(x.state().Approved) != 0 {
		t.Fatal("revocation")
	}
	x.f = New(x.f.cfg)
	x.manifest.Seq--
	x.publish()
	x.f.StorageOnce()
	x.failed("fetch_failed")
}

func TestStorageKeySwitchBackFromApprovedStateStaysQuarantined(t *testing.T) {
	x := newStorageFixture(t)
	x.ready()
	a, e := os.ReadFile(x.keyPath)
	if e != nil {
		t.Fatal(e)
	}
	b, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	x.write(x.keyPath, []byte(base64.StdEncoding.EncodeToString(b)+"\n"))
	x.f.StorageOnce()
	x.failed("fetch_failed")
	x.advance(3 * time.Minute)
	x.write(x.keyPath, a)
	x.publish()
	x.f.StorageOnce()
	x.failed("fetch_failed")
	for _, d := range []time.Duration{9 * time.Minute, 3 * time.Minute} {
		x.advance(d)
		x.publish()
		x.f.StorageOnce()
		x.failed("fetch_failed")
	}
	x.advance(time.Second)
	x.publish()
	x.f.StorageOnce()
	if len(x.state().Approved) != 1 {
		t.Fatal("fresh list not accepted after switch quarantine")
	}
}

func TestStorageTotalBudget(t *testing.T) {
	x := newStorageFixture(t)
	x.ready()
	x.f.storageOnce(int64(len(x.pkg)) - 1)
	x.failed("fetch_failed")
}

func TestGitInitLeavesStorageUntouched(t *testing.T) {
	x := newStorageFixture(t)
	x.ready()
	before, e := os.ReadFile(filepath.Join(x.root, "speicher/state.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = x.f.initStore(context.Background(), "ssh://git@example.org/org/scripts.git", strings.Repeat("a", 64)); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(x.root, "speicher/state.json"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("Git reset changed storage state")
	}
	b, e := os.ReadFile(x.packagePath())
	if e != nil || !bytes.Equal(b, x.pkg) {
		t.Fatal("Git reset changed inbox")
	}
}

type storageBlockingGit struct {
	started chan struct{}
	once    sync.Once
}

func (g *storageBlockingGit) Run(ctx context.Context, _ []string, _ ...string) error {
	g.once.Do(func() { close(g.started) })
	<-ctx.Done()
	return ctx.Err()
}
func (g *storageBlockingGit) Output(ctx context.Context, env []string, args ...string) ([]byte, error) {
	return nil, g.Run(ctx, env, args...)
}
func TestStorageLoopIndependentOfBlockedGitOnce(t *testing.T) {
	x := newStorageFixture(t)
	x.ready()
	g := &storageBlockingGit{started: make(chan struct{})}
	x.f.cfg.Git = g
	if e := os.MkdirAll(filepath.Join(x.root, "wanted"), 0700); e != nil {
		t.Fatal(e)
	}
	x.write(filepath.Join(x.root, "wanted", strings.Repeat("a", 64)), nil)
	x.write(filepath.Join(x.root, "repo_url"), []byte("ssh://git@example.org/org/scripts.git"))
	if e := os.Remove(filepath.Join(x.root, "speicher/inbox/list.json")); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = x.f.Run(ctx); close(done) }()
	select {
	case <-g.started:
	case <-time.After(3 * time.Second):
		t.Fatal("git did not block")
	}
	first := time.Now().Add(3 * time.Second)
	for len(x.state().Approved) != 0 && time.Now().Before(first) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(x.state().Approved) != 0 {
		t.Fatal("first storage pass missing")
	}
	// A second refresh must happen on the independent fixed 5-second timer.
	x.advance(time.Minute)
	x.manifest.Seq++
	x.publish()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if x.state().HighestSeq == x.manifest.Seq {
			cancel()
			select {
			case <-done:
				return
			case <-time.After(time.Second):
				t.Fatal("goroutine leaked")
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("storage stalled behind Git")
}
