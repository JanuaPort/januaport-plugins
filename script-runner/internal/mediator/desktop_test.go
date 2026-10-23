package mediator

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/fetcher"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
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
	algo := "sha1"
	if len(commit) == 64 {
		algo = "sha256"
	}
	e.writeDesktopRaw(fmt.Sprintf(`{"version":1,"repo_url":"ssh://git@git.example.com/org/scripts.git","pins":[{"name":"desk","commit":%q,"path":%q,"target_device":%q,"algo":%q}]}`, commit, path, desktopDevice, algo))
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
	if len(p) != 7 || p["reason"] != "" {
		t.Fatalf("desktop metadata fields: %v", p)
	}
	if p["timeout_s"] != float64(86400) || p["description"] != "Desktop-Test." || p["input_schema"] == nil || mustJSON(p["declared_tools"]) != `["kartei_find"]` {
		t.Fatalf("metadata: %v", p)
	}
	g := dialGuard(t, e.sock, "d1", 2)
	var jobs atomic.Int64
	stop := make(chan struct{})
	go func() {
		defer close(stop)
		for {
			raw, err := frame.Read(g.conn, 64<<20)
			if err != nil {
				return
			}
			typ, _ := frame.TypeOf(raw)
			if typ == frame.TypeJob {
				jobs.Add(1)
				_ = frame.Write(g.conn, frame.Exited{Type: frame.TypeExited, Code: 0})
			}
		}
	}()
	t.Cleanup(func() { _ = g.conn.Close(); <-stop; t.Logf("Sandbox counting seam = %d (want 0)", jobs.Load()) })
	if tools := e.listTools().Tools; len(tools) != 0 {
		t.Fatalf("Desktop registered as sandbox tool: %v", tools)
	}
	assertEnd(t, e.mustCall("desk", map[string]any{}), contract.EndRefused, contract.DetailPin)
	// Same name in pins.json must not create a second execution permission.
	e.writePins([]pinRow{{Name: "desk", Path: "desk", Commit: e.fx.commit}})
	waitUntil(t, 5*time.Second, "local pin loaded", func() bool { e.m.mu.Lock(); defer e.m.mu.Unlock(); return len(e.m.pins) == 1 })
	assertEnd(t, e.mustCall("desk", map[string]any{}), contract.EndRefused, contract.DetailPin)
	listed := e.listTools()
	if len(listed.Tools) != 0 || !strings.Contains(mustJSON(listed.Meta), `"state":"invalid"`) {
		t.Fatalf("local conflict not invalid: %+v", listed)
	}
	if n := jobs.Load(); n != 0 {
		t.Fatalf("Sandbox counting seam = %d, want 0", n)
	}
	if e.m.libs.PreparedCount() != 0 {
		t.Fatal("Desktop pin prepared libraries")
	}
	entries, err := os.ReadDir(e.libs)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Windows wheel extracted: %v %v", entries, err)
	}
}

