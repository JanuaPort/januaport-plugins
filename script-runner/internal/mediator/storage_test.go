package mediator

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/fetcher"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitobjects"
	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
)

type storageAdapter struct {
	e       *env
	f       *fetcher.Fetcher
	clock   atomic.Int64
	key     ed25519.PrivateKey
	keyPath string
	pin     storagepins.Pin
	pkg     []byte
	mu      sync.Mutex
	body    []byte
	status  int
}

func newStorageAdapter(t *testing.T) *storageAdapter {
	t.Helper()
	x := &storageAdapter{}
	x.clock.Store(time.Now().UTC().Truncate(time.Second).UnixNano())
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	x.key = key
	var signed storagepins.Signed
	var list storagepins.Manifest
	if json.Unmarshal(courierList(t), &signed) != nil || json.Unmarshal(signed.Manifest, &list) != nil {
		t.Fatal("golden")
	}
	x.pin = list.Pins[0]
	x.pin.Commit = "7557ff927b2f2e82b826087aa80597499207f18b26f1704368ebae6c19e81760"
	x.pkg, e = os.ReadFile("testdata/storage.pkg")
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		x.mu.Lock()
		defer x.mu.Unlock()
		if x.status != 0 {
			w.WriteHeader(x.status)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pins") {
			_, _ = w.Write(x.body)
		} else {
			_, _ = w.Write(x.pkg)
		}
	}))
	t.Cleanup(server.Close)
	x.sign(t, 9, []storagepins.Pin{x.pin})
	x.e = newEnv(t, nil, func(c *Config) { c.GatewayURL = server.URL; c.Now = x.now })
	keys := t.TempDir()
	x.keyPath = filepath.Join(keys, "runner-pins.pub")
	if e = os.WriteFile(x.keyPath, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	x.f = fetcher.New(fetcher.Config{StoreDir: x.e.fx.store, KeyPath: filepath.Join(keys, "runtime-key"), StorageNow: x.now})
	x.f.StorageOnce()
	x.clock.Add(int64(13 * time.Minute))
	x.sign(t, 9, []storagepins.Pin{x.pin})
	x.transport()
	return x
}
func (x *storageAdapter) now() time.Time { return time.Unix(0, x.clock.Load()).UTC() }
func (x *storageAdapter) sign(t *testing.T, seq uint64, pins []storagepins.Pin) {
	t.Helper()
	m := storagepins.Manifest{Purpose: storagepins.Purpose, Version: 1, Source: "speicher", Seq: seq, IssuedAt: x.now().Format(time.RFC3339), Pins: pins}
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	b, e = gitobjects.Canonical(b)
	if e != nil {
		t.Fatal(e)
	}
	b, e = json.Marshal(storagepins.Signed{Manifest: b, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(x.key, b))})
	if e != nil {
		t.Fatal(e)
	}
	x.mu.Lock()
	x.body = b
	x.mu.Unlock()
}
func (x *storageAdapter) transport() {
	c := x.e.m.storageCourier
	c.mu.Lock()
	c.next = time.Time{}
	c.mu.Unlock()
	c.Once(context.Background())
}
func (x *storageAdapter) pkgPath() string {
	return filepath.Join(x.e.fx.store, "speicher/inbox/pkg", x.pin.Name+"-"+x.pin.Commit+".pkg")
}
func (x *storageAdapter) approve() { x.f.StorageOnce() }
func (x *storageAdapter) cached(t *testing.T) pinState {
	t.Helper()
	x.e.waitReady(x.pin.Name)
	p, _, ok := x.e.m.findPin(x.pin.Name)
	if !ok {
		t.Fatal("pin missing")
	}
	return p
}

func TestStorageReadyAndRunAtMCP(t *testing.T) {
	x := newStorageAdapter(t)
	x.approve()
	g := dialGuard(t, x.e.sock, "storage-run", 2)
	x.e.waitReady(x.pin.Name)
	done := make(chan struct{})
	go func() {
		defer close(done)
		j := g.job()
		if j.Entry != "main.py" {
			t.Error("wrong entry")
		}
		g.send(frame.Output{Type: frame.TypeOutput, Data: `{"storage":true}`})
		g.exited(0)
		_ = g.conn.Close()
	}()
	result, e := x.e.call(x.pin.Name, map[string]any{}, x.pin.Commit)
	if e != nil {
		t.Fatal(e)
	}
	assertEnd(t, result, contract.EndOK, "")
	<-done
	b, _ := json.Marshal(x.e.listTools().Meta[contract.MetaRunner])
	if strings.Contains(string(b), "source") || strings.Contains(string(b), "storage_pins") {
		t.Fatal("wire contract expanded")
	}
}

