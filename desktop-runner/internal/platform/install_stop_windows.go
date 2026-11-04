package platform

import (
	"errors"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Stop only the installed image owned by this user, through the inspected handle.
func stopPanel(exe string) error {
	sid, err := userSID()
	if err != nil {
		return err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var failures []error
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(exe)) {
			continue
		}
		h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, entry.ProcessID)
		// Other accounts may be inaccessible. A disappeared process is already stopped.
		if e == windows.ERROR_ACCESS_DENIED || e == windows.ERROR_INVALID_PARAMETER {
			continue
		}
		if e != nil {
			failures = append(failures, e)
			continue
		}
		e = stopPanelHandle(h, exe, sid)
		windows.CloseHandle(h)
		if e != nil {
			failures = append(failures, e)
		}
	}
	if err != windows.ERROR_NO_MORE_FILES {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func stopPanelHandle(h windows.Handle, exe string, sid *windows.SID) error {
	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	u, err := token.GetTokenUser()
	token.Close()
	if err != nil {
		return err
	}
	if !u.User.Sid.Equals(sid) {
		return nil
	}
	path := make([]uint16, 32768)
	size := uint32(len(path))
	if err = windows.QueryFullProcessImageName(h, 0, &path[0], &size); err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(windows.UTF16ToString(path[:size])), filepath.Clean(exe)) {
		return nil
	}
	if err = windows.TerminateProcess(h, 0); err != nil {
		if state, _ := windows.WaitForSingleObject(h, 0); state == windows.WAIT_OBJECT_0 {
			return nil
		}
		return err
	}
	state, err := windows.WaitForSingleObject(h, 5000)
	if err != nil {
		return err
	}
	if state != windows.WAIT_OBJECT_0 {
		return windows.ERROR_TIMEOUT
	}
	return nil
}