func TestDesktopInvalidManifestAtHTTP(t *testing.T) {
	cases := map[string]string{
		"unknown":             desktopManifest + "KANARIE: geheim\n",
		"trailing":            desktopManifest + "---\nKANARIE: geheim\n",
		"missing_tools":       strings.Replace(desktopManifest, "tools: [kartei_find]\n", "", 1),
		"missing_python":      strings.Replace(desktopManifest, "python: python.zip\n", "", 1),
		"missing_name":        strings.Replace(desktopManifest, "name: desk\n", "", 1),
		"missing_description": strings.Replace(desktopManifest, "description: Desktop-Test.\n", "", 1),
		"missing_entry":       strings.Replace(desktopManifest, "entry: main.py\n", "", 1),
		"missing_schema":      strings.Replace(desktopManifest, "input_schema: {type: object}\n", "", 1),
		"missing_timeout":     strings.Replace(desktopManifest, "timeout_s: 86400\n", "", 1),
		"timeout_high":        strings.Replace(desktopManifest, "86400", "86401", 1),
		"timeout_zero":        strings.Replace(desktopManifest, "86400", "0", 1),
		"timeout_fractional":  strings.Replace(desktopManifest, "86400", "1.5", 1),
		"description_boolean": strings.Replace(desktopManifest, "description: Desktop-Test.", "description: true", 1),
		"tools_boolean":       strings.Replace(desktopManifest, "[kartei_find]", "[true]", 1),
		"tool_script":         strings.Replace(desktopManifest, "kartei_find", "script_desk", 1),
		"tool_admin":          strings.Replace(desktopManifest, "kartei_find", "admin_secret", 1),
		"tool_duplicate":      strings.Replace(desktopManifest, "[kartei_find]", "[kartei_find, kartei_find]", 1),
		"schema":              strings.Replace(desktopManifest, "type: object", "type: array", 1),
		"entry_escape":        strings.Replace(desktopManifest, "main.py", "../main.py", 1),
		"python_escape":       strings.Replace(desktopManifest, "python.zip", "../python.zip", 1),
		"python_not_zip":      strings.Replace(desktopManifest, "python.zip", "main.py", 1),
		"name_mismatch":       strings.Replace(desktopManifest, "name: desk", "name: other", 1),
		"requirements_null":   strings.Replace(desktopManifest, "requirements.lock", "null", 1),
		"requirements_other":  strings.Replace(desktopManifest, "requirements.lock", "KANARIE", 1),
		"description_long":    strings.Replace(desktopManifest, "Desktop-Test.", strings.Repeat("x", 1025), 1),
		"tool_syntax":         strings.Replace(desktopManifest, "kartei_find", "KANARIE.tool", 1),
		"tools_null":          strings.Replace(desktopManifest, "[kartei_find]", "null", 1),
		"yaml_duplicate":      desktopManifest + "name: desk\n",
		"schema_non_json":     strings.Replace(desktopManifest, "input_schema: {type: object}", "input_schema: {type: object, properties: {1: {type: string}}}", 1),
	}
	for name, man := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnvFx(t, desktopFixture(t, func(f *fixture) { f.write("desk/jnpt-desktop.yaml", man) }), nil)
			e.writeDesktop(e.fx.commit, "desk")
			// Exercise HTTP directly; no readiness/cache prerequisite masks a bad reader.
			if s, _, b := e.packageGet(e.fx.commit, "desk", testKey); s != 404 || bytes.Contains(b, []byte("KANARIE")) {
				t.Fatalf("manifest accepted/echoed: status=%d, bytes=%d", s, len(b))
			}
			e.waitDesktop("invalid", "")
			if strings.Contains(mustJSON(e.desktopMeta()), "KANARIE") || strings.Contains(e.logs.String(), "KANARIE") {
				t.Fatal("manifest error echoed")
			}
		})
	}
}

func TestDesktopMissingRequiredFilesAtHTTP(t *testing.T) {
	for _, file := range []string{"jnpt-desktop.yaml", "main.py", "python.zip", "requirements.lock"} {
		t.Run(file, func(t *testing.T) {
			e := newEnvFx(t, desktopFixture(t, func(f *fixture) {
				if err := os.Remove(filepath.Join(f.work, "desk", file)); err != nil {
					t.Fatal(err)
				}
			}), nil)
			e.writeDesktop(e.fx.commit, "desk")
			if s, _, _ := e.packageGet(e.fx.commit, "desk", testKey); s != 404 {
				t.Fatalf("missing file accepted: %d", s)
			}
		})
	}
}

