//go:build windows

package platform

import (
	"bufio"
	"context"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"golang.org/x/sys/windows"
)

func TestDPAPIUndACL(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if e := SecureRoot(root); e != nil {
		t.Fatal(e)
	}
	c := DPAPI{}
	raw := []byte("SECRET-CREDENTIAL")
	b, e := c.Seal(raw)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), string(raw)) {
		t.Fatal("Klartext")
	}
	v, e := c.Open(b)
	if e != nil || string(v) != string(raw) {
		t.Fatal(e)
	}
	b[len(b)/2] ^= 1
	if _, e = c.Open(b); e == nil {
		t.Fatal("DPAPI akzeptiert Manipulation")
	}
	if e = CheckPrivateACL(root); e != nil {
		t.Fatal(e)
	}
}
func TestWindowsNeustartInterrupted(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if e := SecureRoot(root); e != nil {
		t.Fatal(e)
	}
	st, e := client.OpenState(root, DPAPI{})
	if e != nil {
		t.Fatal(e)
	}
	if e = st.Mark(client.Result{JobID: "test", ManifestSeq: 1, Commit: "test"}, "started"); e != nil {
		t.Fatal(e)
	}
	st, e = client.OpenState(root, DPAPI{})
	if e != nil {
		t.Fatal(e)
	}
	if e = st.Recover(time.Now()); e != nil {
		t.Fatal(e)
	}
	if j := st.Journal(); len(j.Results) != 1 || j.Results[0].End != "interrupted" {
		t.Fatal(j)
	}
}
func TestWindowsJobObjectEnkelUndToken(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("RUN_TOKEN", "PRIVATE_TOKEN")
	p, e := Spawn(context.Background(), client.Launch{Dir: t.TempDir(), Exe: exe, Args: []string{"-test.run=^TestProcessHelper$", "--", "parent"}, Env: []string{"SystemRoot=" + os.Getenv("SystemRoot")}})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	done := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(p.Output()); done <- b }()
	// Parent writes the grandchild PID and then holds the output pipe open.
	time.Sleep(time.Second)
	if e = p.Kill(); e != nil {
		t.Fatal(e)
	}
	select {
	case b := <-done:
		if strings.Contains(string(b), "PRIVATE_TOKEN") {
			t.Fatal("Token geerbt")
		}
		fields := strings.Fields(string(b))
		if len(fields) < 1 {
			t.Fatalf("kein Enkel: %q", b)
		}
		pid, e := strconv.Atoi(fields[0])
		if e != nil {
			t.Fatal(e)
		}
		h, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if e == nil {
			defer windows.CloseHandle(h)
			s, e := windows.WaitForSingleObject(h, 3000)
			if e != nil || s != windows.WAIT_OBJECT_0 {
				t.Fatalf("Enkel lebt: %d %v", s, e)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Prozessbaum hält Pipe offen")
	}
}
func TestProcessHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	if os.Args[len(os.Args)-1] == "signal" {
		os.WriteFile(os.Getenv("CANARY_PATH"), []byte("started"), 0600)
	}
	if os.Args[len(os.Args)-1] == "parent" {
		exe, _ := os.Executable()
		cmd := exec.Command(exe, "-test.run=^TestProcessHelper$", "--", "grandchild")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if cmd.Start() != nil {
			os.Exit(2)
		}
		io.WriteString(os.Stdout, strconv.Itoa(cmd.Process.Pid)+" "+os.Getenv("RUN_TOKEN")+"\n")
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
func TestWindowsAufgabeOhneAdmin(t *testing.T) {
	if os.Getenv("DESKTOP_TASK_TEST") != "1" && !strings.Contains(flag.Lookup("test.run").Value.String(), "WindowsAufgabeOhneAdmin") {
		t.Skip("opt-in: echte Benutzeraufgabe")
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("Sonde muss ohne erhöhte Rechte laufen")
	}
	exe, _ := os.Executable()
	name := "JanuaPort-Desktop-Test-" + strconv.Itoa(os.Getpid())
	if e := registerTask(name, exe); e != nil {
		t.Fatalf("COM error: %#v", e)
	}
	defer removeTask(name)
	if level, e := taskRunLevel(name); e != nil || level != 0 {
		t.Fatal("LeastPrivilege", level, e)
	}
	xml, e := taskXML(name)
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"InteractiveToken", "IgnoreNew", "PT0S", "PT1M", "<Count>3</Count>", "<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>"} {
		if !strings.Contains(xml, want) {
			t.Fatal("Einstellung fehlt", want)
		}
	}
}

func TestWindowsSuspendBisJobZuordnung(t *testing.T) {
	exe, _ := os.Executable()
	canary := filepath.Join(t.TempDir(), "canary")
	p, e := spawn(context.Background(), client.Launch{Dir: t.TempDir(), Exe: exe, Args: []string{"-test.run=^TestProcessHelper$", "--", "signal"}, Env: append(WindowsEnv(), "CANARY_PATH="+canary)}, func(windows.ProcessInformation) error {
		time.Sleep(200 * time.Millisecond)
		if _, e := os.Stat(canary); !os.IsNotExist(e) {
			return errPlatform
		}
		return nil
	})
	if e != nil {
		t.Fatal("vor Job-Zuordnung ausgeführt", e)
	}
	defer p.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(canary); e == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("nach Zuordnung nicht fortgesetzt")
}
func TestWindowsJobCloseBeendetEnkel(t *testing.T) {
	exe, _ := os.Executable()
	child, e := Spawn(context.Background(), client.Launch{Dir: t.TempDir(), Exe: exe, Args: []string{"-test.run=^TestProcessHelper$", "--", "parent"}, Env: WindowsEnv()})
	if e != nil {
		t.Fatal(e)
	}
	p := child.(*process)
	defer p.Close()
	line, e := bufio.NewReader(p.Output()).ReadString('\n')
	if e != nil {
		t.Fatal(e)
	}
	pid, e := strconv.Atoi(strings.Fields(line)[0])
	if e != nil {
		t.Fatal(e)
	}
	grand, e := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(grand)
	defer windows.TerminateProcess(grand, 1)
	defer windows.TerminateProcess(p.proc, 1)
	if e = windows.CloseHandle(p.job); e != nil {
		t.Fatal(e)
	}
	status, e := windows.WaitForSingleObject(grand, 3000)
	if e != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatal("Enkel überlebt Job-Close", e)
	}
}
func TestWindowsDPAPIBrauchtEntropie(t *testing.T) {
	b, e := (DPAPI{}).Seal([]byte("secret"))
	if e != nil {
		t.Fatal(e)
	}
	in := blob(b)
	var out windows.DataBlob
	e = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if e == nil {
		windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
		t.Fatal("ohne Entropie lesbar")
	}
}
