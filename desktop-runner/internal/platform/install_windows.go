package platform

import (
	"errors"
	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
	"strings"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValue = "JanuaPortDesktopLaeufer"
const panelRunValue = "JanuaPortBedienfeld"

func taskName() (string, error) {
	sid, e := userSID()
	if e != nil {
		return "", e
	}
	return "JanuaPort-Desktop-" + sid.String(), nil
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
	return registerAutostartTask(name, exe, false)
}
func registerAutostartTask(name, exe string, panel bool) error {
	sid, e := userSID()
	if e != nil {
		return e
	}
	return registerTaskXML(name, taskDefinition(exe, sid.String(), panel), sid.String())
}
func registerTaskXML(name, definition, sid string) error {
	return withTaskFolder(func(folder *ole.IDispatch) error {
		v, e := oleutil.CallMethod(folder, "RegisterTask", name, definition, 6, nil, nil, 3, "D:P(A;;FA;;;"+sid+")")
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

type nativeAutostart struct {
	name, value string
	panel       bool
}

func (n nativeAutostart) Register(exe string) error {
	return registerAutostartTask(n.name, exe, n.panel)
}
func (n nativeAutostart) RemoveTask() error         { return removeTask(n.name) }
func (n nativeAutostart) PolicyDenied(e error) bool { return policyError(e) }
func (n nativeAutostart) SetRun(exe string) error {
	k, _, e := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer k.Close()
	return k.SetStringValue(n.value, commandLine(exe, n.panel))
}
func commandLine(exe string, panel bool) string {
	if panel {
		return windows.EscapeArg(exe)
	}
	return windows.EscapeArg(exe) + " run"
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
	e = k.DeleteValue(n.value)
	if e == registry.ErrNotExist {
		return nil
	}
	return e
}
func (n nativeAutostart) Snapshot() (func() error, error) {
	x, err := taskXML(n.name)
	if err != nil && comCode(err) != 0x80070002 {
		return nil, err
	}
	sid, err := userSID()
	if err != nil {
		return nil, err
	}
	var run string
	var present bool
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil && err != registry.ErrNotExist {
		return nil, err
	}
	if err == nil {
		run, _, err = k.GetStringValue(n.value)
		k.Close()
		if err != nil && err != registry.ErrNotExist {
			return nil, err
		}
		present = err == nil
	}
	return func() error {
		var taskErr error
		if x == "" {
			taskErr = n.RemoveTask()
		} else {
			taskErr = registerTaskXML(n.name, x, sid.String())
		}
		var runErr error
		if present {
			key, _, e := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
			if e == nil {
				e = key.SetStringValue(n.value, run)
				key.Close()
			}
			runErr = e
		} else {
			runErr = n.RemoveRun()
		}
		return errors.Join(taskErr, runErr)
	}, nil
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
	return installPair(nativeAutostart{name: name, value: runValue}, nativeAutostart{name: panelTaskName(name), value: panelRunValue, panel: true}, exe)
}
func panelTaskName(runnerTask string) string {
	return strings.Replace(runnerTask, "JanuaPort-Desktop-", "JanuaPort-Bedienfeld-", 1)
}
func Uninstall() error {
	name, e := taskName()
	if e != nil {
		return e
	}
	removeErr := uninstallPair(nativeAutostart{name: name, value: runValue}, nativeAutostart{name: panelTaskName(name), value: panelRunValue, panel: true})
	exe, e := os.Executable()
	if e != nil {
		return errors.Join(removeErr, e)
	}
	return errors.Join(removeErr, stopPanel(filepath.Join(filepath.Dir(exe), "jnpt-bedienfeld.exe")))
}
func Installed() bool {
	name, e := taskName()
	return e == nil && installedEntry(name, runValue) && installedEntry(panelTaskName(name), panelRunValue)
}
func installedEntry(name, value string) bool {
	if _, e := taskXML(name); e == nil {
		return true
	}
	k, e := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if e != nil {
		return false
	}
	defer k.Close()
	v, _, e := k.GetStringValue(value)
	return e == nil && v != "" && (value != runValue || strings.HasSuffix(v, " run"))
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
