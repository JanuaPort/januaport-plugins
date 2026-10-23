package mediator

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/fetcher"
)

const desktopManifest = `name: desk
description: Desktop-Test.
entry: main.py
input_schema: {type: object}
tools: [kartei_find]
timeout_s: 86400
python: python.zip
requirements: requirements.lock
`

const desktopDevice = "12345678-1234-4123-8123-123456789abc"

func desktopFixture(t *testing.T, change func(*fixture)) *fixture {
	return newFixtureWith(t, func(f *fixture) {
		f.write("desk/jnpt-desktop.yaml", desktopManifest)
		// A second executable manifest deliberately exercises the other entry path.
		f.write("desk/jnpt-script.yaml", strings.Replace(quickManifest, "name: quick", "name: desk", 1))
		f.write("desk/main.py", "print('desktop')\n")
		f.write("desk/python.zip", "opaque runtime bytes")
		p := libsPin{libs: []testLib{{dist: "win", version: "1.0", tag: "cp312-cp312-win_amd64", files: map[string]string{"win/__init__.py": "CANARY=1\n"}}}}
		f.write("desk/requirements.lock", p.lockText(t))
		f.write("desk/wheels/"+p.libs[0].fileName(), string(p.libs[0].bytes(t)))
		if change != nil {
			change(f)
		}
	})
}

func (e *env) writeDesktop(commit, path string) {
	e.t.Helper()
	e.writeDesktopRaw(fmt.Sprintf(`{"version":1,"repo_url":"ssh://git@git.example.com/org/scripts.git","pins":[{"name":"desk","commit":%q,"path":%q,"target_device":%q,"algo":"sha1"}]}`, commit, path, desktopDevice))
}

