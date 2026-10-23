package platform

import (
	"context"
	"encoding/json"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWindowsPythonEchterStartUndManipulation(t *testing.T) {
	py, e := os.ReadFile("../../.probe/python-3.12.10-embed-amd64.zip")
	if os.IsNotExist(e) {
		t.Skip("kuratierte ZIP in .probe erforderlich")
	}
	if e != nil {
		t.Fatal(e)
	}
	tmp, e := os.MkdirTemp("../../.probe", "run-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(tmp)
	root, _ := filepath.Abs(tmp)
	if e = SecureRoot(root); e != nil {
		t.Fatal(e)
	}
	st, e := client.OpenState(root, DPAPI{})
	if e != nil {
		t.Fatal(e)
	}
	script := []byte("import os,sys,januaport\nassert 'PRIVATE_TOKEN' not in str(dict(os.environ))+str(sys.argv)\nassert 'site' not in sys.modules\nprint('PRIVATE_STDOUT')\njanuaport.log('PRIVATE_LOG')\nr=januaport.call('demo_tool',{})\nassert r['content']==[]\n")
	manifest := []byte("name: demo\ndescription: test\nentry: main.py\ninput_schema: {type: object}\ntools: [demo_tool]\ntimeout_s: 10\npython: python-3.12.10-embed-amd64.zip\n")
	commit, raw := testfixture.Package(map[string][]byte{"main.py": script, "jnpt-desktop.yaml": manifest, "python-3.12.10-embed-amd64.zip": py}, "")
	pin := client.Pin{Pin: "demo", Commit: commit, Path: "demo", Algo: "sha256", DeclaredTools: []string{"demo_tool"}, Schedules: []client.Schedule{}}
	env, e := client.Prepare(filepath.Join(root, "envs"), raw, pin)
	if e != nil {
		t.Fatal(e)
	}
	os.Mkdir(filepath.Join(root, "packages"), 0700)
	os.WriteFile(filepath.Join(root, "packages", env.Dir), raw, 0600)
	var bodies []string
	var delayed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "mcp") {
			if delayed.Load() {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(10 * time.Second):
				}
			}
			if r.Header.Get("X-Jnpt-Run-Token") != "PRIVATE_TOKEN" {
				t.Error("MCP Token fehlt")
			}
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	api, e := client.NewAPI(client.Trust{URL: server.URL}, "jnpt_test_secret", st.Revoke)
	if e != nil {
		t.Fatal(e)
	}
	cfg := client.Config{ExpectedAccount: "test"}
	cfg.StartPolicy.Mode = "always"
	starts := 0
	engine := client.Engine{State: st, API: api, Config: cfg, Session: func() (client.Session, error) { return client.Session{Active: true, Account: "test"}, nil }, ChildEnv: WindowsEnv(), Spawn: func(ctx context.Context, l client.Launch) (client.Process, error) { starts++; return Spawn(ctx, l) }}
	result := client.Result{RunKey: "demo/daily/2026-10-23T10:00:00Z", ManifestSeq: 1, Commit: commit}
	got := engine.Execute(context.Background(), result, env, json.RawMessage(`{"private_input":1}`), "PRIVATE_TOKEN", 10, 1)
	if got.End != "ok" || starts != 1 {
		t.Fatalf("positive Kontrolle: %+v starts=%d", got, starts)
	}
	if e = st.Append(got); e != nil {
		t.Fatal(e)
	}
	if e = st.Deliver(func(r client.Result) error { return api.Report(context.Background(), r) }); e != nil {
		t.Fatal(e)
	}
	for _, b := range bodies {
		for _, secret := range []string{"PRIVATE_STDOUT", "PRIVATE_LOG", "PRIVATE_TOKEN", "private_input", "idle", "account", "session"} {
			if strings.Contains(b, secret) {
				t.Fatal("Datenabfluss", secret)
			}
		}
	}
	file := filepath.Join(root, "envs", env.Dir, "pin", "main.py")
	if e = os.WriteFile(file, []byte("raise Exception()"), 0600); e != nil {
		t.Fatal(e)
	}
	got = engine.Execute(context.Background(), result, env, json.RawMessage(`{}`), "PRIVATE_TOKEN", 10, 1)
	if got.End != "refused" || got.Detail != "hash" || starts != 1 {
		t.Fatalf("Manipulation: %+v starts=%d", got, starts)
	}
	if e = os.WriteFile(file, script, 0600); e != nil {
		t.Fatal(e)
	}
	delayed.Store(true)
	for _, kind := range []string{"timeout", "cancelled", "limit"} {
		ctx, cancel := context.WithCancel(context.Background())
		wall, calls := 1, 1
		if kind == "cancelled" {
			wall = 10
			time.AfterFunc(200*time.Millisecond, cancel)
		}
		if kind == "limit" {
			wall = 10
			calls = 0
		}
		got = engine.Execute(ctx, result, env, json.RawMessage("{}"), "PRIVATE_TOKEN", wall, calls)
		cancel()
		if got.End != kind {
			t.Fatalf("%s: %+v", kind, got)
		}
		if e = st.Append(got); e != nil {
			t.Fatal(e)
		}
	}

}
