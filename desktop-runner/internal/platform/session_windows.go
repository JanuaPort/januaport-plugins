package platform

import (
	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"golang.org/x/sys/windows"
	"time"
	"unsafe"
)

var wtsQuery = windows.NewLazySystemDLL("wtsapi32.dll").NewProc("WTSQuerySessionInformationW")
var lastInput = windows.NewLazySystemDLL("user32.dll").NewProc("GetLastInputInfo")
var tickCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount")

func Session() (client.Session, error) {
	var out client.Session
	var session uint32
	if e := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); e != nil {
		return out, winError("ProcessIdToSessionId", e)
	}
	var buffer *uint32
	var n uint32
	ok, _, callErr := wtsQuery.Call(0, uintptr(session), 8, uintptr(unsafe.Pointer(&buffer)), uintptr(unsafe.Pointer(&n))) // WTSConnectState
	if ok == 0 || buffer == nil {
		return out, winError("WTSQuerySessionInformation", callErr)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buffer)))
	if n < 4 {
		return out, winError("WTSResponseSize", nil)
	}
	state := *buffer
	out.Active = state == 0 // WTSActive includes locked sessions, excludes disconnected.
	sid, e := userSID()
	if e != nil {
		return out, e
	}
	name, domain, _, e := sid.LookupAccount("")
	if e != nil {
		return out, winError("LookupAccountSid", e)
	}
	out.Account = domain + "\\" + name
	input := struct {
		Size uint32
		Tick uint32
	}{Size: 8}
	ok, _, callErr = lastInput.Call(uintptr(unsafe.Pointer(&input)))
	if ok == 0 {
		return out, winError("GetLastInputInfo", callErr)
	}
	ticks, _, _ := tickCount.Call()
	out.Idle = time.Duration(uint32(ticks)-input.Tick) * time.Millisecond
	return out, nil
}
