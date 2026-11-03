//go:build windows

package pipe

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsServerPrueftClientbildUndReconnect(t *testing.T) {
	l := nativeListener(t)
	exe, _ := os.Executable()
	client := filepath.Join(filepath.Dir(exe), "jnpt-bedienfeld.exe")
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(client, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(client)
	for _, valid := range []bool{false, true, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		program := exe
		if valid {
			program = client
		}
		cmd := exec.CommandContext(ctx, program, "-test.run=^TestWindowsTransportClientHelper$", "--", l.name, strconv.FormatBool(valid))
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		var output strings.Builder
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err = cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		c, acceptErr := l.Accept(ctx)
		if valid {
			if acceptErr != nil {
				cancel()
				cmd.Wait()
				t.Fatal(acceptErr)
			}
			raw, err := ReadFrame(c)
			if err != nil {
				t.Fatal(err)
			}
			if m, err := DecodeClient(raw); err != nil || m.Type != "status" {
				t.Fatal(m, err)
			}
			if err = WriteFrame(c, Message{Type: "fehler", Code: "abgewiesen"}); err != nil {
				t.Fatal(err)
			}
			if _, err = ReadFrame(c); err != nil {
				t.Fatal("Client hat Antwort nicht gelesen", err)
			}
			// A pending read must be cancelled by Close, and the first handle
			// must still own the namespace after each disconnect.
			readDone := make(chan struct{})
			go func() { ReadFrame(c); close(readDone) }()
			c.Close()
			<-readDone
		} else if acceptErr == nil {
			c.Close()
			cancel()
			cmd.Wait()
			t.Fatal("fremdes Clientbild zugelassen")
		}
		if err = cmd.Wait(); err != nil {
			cancel()
			t.Fatal(err, output.String())
		}
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.Accept(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Accept-Abbruch hängt")
	}
}

func TestWindowsTransportClientHelper(t *testing.T) {
	index := -1
	for i, a := range os.Args {
		if a == "--" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	name, _ := windows.UTF16PtrFromString(os.Args[index+1])
	valid := os.Args[index+2] == "true"
	h, err := windows.CreateFile(name, windows.FILE_GENERIC_READ|windows.FILE_WRITE_DATA, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(h), "native-test-client")
	defer f.Close()
	writeErr := WriteFrame(f, Message{Type: "status"})
	raw, readErr := ReadFrame(f)
	if valid {
		m, err := DecodeServer(raw)
		if writeErr != nil || readErr != nil || err != nil || m.Code != "abgewiesen" {
			t.Fatal(writeErr, readErr, err, m)
		}
		WriteFrame(f, Message{Type: "status"})
	} else if readErr == nil {
		t.Fatal("Clientbild nicht abgewiesen")
	}
}

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
	result, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation").Call(uintptr(l.handle), uintptr(unsafe.Pointer(&flags)))
	if result == 0 || flags&windows.HANDLE_FLAG_INHERIT != 0 {
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

func TestWindowsPipeClientDarfKeineInstanzAnlegen(t *testing.T) {
	l := nativeListener(t)
	name, _ := windows.UTF16PtrFromString(l.name)
	// FILE_APPEND_DATA is the pipe CREATE_INSTANCE right. Do not accidentally
	// grant it through GENERIC_WRITE, even to the pipe's user SID.
	h, err := windows.CreateFile(name, windows.FILE_APPEND_DATA, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err == nil {
		windows.CloseHandle(h)
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatal("CREATE_INSTANCE-Recht", err)
	}
}

func TestWindowsPipeRemoteAbgewiesen(t *testing.T) {
	l := nativeListener(t)
	name, _ := windows.UTF16PtrFromString(strings.Replace(l.name, `\\.\`, `\\localhost\`, 1))
	h, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err == nil {
		windows.CloseHandle(h)
		t.Fatal("Remote-Client zugelassen")
	}
	if errors.Is(err, windows.ERROR_BAD_NETPATH) || errors.Is(err, windows.ERROR_BAD_NET_NAME) {
		t.Skip("lokaler SMB-Transport nicht verfügbar; Flag-Wächter bleibt verpflichtend")
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatal("Remote-Abweisung", err)
	}
}

func TestWindowsPipeLowILReadWrite(t *testing.T) {
	for _, access := range []uint32{windows.GENERIC_READ, windows.GENERIC_WRITE} {
		t.Run(fmt.Sprint(access), func(t *testing.T) {
			l := nativeListener(t)
			exe, _ := os.Executable()
			cmd := exec.Command(exe, "-test.run=^TestWindowsPipeAccessHelper$", "--", l.name, strconv.FormatUint(uint64(access), 10), "low")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
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
	// Drop the helper process itself to Low-IL before any pipe open. This needs
	// no AssignPrimaryToken privilege and never lowers the test runner's token.
	if len(args) <= index+3 || args[index+3] != "low" {
		t.Fatal("Low-IL helper argument")
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_QUERY, &token); err != nil {
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
	var size uint32
	buf := make([]byte, 1024)
	if err = windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buf[0], uint32(len(buf)), &size); err != nil {
		t.Fatal(err)
	}
	actual := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0]))
	if !windows.EqualSid(actual.Label.Sid, sid) {
		t.Fatal("Helper ist nicht Low-IL")
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

func TestWindowsPipeFremdesToken(t *testing.T) {
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

func TestWindowsPipeFremdesKonto(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("Windows-CI: temporäres fremdes Konto braucht erhöhte Testrechte")
	}
	l := nativeListener(t)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("jp_pipe_%x", random[:6]))
	password, _ := windows.UTF16PtrFromString(base64.RawURLEncoding.EncodeToString(random[:]) + "aA1!")
	type userInfo struct {
		Name, Password         *uint16
		PasswordAge, Privilege uint32
		Home, Comment          *uint16
		Flags                  uint32
		Script                 *uint16
	}
	info := userInfo{Name: name, Password: password, Privilege: 1, Flags: 0x201} // ordinary local user, UF_SCRIPT | UF_NORMAL_ACCOUNT
	netapi := windows.NewLazySystemDLL("netapi32.dll")
	var parameter uint32
	status, _, _ := netapi.NewProc("NetUserAdd").Call(0, 1, uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&parameter)))
	if status != 0 {
		t.Fatalf("NetUserAdd status=%d parameter=%d", status, parameter)
	}
	defer func() {
		status, _, _ := netapi.NewProc("NetUserDel").Call(0, uintptr(unsafe.Pointer(name)))
		if status != 0 {
			t.Errorf("NetUserDel status=%d", status)
		}
	}()
	var token windows.Token
	domain, _ := windows.UTF16PtrFromString(".")
	advapi := windows.NewLazySystemDLL("advapi32.dll")
	result, _, err := advapi.NewProc("LogonUserW").Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(domain)), uintptr(unsafe.Pointer(password)), 2, 0, uintptr(unsafe.Pointer(&token)))
	if result == 0 {
		t.Fatal("LogonUser", err)
	}
	defer token.Close()
	foreign, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	own, _, err := currentSIDs()
	if err != nil || foreign.User.Sid.String() == own {
		t.Fatal("kein fremdes Konto", err)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result, _, err = advapi.NewProc("ImpersonateLoggedOnUser").Call(uintptr(token))
	if result == 0 {
		t.Fatal(err)
	}
	defer windows.RevertToSelf()
	pipeName, _ := windows.UTF16PtrFromString(l.name)
	for _, access := range []uint32{windows.GENERIC_READ, windows.GENERIC_WRITE} {
		h, err := windows.CreateFile(pipeName, access, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			windows.CloseHandle(h)
		}
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Fatalf("fremdes Konto access=%d: %v", access, err)
		}
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
