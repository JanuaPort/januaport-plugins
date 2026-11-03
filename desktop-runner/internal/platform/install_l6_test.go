package platform

import (
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBedienfeldAufgabeOhneArgumente(t *testing.T) {
	for _, panel := range []bool{false, true} {
		x := taskDefinition(`C:\Install & Test\jnpt-bedienfeld.exe`, "S-1-5-21-123", panel)
		var task struct {
			Actions struct {
				Exec struct{ Command, Arguments string }
			}
		}
		if err := xml.Unmarshal([]byte(strings.Replace(x, "UTF-16", "UTF-8", 1)), &task); err != nil {
			t.Fatal(err)
		}
		want := "run"
		if panel {
			want = ""
		}
		if task.Actions.Exec.Command != `C:\Install & Test\jnpt-bedienfeld.exe` || task.Actions.Exec.Arguments != want {
			t.Fatal("Startvertrag", x)
		}
	}
}

type pairAutostart struct {
	task, run, fail string
	registered      int
}

func (a *pairAutostart) Register(exe string) error {
	a.registered++
	if a.fail == "register" || a.fail == "policy" {
		return errors.New("register")
	}
	a.task = exe
	return nil
}
func (a *pairAutostart) SetRun(exe string) error { a.run = exe; return nil }
func (a *pairAutostart) RemoveTask() error       { a.task = ""; return nil }
func (a *pairAutostart) RemoveRun() error {
	a.run = ""
	if a.fail == "cleanup" {
		return errors.New("cleanup")
	}
	return nil
}
func (a *pairAutostart) PolicyDenied(error) bool { return a.fail == "policy" }
func (a *pairAutostart) Snapshot() (func() error, error) {
	task, run := a.task, a.run
	return func() error { a.task, a.run = task, run; return nil }, nil
}

func TestInstallBeideOderVorherigerStand(t *testing.T) {
	for _, failure := range []string{"", "register", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "desktop-runner.exe")
			panel := filepath.Join(dir, "jnpt-bedienfeld.exe")
			if err := os.WriteFile(panel, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			a := &pairAutostart{task: "old-runner", run: "old-run"}
			b := &pairAutostart{task: "old-panel", run: "old-panel-run", fail: failure}
			_, err := installPair(a, b, exe)
			if failure == "" {
				if err != nil || a.task != exe || b.task != panel || a.run != "" || b.run != "" {
					t.Fatalf("beide Aufgaben: %+v %+v %v", a, b, err)
				}
			} else if err == nil || a.task != "old-runner" || a.run != "old-run" || b.task != "old-panel" || b.run != "old-panel-run" {
				t.Fatalf("halber Updatezustand: %+v %+v %v", a, b, err)
			}
		})
	}
}

func TestInstallOhneBedienfeldAendertNichts(t *testing.T) {
	for _, directory := range []bool{false, true} {
		dir := t.TempDir()
		if directory {
			if err := os.Mkdir(filepath.Join(dir, "jnpt-bedienfeld.exe"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		a, b := &pairAutostart{}, &pairAutostart{}
		if _, err := installPair(a, b, filepath.Join(dir, "desktop-runner.exe")); !errors.Is(err, ErrPanelMissing) {
			t.Fatal("fehlende Exe angenommen", err)
		}
		if a.registered != 0 || b.registered != 0 {
			t.Fatal("Registrierung vor Dateiprüfung")
		}
	}
}

func TestUninstallBeideAuchBeiFehler(t *testing.T) {
	a := &pairAutostart{task: "runner", run: "runner", fail: "cleanup"}
	b := &pairAutostart{task: "panel", run: "panel"}
	if err := uninstallPair(a, b); err == nil {
		t.Fatal("Fehler verschluckt")
	}
	if a.task != "" || a.run != "" || b.task != "" || b.run != "" {
		t.Fatal("Autostart übrig", a, b)
	}
}

func TestInstallBeideMitRichtlinienRueckfall(t *testing.T) {
	dir := t.TempDir()
	panel := filepath.Join(dir, "jnpt-bedienfeld.exe")
	if err := os.WriteFile(panel, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	a, b := &pairAutostart{fail: "policy"}, &pairAutostart{fail: "policy"}
	exe := filepath.Join(dir, "desktop-runner.exe")
	fallback, err := installPair(a, b, exe)
	if err != nil || !fallback || a.run != exe || b.run != panel {
		t.Fatal("beide Run-Einträge", a, b, fallback, err)
	}
}
