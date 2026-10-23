package platform

import (
	"bytes"
	"encoding/xml"
	"errors"
	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"strings"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValue = "JanuaPortDesktopLaeufer"

func taskName() (string, error) {
	sid, e := userSID()
	if e != nil {
		return "", e
	}
	return "JanuaPort-Desktop-" + sid.String(), nil
}
func escape(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
func taskDefinition(exe, account string) string {
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
<RegistrationInfo><Description>JanuaPort Desktop-Läufer</Description></RegistrationInfo>
<Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + escape(account) + `</UserId></LogonTrigger></Triggers>
<Principals><Principal id="User"><UserId>` + escape(account) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
<Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><AllowHardTerminate>true</AllowHardTerminate><StartWhenAvailable>true</StartWhenAvailable><RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable><IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings><AllowStartOnDemand>true</AllowStartOnDemand><Enabled>true</Enabled><Hidden>false</Hidden><RunOnlyIfIdle>false</RunOnlyIfIdle><WakeToRun>false</WakeToRun><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><Priority>7</Priority><RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure></Settings>
<Actions Context="User"><Exec><Command>` + escape(exe) + `</Command><Arguments>run</Arguments></Exec></Actions></Task>`
}

// Schedule.Service is the COM ITaskService coclass, not schtasks or a shell.
func withTaskFolder(fn func(*ole.IDispatch) error) error {
	unlock := pinThread()
	defer unlock()
	if e := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); e != nil {
		if o, ok := e.(*ole.OleError); !ok || o.Code() != 1 {
			return e
		}
	}
	defer ole.CoUninitialize()
	obj, e := oleutil.CreateObject("Schedule.Service")
	if e != nil {
		return e
	}
	defer obj.Release()
	svc, e := obj.QueryInterface(ole.IID_IDispatch)
	if e != nil {
		return e
	}
	defer svc.Release()
	v, e := oleutil.CallMethod(svc, "Connect")
	if e != nil {
		return e
	}
	v.Clear()
	folder, e := oleutil.CallMethod(svc, "GetFolder", `\`)
	if e != nil {
		return e
	}
	defer folder.Clear()
	return fn(folder.ToIDispatch())
}
func registerTask(name, exe string) error {
	sid, e := userSID()
	if e != nil {
		return e
	}
	return withTaskFolder(func(folder *ole.IDispatch) error {
		v, e := oleutil.CallMethod(folder, "RegisterTask", name, taskDefinition(exe, sid.String()), 6, nil, nil, 3, "D:P(A;;FA;;;"+sid.String()+")")
		if v != nil {
			v.Clear()
		}
		return e
	})
}
func removeTask(name string) error {
	return withTaskFolder(func(f *ole.IDispatch) error {
		v, e := oleutil.CallMethod(f, "DeleteTask", name, 0)
		if v != nil {
			v.Clear()
		}
		if comCode(e) == 0x80070002 {
			return nil
		}
		return e
	})
}
func taskXML(name string) (string, error) {
	var out string
	e := withTaskFolder(func(f *ole.IDispatch) error {
		v, e := oleutil.CallMethod(f, "GetTask", name)
		if e != nil {
			return e
		}
		defer v.Clear()
		x, e := oleutil.GetProperty(v.ToIDispatch(), "Xml")
		if e != nil {
			return e
		}
		defer x.Clear()
		out = x.ToString()
		return nil
	})
	return out, e
}
func comCode(e error) uintptr {
	var o *ole.OleError
	if !errors.As(e, &o) {
		return 0
	}
	if ex, ok := o.SubError().(ole.EXCEPINFO); ok {
		return uintptr(ex.SCODE())
	}
	return o.Code()
}
func policyError(e error) bool { c := comCode(e); return c == 0x80070005 || c == 0x800704ec }

type nativeAutostart struct{ name string }

func (n nativeAutostart) Register(exe string) error { return registerTask(n.name, exe) }
func (n nativeAutostart) RemoveTask() error         { return removeTask(n.name) }
func (n nativeAutostart) PolicyDenied(e error) bool { return policyError(e) }
func (n nativeAutostart) SetRun(exe string) error {
	k, _, e := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer k.Close()
	return k.SetStringValue(runValue, windows.EscapeArg(exe)+" run")
}
func (n nativeAutostart) RemoveRun() error {
	k, e := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if e == registry.ErrNotExist {
		return nil
	}
	if e != nil {
		return e
	}
	defer k.Close()
	e = k.DeleteValue(runValue)
	if e == registry.ErrNotExist {
		return nil
	}
	return e
}
func Install() (bool, error) {
	name, e := taskName()
	if e != nil {
		return false, e
	}
	exe, e := os.Executable()
	if e != nil {
		return false, e
	}
	return InstallWith(nativeAutostart{name}, exe)
}
func Uninstall() error {
	name, e := taskName()
	if e != nil {
		return e
	}
	return UninstallWith(nativeAutostart{name})
}
func Installed() bool {
	name, e := taskName()
	if e == nil {
		if _, e = taskXML(name); e == nil {
			return true
		}
	}
	k, e := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if e != nil {
		return false
	}
	defer k.Close()
	v, _, e := k.GetStringValue(runValue)
	return e == nil && strings.HasSuffix(v, " run")
}

func taskRunLevel(name string) (int64, error) {
	var out int64
	err := withTaskFolder(func(f *ole.IDispatch) error {
		task, err := oleutil.CallMethod(f, "GetTask", name)
		if err != nil {
			return err
		}
		defer task.Clear()
		def, err := oleutil.GetProperty(task.ToIDispatch(), "Definition")
		if err != nil {
			return err
		}
		defer def.Clear()
		p, err := oleutil.GetProperty(def.ToIDispatch(), "Principal")
		if err != nil {
			return err
		}
		defer p.Clear()
		level, err := oleutil.GetProperty(p.ToIDispatch(), "RunLevel")
		if err != nil {
			return err
		}
		defer level.Clear()
		out = level.Val
		return nil
	})
	return out, err
}