func TestDesktopPinFileFailsClosedAtHTTP(t *testing.T) {
	e := newEnvFx(t, desktopFixture(t, nil), nil)
	e.writeDesktop(e.fx.commit, "desk")
	valid, err := os.ReadFile(filepath.Join(e.keys, "desktop-pins.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"broken": "{", "unknown": strings.Replace(string(valid), `"version":1`, `"unknown":true,"version":1`, 1),
		"version":  strings.Replace(string(valid), `"version":1`, `"version":2`, 1),
		"trailing": string(valid) + `{}`, "algo": strings.Replace(string(valid), `"sha1"`, `"sha256"`, 1),
		"device": strings.Replace(string(valid), desktopDevice, "", 1), "path": strings.Replace(string(valid), `"path":"desk"`, `"path":"../desk"`, 1),
		"url":        strings.Replace(string(valid), "ssh://git@git.example.com/org/scripts.git", "ext::KANARIE", 1),
		"duplicate":  strings.TrimSuffix(string(valid), "]}") + "," + strings.Split(string(valid), `"pins":[`)[1],
		"over_limit": strings.Repeat(" ", 1<<20) + string(valid),
	} {
		t.Run(name, func(t *testing.T) {
			e.writeDesktopRaw(raw)
			if s, _, b := e.packageGet(e.fx.commit, "desk", testKey); s != 404 || bytes.Contains(b, []byte("KANARIE")) {
				t.Fatalf("invalid pin allowed: status=%d, bytes=%d", s, len(b))
			}
		})
	}
	if err := os.Remove(filepath.Join(e.keys, "desktop-pins.json")); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := e.packageGet(e.fx.commit, "desk", testKey); s != 404 {
		t.Fatalf("removed pin allowed: %d", s)
	}
}

func TestDesktopQueryRejectsMalformedAtHTTP(t *testing.T) {
	e := newEnvFx(t, desktopFixture(t, nil), nil)
	e.writeDesktop(e.fx.commit, "desk")
	for _, extra := range []string{"&path=desk", "&commit=" + e.fx.commit, "&extra=1", "&bad;ignored", "&bad=%zz"} {
		r, _ := http.NewRequest("GET", e.srv.URL+"/desktop/package?commit="+e.fx.commit+"&path=desk"+extra, nil)
		r.Header.Set("Authorization", "Bearer "+testKey)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("query %s: %d", extra, resp.StatusCode)
		}
	}
}

func TestDesktopPackageRechecksEveryObjectAtHTTP(t *testing.T) {
	for _, typ := range []string{"commit", "tree", "blob"} {
		t.Run(typ, func(t *testing.T) {
			e := newEnvFx(t, desktopFixture(t, nil), nil)
			e.writeDesktop(e.fx.commit, "desk")
			e.waitDesktop("ready", "")
			id := e.fx.commit
			var raw []byte
			switch typ {
			case "commit":
			case "tree":
				id = e.fx.git(e.fx.work, "rev-parse", e.fx.commit+":desk")
			case "blob":
				id = e.fx.git(e.fx.work, "rev-parse", e.fx.commit+":desk/main.py")
			}
			cmd := exec.Command("git", "--git-dir="+filepath.Join(e.fx.store, "store.git"), "cat-file", typ, id)
			var err error
			raw, err = cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			switch typ {
			case "commit":
				raw = append(raw, []byte("\nMUTATED\n")...)
			case "tree":
				raw = bytes.Replace(raw, []byte("100644"), []byte("100755"), 1)
			case "blob":
				raw = []byte("print('MUTATED')\n")
			}
			var buf bytes.Buffer
			z := zlib.NewWriter(&buf)
			fmt.Fprintf(z, "%s %d\x00", typ, len(raw))
			z.Write(raw)
			z.Close()
			p := filepath.Join(e.fx.store, "store.git", "objects", id[:2], id[2:])
			_ = os.Chmod(p, 0644)
			if err := os.WriteFile(p, buf.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
			if s, _, _ := e.packageGet(e.fx.commit, "desk", testKey); s != 404 {
				t.Fatalf("tampered %s delivered: %d", typ, s)
			}
		})
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
	var firstHeader http.Header
	for _, q := range [][2]string{{strings.Repeat("a", 40), "desk"}, {e.fx.commit, "quick"}, {e.fx.commit, "desk/main.py"}, {e.fx.commit, "../desk"}, {"--all", "desk"}} {
		s, h, b := e.packageGet(q[0], q[1], testKey)
		if s != 404 || h.Get("Cache-Control") != "no-store" {
			t.Fatalf("foreign package: %d %v", s, h)
		}
		if first == nil {
			first = b
			firstHeader = h
		} else if !bytes.Equal(first, b) {
			t.Fatalf("404 bodies differ: %s / %s", first, b)
		}
		for _, key := range []string{"Content-Type", "Cache-Control", "Content-Length"} {
			if h.Get(key) != firstHeader.Get(key) {
				t.Fatalf("404 headers differ for %s", key)
			}
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
	// The same real fetcher approves the default-branch commit, with no seeded
	// approval surviving its initial store reset.
	e.writeDesktop(f.commit, "desk")
	waitUntil(t, 5*time.Second, "default desktop wanted", func() bool { _, err := os.Stat(filepath.Join(f.store, "wanted", f.commit)); return err == nil })
	abholer.Once(context.Background())
	e.waitDesktop("ready", "")
	if s, _, _ := e.packageGet(f.commit, "desk", testKey); s != 200 {
		t.Fatalf("default branch package: %d", s)
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
	clk.Set(clockAt(8, 0, 1).Add(72 * time.Hour))
	waitUntil(t, 5*time.Second, "device owns missed slots", func() bool { s, ok := e.schedule("d"); return ok && s.NextRun == "2026-10-16T08:00:00Z" })
	if s, _ := e.schedule("d"); s.Missed != 0 || len(core.requests()) != 0 {
		t.Fatalf("desktop gap counted/fired: %+v", s)
	}
}

func TestDesktopPackageLimitAtHTTP(t *testing.T) {
	fx := desktopFixture(t, func(f *fixture) { f.write("desk/big.bin", strings.Repeat("x", 128<<20)) })
	e := newEnvFx(t, fx, nil)
	e.writeDesktop(fx.commit, "desk")
	waitUntil(t, 5*time.Second, "desktop pin read", func() bool { return len(e.desktopMeta()) == 1 })
	if s, _, b := e.packageGet(fx.commit, "desk", testKey); s != 413 || bytes.Contains(b, []byte("big.bin")) {
		t.Fatalf("limit: %d (%d response bytes)", s, len(b))
	}
}

func TestDesktopWheelPreparationSeamZero(t *testing.T) {
	for _, tag := range []string{"cp312-cp312-win_amd64", "py3-none-any"} {
		t.Run(tag, func(t *testing.T) {
			fx := desktopFixture(t, func(f *fixture) {
				if err := os.RemoveAll(filepath.Join(f.work, "desk", "wheels")); err != nil {
					t.Fatal(err)
				}
				p := libsPin{libs: []testLib{{dist: "wheel", version: "1.0", tag: tag, files: map[string]string{"wheel/__init__.py": "X=1\n"}}}}
				f.write("desk/wheels/"+p.libs[0].fileName(), string(p.libs[0].bytes(t)))
				f.write("desk/requirements.lock", p.lockText(t))
				f.write("desk/jnpt-script.yaml", strings.Replace(quickManifest, "name: quick", "name: desk", 1)+"requirements: requirements.lock\n")
			})
			e := newEnvFx(t, fx, nil)
			e.writeDesktop(fx.commit, "desk")
			e.waitDesktop("ready", "")
			if s, _, _ := e.packageGet(fx.commit, "desk", testKey); s != 200 {
				t.Fatalf("package: %d", s)
			}
			if n := e.m.libs.PreparedCount(); n != 0 {
				t.Fatalf("wheel preparation seam = %d, want 0", n)
			}
			for _, dir := range []string{e.m.cfg.LibsDir, e.m.cfg.LibsStagingDir} {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("wheel adapter wrote files: %v %v", entries, err)
				}
			}
		})
	}
}

func TestDesktopSharedWantedAndRepositoryBinding(t *testing.T) {
	e := newEnvFx(t, desktopFixture(t, nil), nil)
	e.writePins(e.quickPin())
	e.writeDesktop(e.fx.commit, "desk")
	e.waitDesktop("ready", "")
	// Shared SHA is requested once; dropping local pins cannot drop its marker.
	e.writePins([]pinRow{})
	waitUntil(t, 5*time.Second, "local pins removed", func() bool { e.m.mu.Lock(); defer e.m.mu.Unlock(); return len(e.m.pins) == 0 })
	if _, err := os.Stat(filepath.Join(e.fx.store, "wanted", e.fx.commit)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.fx.store, "approved", e.fx.commit), []byte("ssh://git@git.example.com/foreign.git"), 0644); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := e.packageGet(e.fx.commit, "desk", testKey); s != 404 {
		t.Fatalf("foreign approval: %d", s)
	}
	e.waitDesktop("pending", "")
	if err := os.WriteFile(filepath.Join(e.fx.store, "approved", e.fx.commit), []byte("ssh://git@git.example.com/org/scripts.git"), 0644); err != nil {
		t.Fatal(err)
	}
	e.waitDesktop("ready", "")
	// A different repository in the materialized desktop file cannot reuse it.
	b, err := os.ReadFile(filepath.Join(e.keys, "desktop-pins.json"))
	if err != nil {
		t.Fatal(err)
	}
	e.writeDesktopRaw(strings.Replace(string(b), "org/scripts.git", "foreign.git", 1))
	if s, _, _ := e.packageGet(e.fx.commit, "desk", testKey); s != 404 {
		t.Fatalf("repository mismatch: %d", s)
	}
	e.waitDesktop("invalid", "manifest_invalid")
}

func TestDesktopFailurePrecedesCachedApprovalAtHTTP(t *testing.T) {
	e := newEnvFx(t, desktopFixture(t, nil), nil)
	e.writeDesktop(e.fx.commit, "desk")
	e.waitDesktop("ready", "")
	failed := filepath.Join(e.fx.store, "failed", e.fx.commit)
	if err := os.MkdirAll(filepath.Dir(failed), 0755); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"not_on_default_branch", "fetch_failed"} {
		if err := os.WriteFile(failed, []byte(reason), 0644); err != nil {
			t.Fatal(err)
		}
		e.waitDesktop("invalid", reason)
		if s, _, _ := e.packageGet(e.fx.commit, "desk", testKey); s != 404 {
			t.Fatalf("failed marker overridden by approval/cache: %d", s)
		}
		if err := os.Remove(failed); err != nil {
			t.Fatal(err)
		}
		e.waitDesktop("ready", "")
	}
}

func TestDesktopEmptyToolsAndOptionalRequirements(t *testing.T) {
	fx := desktopFixture(t, func(f *fixture) {
		f.write("desk/jnpt-desktop.yaml", strings.Replace(strings.Replace(desktopManifest, "[kartei_find]", "[]", 1), "requirements: requirements.lock\n", "", 1))
		if err := os.Remove(filepath.Join(f.work, "desk", "requirements.lock")); err != nil {
			t.Fatal(err)
		}
	})
	e := newEnvFx(t, fx, nil)
	e.writeDesktop(fx.commit, "desk")
	e.waitDesktop("ready", "")
	if got := mustJSON(e.desktopMeta()[0]["declared_tools"]); got != "[]" {
		t.Fatalf("empty tool set: %s", got)
	}
	if s, _, _ := e.packageGet(fx.commit, "desk", testKey); s != 200 {
		t.Fatalf("optional lock: %d", s)
	}
}

func TestDesktopSHA256AtHTTP(t *testing.T) {
	f := &fixture{t: t, work: t.TempDir(), store: t.TempDir()}
	f.git(f.work, "init", "-q", "--object-format=sha256", ".")
	f.git(f.work, "config", "user.email", "test@example.com")
	f.git(f.work, "config", "user.name", "Test")
	f.write("desk/jnpt-desktop.yaml", strings.Replace(desktopManifest, "requirements: requirements.lock\n", "", 1))
	f.write("desk/main.py", "x")
	f.write("desk/python.zip", "zip")
	f.git(f.work, "add", "desk")
	f.git(f.work, "commit", "-q", "-m", "sha256")
	f.commit = f.git(f.work, "rev-parse", "HEAD")
	f.git(f.store, "init", "-q", "--bare", "--object-format=sha256", "store.git")
	f.git(f.store, "--git-dir=store.git", "fetch", "-q", f.work, "HEAD:refs/heads/main")
	if err := os.MkdirAll(filepath.Join(f.store, "approved"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.store, "approved", f.commit), []byte("ssh://git@git.example.com/org/scripts.git"), 0644); err != nil {
		t.Fatal(err)
	}
	e := newEnvFx(t, f, nil)
	e.writeDesktop(f.commit, "desk")
	e.waitDesktop("ready", "")
	s, _, b := e.packageGet(f.commit, "desk", testKey)
	if s != 200 || !bytes.Contains(b, []byte(`"algo":"sha256"`)) {
		t.Fatalf("sha256: %d", s)
	}
}

// Count actual Git commands, preserving the real executable and object store.
// Only the remote URL is redirected to the isolated test repository.
type desktopGitProbe struct {
	branchTestGit
	calls atomic.Int64
}

func (g *desktopGitProbe) Run(ctx context.Context, env []string, args ...string) error {
	g.calls.Add(1)
	return g.cmd(ctx, env, args...).Run()
}
func (g *desktopGitProbe) Output(ctx context.Context, env []string, args ...string) ([]byte, error) {
	g.calls.Add(1)
	return g.cmd(ctx, env, args...).Output()
}

func TestDesktopFetcherRechecksM7V2AtRealGitAdapter(t *testing.T) {
	for _, tc := range []struct{ name, wanted, url string }{
		{"M7", strings.Repeat("a", 40), "ext::KANARIE"},
		{"V2", "--help", "ssh://git@git.example.com/org/scripts.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := desktopFixture(t, nil)
			storeDir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(storeDir, "wanted"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(storeDir, "wanted", tc.wanted), nil, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(storeDir, "repo_url"), []byte(tc.url+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			key := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(key, []byte("test"), 0600); err != nil {
				t.Fatal(err)
			}
			g := &desktopGitProbe{branchTestGit: branchTestGit{remote: f.work}}
			fetcher.New(fetcher.Config{StoreDir: storeDir, KeyPath: key, TmpDir: t.TempDir(), Git: g}).Once(context.Background())
			if n := g.calls.Load(); n != 0 {
				t.Fatalf("untrusted %s reached real git: %d calls", tc.name, n)
			}
		})
	}
}

func TestDesktopPackageNoSymlinkAtHTTP(t *testing.T) {
	e := newEnvFx(t, desktopFixture(t, func(f *fixture) {
		if err := os.Symlink("main.py", filepath.Join(f.work, "desk", "link")); err != nil {
			t.Fatal(err)
		}
	}), nil)
	e.writeDesktop(e.fx.commit, "desk")
	if s, _, _ := e.packageGet(e.fx.commit, "desk", testKey); s != 404 {
		t.Fatalf("symlink package: %d", s)
	}
}

func TestDesktopRevocationDuringRealTreeWalkAtHTTP(t *testing.T) {
	for _, revoke := range []string{"approval", "pin"} {
		t.Run(revoke, func(t *testing.T) {
			e := newEnvFx(t, desktopFixture(t, nil), nil)
			e.writeDesktop(e.fx.commit, "desk")
			e.waitDesktop("ready", "")
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			gate := t.TempDir()
			entered := filepath.Join(gate, "entered")
			proceed := filepath.Join(gate, "proceed")
			// The real cat-file process waits after the first HTTP authorization.
			// This controls timing, not its bytes or result.
			wrapper := "#!/bin/sh\ncase \"$*\" in *\"cat-file --batch\"*) touch '" + entered + "'; while [ ! -e '" + proceed + "' ]; do sleep 0.01; done;; esac\nexec '" + realGit + "' \"$@\"\n"
			if err := os.WriteFile(filepath.Join(gate, "git"), []byte(wrapper), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", gate+string(os.PathListSeparator)+os.Getenv("PATH"))
			done := make(chan int, 1)
			go func() {
				r, _ := http.NewRequest("GET", e.srv.URL+"/desktop/package?commit="+e.fx.commit+"&path=desk", nil)
				r.Header.Set("Authorization", "Bearer "+testKey)
				resp, err := http.DefaultClient.Do(r)
				if err != nil {
					done <- 0
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				done <- resp.StatusCode
			}()
			// Always release the executable, including after a failed assertion.
			defer func() { _ = os.WriteFile(proceed, nil, 0600) }()
			waitUntil(t, 5*time.Second, "real Git waiting", func() bool { _, err := os.Stat(entered); return err == nil })
			p := filepath.Join(e.fx.store, "approved", e.fx.commit)
			if revoke == "pin" {
				p = filepath.Join(e.keys, "desktop-pins.json")
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(proceed, nil, 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case status := <-done:
				if status != 404 {
					t.Fatalf("revoked during tree walk: %d", status)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("package request did not finish")
			}
		})
	}
}