func TestStorageInboxAndHTTPFailuresNeverReady(t *testing.T) {
	for _, status := range []int{0, 404, 503, -1} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			x := newStorageAdapter(t)
			g := dialGuard(t, x.e.sock, "storage-no-approval", 2)
			if status != 0 {
				x.mu.Lock()
				x.status = status
				x.mu.Unlock()
				if status < 0 {
					x.e.m.storageCourier.base = "http://127.0.0.1:1"
				}
				x.transport()
			}
			time.Sleep(50 * time.Millisecond)
			p, _, ok := x.e.m.findPin(x.pin.Name)
			if ok && p.state == contract.StateReady {
				t.Fatal("inbox or error became ready")
			}
			assertNoSandboxJob(t, g)
		})
	}
}

func TestStorageRechecksAtCodeHandoffWithStoppedFetcher(t *testing.T) {
	for _, mode := range []string{"expired", "hash", "digest", "approval_removed", "key_missing", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			x := newStorageAdapter(t)
			x.approve()
			g := dialGuard(t, x.e.sock, "storage-refusal", 2)
			p := x.cached(t)
			switch mode {
			case "expired":
				x.clock.Add(int64(10*time.Minute + time.Nanosecond))
			case "hash":
				if e := os.WriteFile(x.pkgPath(), append(x.pkg, 'x'), 0600); e != nil {
					t.Fatal(e)
				}
			case "digest":
				path := filepath.Join(x.e.fx.store, "speicher/state.json")
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var state storagepins.State
				if json.Unmarshal(b, &state) != nil {
					t.Fatal("state")
				}
				state.Approved[0].PackageSHA256 = strings.Repeat("0", 64)
				b, _ = json.Marshal(state)
				if err = os.WriteFile(path, b, 0600); err != nil {
					t.Fatal(err)
				}
			case "approval_removed":
				if e := os.Remove(filepath.Join(x.e.fx.store, "speicher/state.json")); e != nil {
					t.Fatal(e)
				}
			case "key_missing":
				if e := os.Remove(x.keyPath); e != nil {
					t.Fatal(e)
				}
				x.f.StorageOnce()
			case "revoke":
				x.sign(t, 10, []storagepins.Pin{})
				x.transport()
				x.f.StorageOnce()
			}
			if err := x.e.m.pinSource(p).CheckStorage(); err == nil {
				t.Fatal("sink accepted forbidden storage state", mode)
			}
			result, _ := x.e.m.run(context.Background(), p, []byte(`{}`), testRunToken, contract.RunLimits{WallS: 25, OutputBytes: 1048576, ToolCalls: 50})
			if result.End != contract.EndRefused || result.Detail != contract.DetailState {
				t.Fatalf("cached approval executable after %s: %+v", mode, result)
			}
			assertNoSandboxJob(t, g)
		})
	}
}

func TestStorageExpiryWhileWaitingForSandbox(t *testing.T) {
	x := newStorageAdapter(t)
	x.approve()
	g := dialGuard(t, x.e.sock, "held", 2)
	p := x.cached(t)
	held, ok := x.e.m.sb.acquire(make(chan struct{}), time.Second)
	if !ok {
		t.Fatal("hold slot")
	}
	_ = held
	done := make(chan contract.Run, 1)
	go func() {
		r, _ := x.e.m.run(context.Background(), p, []byte(`{}`), testRunToken, contract.RunLimits{WallS: 25, OutputBytes: 1048576, ToolCalls: 50})
		done <- r
	}()
	time.Sleep(50 * time.Millisecond)
	x.clock.Add(int64(10*time.Minute + time.Second))
	x.e.closeGuardAndWaitEOF(g)
	next := dialGuard(t, x.e.sock, "after-expiry", 2)
	select {
	case r := <-done:
		if r.End != contract.EndRefused {
			t.Fatal("expired while waiting", r)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("blocked run")
	}
	assertNoSandboxJob(t, next)
}

func TestStoragePruneRevokedPackages(t *testing.T) {
	x := newStorageAdapter(t)
	x.approve()
	g := dialGuard(t, x.e.sock, "prune", 2)
	_ = g
	x.e.waitReady(x.pin.Name)
	// The courier has not delivered another list: only the approval expires.
	x.clock.Add(int64(10*time.Minute + time.Second))
	waitFor(t, "expired package pruned", func() bool { _, e := os.Stat(x.pkgPath()); return os.IsNotExist(e) })
}
