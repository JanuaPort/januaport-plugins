package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Called only by the existing explicit standard-account task probe. Never local.
func testWindowsL6Installation(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "desktop-runner.exe")
	panel := filepath.Join(dir, "jnpt-bedienfeld.exe")
	name := "JanuaPort-L6-Test-" + strconv.Itoa(os.Getpid())
	a := nativeAutostart{name: name, value: name}
	b := nativeAutostart{name: name + "-Panel", value: name + "-Panel", panel: true}
	defer uninstallPair(a, b)
	// Real adapter: absent panel must fail before either COM/Run registration.
	if _, err := installPair(a, b, exe); err != ErrPanelMissing {
		t.Fatal(err)
	}
	for _, n := range []nativeAutostart{a, b} {
		if _, err := taskXML(n.name); comCode(err) != 0x80070002 {
			t.Fatal("Aufgabe trotz fehlender Exe", err)
		}
	}
	if err := os.WriteFile(panel, []byte("never executed fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := installPair(a, b, exe); err != nil {
		t.Fatal(err)
	}
	for _, n := range []nativeAutostart{a, b} {
		x, err := taskXML(n.name)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"LogonTrigger", "InteractiveToken", "LeastPrivilege"} {
			if !strings.Contains(x, want) {
				t.Fatal(want, x)
			}
		}
		if n.panel && strings.Contains(x, "<Arguments>") && !strings.Contains(x, "<Arguments></Arguments>") {
			t.Fatal("Bedienfeld mit Argument", x)
		}
		// Real HKCU adapter, including no arguments and exact path.
		if err := n.SetRun(panel); err != nil {
			t.Fatal(err)
		}
		k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		v, _, err := k.GetStringValue(n.value)
		k.Close()
		if err != nil || (n.panel && v != windows.EscapeArg(panel)) {
			t.Fatal("Run-Argument", v, err)
		}
	}
	// Snapshot/restore uses real COM and HKCU and must retain both old entries.
	for _, n := range []nativeAutostart{a, b} {
		restore, err := n.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		before, err := taskXML(n.name)
		if err != nil {
			t.Fatal(err)
		}
		if err = UninstallWith(n); err != nil {
			t.Fatal(err)
		}
		if err = restore(); err != nil {
			t.Fatal(err)
		}
		after, err := taskXML(n.name)
		if err != nil || after != before {
			t.Fatal("Aufgabe nicht wiederhergestellt", err)
		}
		k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		v, _, err := k.GetStringValue(n.value)
		k.Close()
		if err != nil || v != commandLine(panel, n.panel) {
			t.Fatal("Run nicht wiederhergestellt", err)
		}
	}
	if err := uninstallPair(a, b); err != nil {
		t.Fatal(err)
	}
	for _, n := range []nativeAutostart{a, b} {
		if _, err := taskXML(n.name); comCode(err) != 0x80070002 {
			t.Fatal("Aufgabe übrig", err)
		}
		k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = k.GetStringValue(n.value)
		k.Close()
		if err != registry.ErrNotExist {
			t.Fatal("Run übrig", err)
		}
	}
}

func TestWindowsUninstallBeendetNurEigenesBedienfeld(t *testing.T) {
	// Test executables sleep without GUI. No task or Run entry is created.
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var children []*exec.Cmd
	var paths []string
	for i := 0; i < 2; i++ {
		path := filepath.Join(t.TempDir(), "jnpt-bedienfeld.exe")
		if err = os.WriteFile(path, bytes, 0700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(path, "-test.run=^TestProcessHelper$", "--", "grandchild")
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer cmd.Process.Kill()
		children = append(children, cmd)
		paths = append(paths, path)
	}
	hOther, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(children[1].Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(hOther)
	otherSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	if err = stopPanelHandle(hOther, paths[1], otherSID); err != nil {
		t.Fatal(err)
	}
	if state, err := windows.WaitForSingleObject(hOther, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("fremde Benutzerbindung beendet", state, err)
	}
	if err = stopPanel(paths[0]); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- children[0].Wait() }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("Bedienfeld überlebt uninstall")
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(children[1].Process.Pid))
	if err != nil {
		t.Fatal("anderer Bildpfad beendet", err)
	}
	defer windows.CloseHandle(h)
	if state, err := windows.WaitForSingleObject(h, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("anderer Bildpfad beendet", state, err)
	}
	children[1].Process.Kill()
	children[1].Wait()
}