func (e *env) writeDesktopRaw(s string) {
	e.t.Helper()
	tmp := filepath.Join(e.keys, "desktop-pins.json.tmp")
	if err := os.WriteFile(tmp, []byte(s), 0600); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(e.keys, "desktop-pins.json")); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) desktopMeta() []map[string]any {
	e.t.Helper()
	b, _ := json.Marshal(e.listTools().Meta[contract.MetaRunner])
	var r struct {
		Pins []map[string]any `json:"desktop_pins"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		e.t.Fatal(err)
	}
	return r.Pins
}

func (e *env) waitDesktop(state, reason string) {
	e.t.Helper()
	waitUntil(e.t, 5*time.Second, "Desktop-Pin "+state+"/"+reason, func() bool {
		p := e.desktopMeta()
		return len(p) == 1 && p[0]["state"] == state && (reason == "" || p[0]["reason"] == reason)
	})
}

func (e *env) packageGet(commit, path, key string) (int, http.Header, []byte) {
	e.t.Helper()
	r, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/desktop/package?commit="+commit+"&path="+path, nil)
	r.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, b
}

func TestDesktopMetadataAndNoSandboxOrWheelExtraction(t *testing.T) {
	e := newEnvFx(t, desktopFixture(t, nil), nil)
	e.writeDesktop(e.fx.commit, "desk")
	e.waitDesktop("ready", "") // Desktop readiness does not depend on sandbox isolation.
	p := e.desktopMeta()[0]
	if p["timeout_s"] != float64(86400) || p["description"] != "Desktop-Test." || p["input_schema"] == nil || mustJSON(p["declared_tools"]) != `["kartei_find"]` {
		t.Fatalf("metadata: %v", p)
	}
	g := dialGuard(t, e.sock, "d1", 2)
	if tools := e.listTools().Tools; len(tools) != 0 {
		t.Fatalf("Desktop registered as sandbox tool: %v", tools)
	}
	assertEnd(t, e.mustCall("desk", map[string]any{}), contract.EndRefused, contract.DetailPin)
	// Same name in pins.json must not create a second execution permission.
	e.writePins([]pinRow{{Name: "desk", Path: "desk", Commit: e.fx.commit}})
	waitUntil(t, 5*time.Second, "local pin loaded", func() bool { e.m.mu.Lock(); defer e.m.mu.Unlock(); return len(e.m.pins) == 1 })
	assertEnd(t, e.mustCall("desk", map[string]any{}), contract.EndRefused, contract.DetailPin)
	assertNoSandboxJob(t, g) // Real Unix adapter: zero job frames, counting seam below.
	if e.m.libs.PreparedCount() != 0 {
		t.Fatal("Desktop pin prepared libraries")
	}
	entries, err := os.ReadDir(e.libs)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Windows wheel extracted: %v %v", entries, err)
	}
}

func TestDesktopPackageAuthorizationAtHTTP(t *testing.T) {
	e := newEnvFx(t, desktopFixture(t, nil), nil)
	e.writeDesktop(e.fx.commit, "desk")
	e.waitDesktop("ready", "")
	status, h, b := e.packageGet(e.fx.commit, "desk", testKey)
	if status != 200 || h.Get("Content-Type") != "application/vnd.jnpt.gitobjects.v1" {
		t.Fatalf("package: %d %v %s", status, h, b)
	}
	if len(b) < 4 || int(binary.BigEndian.Uint32(b[:4])) > len(b)-4 {
		t.Fatal("invalid framing")
	}
	if status, _, _ := e.packageGet(e.fx.commit, "desk", "wrong"); status != 401 {
		t.Fatalf("wrong bearer: %d", status)
	}
	if status, _, _ := e.packageGet(e.fx.commit, "desk", ""); status != 401 {
		t.Fatalf("empty bearer: %d", status)
	}
	var first []byte
	for _, q := range [][2]string{{strings.Repeat("a", 40), "desk"}, {e.fx.commit, "quick"}, {e.fx.commit, "desk/main.py"}, {e.fx.commit, "../desk"}, {"--all", "desk"}} {
		s, h, b := e.packageGet(q[0], q[1], testKey)
		if s != 404 || h.Get("Cache-Control") != "no-store" {
			t.Fatalf("foreign package: %d %v", s, h)
		}
		if first == nil {
			first = b
		} else if !bytes.Equal(first, b) {
			t.Fatalf("404 bodies differ: %s / %s", first, b)
		}
	}
	// Revoke immediately without waiting for the state poll/cache.
	if err := os.Remove(filepath.Join(e.fx.store, "approved", e.fx.commit)); err != nil {
		t.Fatal(err)
	}
	if s, _, b := e.packageGet(e.fx.commit, "desk", testKey); s != 404 || !bytes.Equal(first, b) {
		t.Fatalf("revoked: %d %s", s, b)
	}
}

func TestDesktopPRCommitNoApprovalNoPackage(t *testing.T) {
	e := newEnvFx(t, desktopFixture(t, nil), nil)
	f := e.fx
	f.git(f.work, "checkout", "-q", "--detach", f.commit)
	f.write("desk/main.py", "print('PR only')\n")
	f.git(f.work, "add", "desk/main.py")
	f.git(f.work, "commit", "-q", "-m", "PR")
	pr := f.git(f.work, "rev-parse", "HEAD")
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.git(f.work, "clone", "-q", "--bare", f.work, remote)
	f.git(f.work, "--git-dir="+remote, "update-ref", "refs/heads/trunk", f.commit)
	f.git(f.work, "--git-dir="+remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
	f.git(f.work, "--git-dir="+remote, "update-ref", "refs/pull/1/head", pr)
	e.writeDesktop(pr, "desk")
	waitUntil(t, 5*time.Second, "desktop wanted", func() bool { _, err := os.Stat(filepath.Join(f.store, "wanted", pr)); return err == nil })
	abholer := fetcher.New(fetcher.Config{StoreDir: f.store, KeyPath: filepath.Join(e.keys, "runtime-key"), TmpDir: t.TempDir(), Git: branchTestGit{remote: remote}})
	abholer.Once(context.Background())
	e.waitDesktop("invalid", "not_on_default_branch")
	if _, err := os.Stat(filepath.Join(f.store, "approved", pr)); !os.IsNotExist(err) {
		t.Fatalf("PR approved: %v", err)
	}
	if s, _, _ := e.packageGet(pr, "desk", testKey); s != 404 {
		t.Fatalf("PR package: %d", s)
	}
}

func TestDesktopTickerNeverFiresAtHTTP(t *testing.T) {
	core := newFakeCore(t, answer("ok"))
	clk := &fakeClock{t: clockAt(7, 59, 50)}
	e := newEnvFx(t, desktopFixture(t, nil), nil, withGateway(core.srv.URL), withClock(clk))
	e.writeDesktop(e.fx.commit, "desk")
	e.waitDesktop("ready", "")
	e.writeSchedules(scheduleRow("d", "desk", e.fx.commit, "0 8 * * *", "UTC", true))
	waitUntil(t, 5*time.Second, "desktop schedule known", func() bool { s, ok := e.schedule("d"); return ok && s.State == contract.ScheduleReady })
	clk.Set(clockAt(8, 0, 1))
	waitUntil(t, 5*time.Second, "next desktop slot", func() bool { s, ok := e.schedule("d"); return ok && s.NextRun == "2026-10-13T08:00:00Z" })
	// Barrier covers the asynchronous trigger goroutine if the gate is removed.
	time.Sleep(100 * time.Millisecond)
	if got := core.requests(); len(got) != 0 {
		t.Fatalf("Desktop schedule fired: %v", got)
	}
	if s, _ := e.schedule("d"); s.LastRun != "" || s.Missed != 0 || s.SkippedBusy != 0 {
		t.Fatalf("desktop counters: %+v", s)
	}
}

func TestDesktopPackageLimitAtHTTP(t *testing.T) {
	fx := desktopFixture(t, func(f *fixture) { f.write("desk/big.bin", strings.Repeat("x", 128<<20)) })
	e := newEnvFx(t, fx, nil)
	e.writeDesktop(fx.commit, "desk")
	waitUntil(t, 5*time.Second, "desktop pin read", func() bool { return len(e.desktopMeta()) == 1 })
	if s, _, b := e.packageGet(fx.commit, "desk", testKey); s != 413 || bytes.Contains(b, []byte("big.bin")) {
		t.Fatalf("limit: %d %s", s, b)
	}
}
