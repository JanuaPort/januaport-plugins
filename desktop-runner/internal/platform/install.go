package platform

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
)

// Autostart is the seam used by Linux CI; the Windows implementation uses COM
// and HKCU directly. Fallback is limited to access/policy denial.
type Autostart interface {
	Register(string) error
	RemoveTask() error
	SetRun(string) error
	RemoveRun() error
	PolicyDenied(error) bool
}

func InstallWith(a Autostart, exe string) (bool, error) {
	if err := a.Register(exe); err != nil {
		if !a.PolicyDenied(err) {
			return false, err
		}
		return true, a.SetRun(exe)
	}
	return false, a.RemoveRun()
}
func UninstallWith(a Autostart) error {
	first := a.RemoveTask()
	second := a.RemoveRun()
	if first != nil {
		return first
	}
	return second
}

// ErrPanelMissing is mapped to the CLI's fixed E1 diagnostic, never a path.
var ErrPanelMissing = errors.New("panel_missing")

type pairedAutostart interface {
	Autostart
	Snapshot() (func() error, error)
}

func installPair(runner, panel pairedAutostart, exe string) (bool, error) {
	panelExe := filepath.Join(filepath.Dir(exe), "jnpt-bedienfeld.exe")
	info, err := os.Stat(panelExe)
	if err != nil || !info.Mode().IsRegular() {
		return false, ErrPanelMissing
	}
	restoreRunner, err := runner.Snapshot()
	if err != nil {
		return false, err
	}
	restorePanel, err := panel.Snapshot()
	if err != nil {
		return false, err
	}
	fallback, err := InstallWith(runner, exe)
	if err == nil {
		var panelFallback bool
		panelFallback, err = InstallWith(panel, panelExe)
		fallback = fallback || panelFallback
	}
	if err != nil {
		return false, errors.Join(err, restorePanel(), restoreRunner())
	}
	return fallback, nil
}

func uninstallPair(runner, panel Autostart) error {
	return errors.Join(UninstallWith(runner), UninstallWith(panel))
}

func escape(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
func taskDefinition(exe, account string, panel ...bool) string {
	arguments := "<Arguments>run</Arguments>"
	if len(panel) > 0 && panel[0] {
		arguments = ""
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
<RegistrationInfo><Description>JanuaPort Desktop-Läufer</Description></RegistrationInfo>
<Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + escape(account) + `</UserId></LogonTrigger></Triggers>
<Principals><Principal id="User"><UserId>` + escape(account) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
<Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><AllowHardTerminate>true</AllowHardTerminate><StartWhenAvailable>true</StartWhenAvailable><RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable><IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings><AllowStartOnDemand>true</AllowStartOnDemand><Enabled>true</Enabled><Hidden>false</Hidden><RunOnlyIfIdle>false</RunOnlyIfIdle><WakeToRun>false</WakeToRun><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><Priority>7</Priority><RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure></Settings>
<Actions Context="User"><Exec><Command>` + escape(exe) + `</Command>` + arguments + `</Exec></Actions></Task>`
}
