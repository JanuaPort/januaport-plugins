//go:build windows

package pipe

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func nativeListener(t *testing.T) *Listener {
	t.Helper()
	user, logon, err := currentSIDs()
	if err != nil {
		t.Fatal(err)
	}
	l, err := listenName(PipeName(user)+".test."+strconv.Itoa(os.Getpid()), user, logon)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func TestWindowsPipeDACLUndLabel(t *testing.T) {
	l := nativeListener(t)
	sd, err := windows.GetSecurityInfo(l.handle, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl.AceCount != 2 {
		t.Fatal("ACE-Menge", err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("DACL nicht geschützt", err)
	}
	user, logon, _ := currentSIDs()
	for i, s := range []string{user, logon} {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, uint32(i), &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		want, _ := windows.StringToSid(s)
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || ace.Mask != windows.FILE_GENERIC_READ|windows.FILE_WRITE_DATA || !windows.EqualSid(sid, want) {
			t.Fatal("ACE")
		}
	}
	if !strings.Contains(sd.String(), "(ML;;NWNR;;;ME)") {
		t.Fatal("Label fehlt", sd.String())
	}
	var flags uint32
	if err = windows.GetHandleInformation(l.handle, &flags); err != nil || flags&windows.HANDLE_FLAG_INHERIT != 0 {
		t.Fatal("vererbbarer Server-Handle", err)
	}
	name, _ := windows.UTF16PtrFromString(l.name)
	h, err := windows.CreateFile(name, windows.FILE_GENERIC_READ|windows.FILE_WRITE_DATA, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal("Medium-Client", err)
	}
	windows.CloseHandle(h)
}

func TestWindowsPipeBesetzt(t *testing.T) {
	l := nativeListener(t)
	user, logon, _ := currentSIDs()
	second, err := listenName(l.name, user, logon)
	if second != nil {
		second.Close()
	}
	if !errors.Is(err, ErrOccupied) {
		t.Fatal("zweite Instanz", err)
	}
}

func TestWindowsPipeLowILReadWrite(t *testing.T) {
	for _, access := range []uint32{windows.GENERIC_READ, windows.GENERIC_WRITE} {
		t.Run(fmt.Sprint(access), func(t *testing.T) {
			l := nativeListener(t)
			var token windows.Token
			if err := windows.DuplicateTokenEx(windows.GetCurrentProcessToken(), windows.TOKEN_ALL_ACCESS, nil, windows.SecurityImpersonation, windows.TokenPrimary, &token); err != nil {
				t.Fatal(err)
			}
			defer token.Close()
			sid, err := windows.StringToSid("S-1-16-4096")
			if err != nil {
				t.Fatal(err)
			}
			label := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{Sid: sid, Attributes: windows.SE_GROUP_INTEGRITY}}
			if err = windows.SetTokenInformation(token, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), uint32(unsafe.Sizeof(label))+uint32(sid.Len())); err != nil {
				t.Fatal(err)
			}
			exe, _ := os.Executable()
			cmd := exec.Command(exe, "-test.run=^TestWindowsPipeAccessHelper$", "--", l.name, strconv.FormatUint(uint64(access), 10))
			cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(token), HideWindow: true}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Low-IL muss ACCESS_DENIED liefern: %v %s", err, out)
			}
		})
	}
}

func TestWindowsPipeAccessHelper(t *testing.T) {
	args := os.Args
	index := -1
	for i, a := range args {
		if a == "--" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	name, _ := windows.UTF16PtrFromString(args[index+1])
	access, _ := strconv.ParseUint(args[index+2], 10, 32)
	h, err := windows.CreateFile(name, uint32(access), 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err == nil {
		windows.CloseHandle(h)
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("Access=%d: %v", access, err)
	}
}

func TestWindowsPipeFremdesKonto(t *testing.T) {
	l := nativeListener(t)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Anonymous logon has a different user SID and no matching logon SID.
	proc := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateAnonymousToken")
	r, _, err := proc.Call(uintptr(windows.CurrentThread()))
	if r == 0 {
		t.Fatal(err)
	}
	defer windows.RevertToSelf()
	name, _ := windows.UTF16PtrFromString(l.name)
	h, e := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if e == nil {
		windows.CloseHandle(h)
	}
	if !errors.Is(e, windows.ERROR_ACCESS_DENIED) {
		t.Fatal("fremdes Token", e)
	}
}

func TestWindowsClientPrueftEchtenServer(t *testing.T) {
	l := nativeListener(t)
	name, _ := windows.UTF16PtrFromString(l.name)
	h, err := windows.CreateFile(name, windows.FILE_GENERIC_READ|windows.FILE_WRITE_DATA, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	// The real server PID points at this unsigned test binary, never an approved runner.
	if err = checkServer(h); err != ErrDisturbance {
		t.Fatal("falsches Serverbild zugelassen", err)
	}
	if _, err = publisher(os.Args[0]); err == nil {
		t.Fatal("unsigniertes Bild zugelassen")
	}
}
