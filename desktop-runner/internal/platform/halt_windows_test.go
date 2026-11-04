//go:build windows

package platform

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
	"golang.org/x/sys/windows"
)

// The test helper emits a PID, not bootstrap frames. Consume that handshake
// inside Spawn, then keep Execute's frame reader blocked until job teardown.
type haltNativeProcess struct {
	client.Process
	reader *io.PipeReader
	writer *io.PipeWriter
}

func (p *haltNativeProcess) Output() io.ReadCloser { return p.reader }
func (p *haltNativeProcess) Close() error {
	p.writer.Close()
	p.reader.Close()
	return p.Process.Close()
}

func TestWindowsAnhaltenBeendetEnkel(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := SecureRoot(root); err != nil {
		t.Fatal(err)
	}
	st, err := client.OpenState(root, DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte("name: demo\ndescription: test\nentry: main.py\ninput_schema: {type: object}\ntools: []\ntimeout_s: 10\npython: python.zip\n")
	commit, raw := testfixture.Package(map[string][]byte{"main.py": []byte("pass"), "jnpt-desktop.yaml": manifest}, "")
	pin := client.Pin{Pin: "demo", Commit: commit, Path: "demo", Algo: "sha256", DeclaredTools: []string{}, Schedules: []client.Schedule{}}
	// Prepare's runtime allowlist is independently tested; this fixture supplies
	// a sealed inert environment and substitutes only the process executable.
	dir := commit + "-" + client.RunID("demo")
	envRoot := filepath.Join(root, "envs", dir)
	if err := os.MkdirAll(envRoot, 0700); err != nil {
		t.Fatal(err)
	}
	seal, err := client.Snapshot(envRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "packages", dir), raw, 0600); err != nil {
		t.Fatal(err)
	}
	env := client.Environment{Origin: pin, Dir: dir, Entry: "main.py", Timeout: 10, Tools: []string{}, Files: seal}
	cfg := client.Config{Contract: client.WindowsContract, ExpectedAccount: "own"}
	cfg.StartPolicy.Mode = "always"
	pid := make(chan uint32, 1)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e := &client.Engine{State: st, Config: cfg, Session: func() (client.Session, error) { return client.Session{Active: true, Account: "own"}, nil }}
	e.Spawn = func(ctx context.Context, l client.Launch) (client.Process, error) {
		l.Exe, l.Args, l.Env = exe, []string{"-test.run=^TestProcessHelper$", "--", "parent"}, WindowsEnv()
		p, err := Spawn(ctx, l)
		if err != nil {
			return nil, err
		}
		line, err := bufio.NewReader(p.Output()).ReadString('\n')
		if err != nil {
			p.Close()
			return nil, err
		}
		n, err := strconv.Atoi(strings.Fields(line)[0])
		if err != nil {
			p.Close()
			return nil, err
		}
		r, w := io.Pipe()
		pid <- uint32(n)
		return &haltNativeProcess{p, r, w}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan client.Result, 1)
	go func() {
		done <- e.Execute(ctx, client.Result{RunKey: "demo/daily/2026-11-03T10:00:00Z", ManifestSeq: 1, Commit: commit}, env, json.RawMessage(`{}`), "", 10, 0)
	}()
	var grandPID uint32
	select {
	case grandPID = <-pid:
	case got := <-done:
		t.Fatal("kein Prozessbaum", got)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	grand, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, grandPID)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(grand)
	defer windows.TerminateProcess(grand, 1)
	if err := e.Halt("angehalten", time.Time{}); err != nil {
		t.Fatal(err)
	}
	status, err := windows.WaitForSingleObject(grand, 3000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatal("Enkel überlebt Halt", status, err)
	}
	select {
	case got := <-done:
		if got.End != "cancelled" {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	raw, err = os.ReadFile(filepath.Join(root, "halt"))
	if err != nil || strings.Contains(string(raw), "angehalten") {
		t.Fatal("kein DPAPI-Halt", err)
	}
	st, err = client.OpenState(root, DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	restarted := &client.Engine{State: st}
	if state, _, fault := restarted.HaltStatus(); state != "angehalten" || fault {
		t.Fatal(state, fault)
	}
}
