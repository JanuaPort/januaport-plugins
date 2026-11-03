package platform

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
		if err != nil || (n.panel && v != commandLine(panel, true)) {
			t.Fatal("Run-Argument", v, err)
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
