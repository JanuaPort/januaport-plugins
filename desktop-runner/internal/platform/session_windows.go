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
		return out, e
	}
	var buffer *uint32
	var n uint32
	ok, _, _ := wtsQuery.Call(0, uintptr(session), 8, uintptr(unsafe.Pointer(&buffer)), uintptr(unsafe.Pointer(&n))) // WTSConnectState
	if ok == 0 || buffer == nil {
		return out, errPlatform
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buffer)))
	if n < 4 {
		return out, errPlatform
	}
	state := *buffer
	out.Active = state == 0 // WTSActive includes locked sessions, excludes disconnected.
	sid, e := userSID()
	if e != nil {
		return out, e
	}
	name, domain, _, e := sid.LookupAccount("")
	if e != nil {
		return out, e
	}
	out.Account = domain + "\\" + name
	input := struct {
		Size uint32
		Tick uint32
	}{Size: 8}
	ok, _, _ = lastInput.Call(uintptr(unsafe.Pointer(&input)))
	if ok == 0 {
		return out, errPlatform
	}
	ticks, _, _ := tickCount.Call()
	out.Idle = time.Duration(uint32(ticks)-input.Tick) * time.Millisecond
	return out, nil
}
