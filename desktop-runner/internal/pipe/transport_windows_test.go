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
	token := standardPipeToken(t)
	// Preserve the negative probe against the parent runner's actual descriptor,
	// including an elevated server/Administrators owner on CI.
	t.Run("production_server", func(t *testing.T) {
		pipeInstanceRights(t, token, []bool{false})
	})
	// Both positive-control endpoints must originate in a standard PROCESS.
	// Thread impersonation left the first server endpoint in the elevated runner.
	t.Run("standard_process", func(t *testing.T) {
		out, err := runPipeRightsHelper(t, token, false)
		t.Log(string(out))
		if err != nil || !strings.Contains(string(out), "PIPE_RIGHTS_HELPER_OK") {
			t.Fatalf("standard process control: %v", err)
		}
	})
}

func runPipeRightsHelper(t *testing.T, token windows.Token, restricting bool) ([]byte, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	mode := "pipe-rights-helper"
	if restricting {
		mode = "pipe-rights-restricted-helper"
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsPipeRightsHelper$", "-test.v", "--", mode)
	// Go implements SysProcAttr.Token with CreateProcessAsUserW. Do not fall
	// back to an ordinary (possibly elevated) child if this call fails.
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(token), HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.CombinedOutput()
}

func TestWindowsPipeRestrictingSIDsVerweigert(t *testing.T) {
	token := standardPipeToken(t)
	out, err := runPipeRightsHelper(t, token, true)
	if err == nil || !strings.Contains(string(out), "1 restricting SIDs would add a second access check") || strings.Contains(string(out), "PIPE_RIGHTS_HELPER_OK") {
		t.Fatalf("helper must reject restricting SIDs before testing pipe access: %v %s", err, out)
	}
}

func pipeRestrictedControlToken(t *testing.T, token windows.Token) windows.Token {
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	restrict := windows.SIDAndAttributes{Sid: world}
	var restricted windows.Token
	ok, _, err := windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken").Call(uintptr(token), 0, 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&restrict)), uintptr(unsafe.Pointer(&restricted)))
	if ok == 0 {
		t.Fatal("restricted control token", err)
	}
	t.Cleanup(func() { restricted.Close() })
	if count := pipeRestrictingSIDCount(t, restricted); count != 1 {
		t.Fatalf("restricting control SID count=%d", count)
	}
	return restricted
}

func TestWindowsPipeRightsHelper(t *testing.T) {
	if len(os.Args) < 2 {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode != "pipe-rights-helper" && mode != "pipe-rights-restricted-helper" {
		return
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	// Check the child's primary token before creating ANY listener. Checking
	// only an impersonation token would repeat the original CI probe defect.
	assertStandardPipeToken(t, token)
	if mode == "pipe-rights-restricted-helper" {
		token = pipeRestrictedControlToken(t, token)
		assertStandardPipeToken(t, token)
	}
	pipeInstanceRights(t, token, []bool{false, true})
	if !t.Failed() {
		fmt.Println("PIPE_RIGHTS_HELPER_OK")
	}
}

func pipeInstanceRights(t *testing.T, token windows.Token, widths []bool) {
	l := nativeListener(t)
	// Use the descriptor actually installed by the production listener. A second
	// production instance (maxInstances=1) would also fail with a BROKEN DACL.
	sd, err := windows.GetSecurityInfo(l.handle, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, _, _ := currentSIDs()
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("server_elevated=%t owner_is_user=%t", windows.GetCurrentProcessToken().IsElevated(), owner.String() == user)
	// INBOUND isolates the create-instance bit from the other GENERIC_WRITE
	// bits needed for a DUPLEX server. Test both, with the real and user owner.
	for _, access := range []uint32{windows.PIPE_ACCESS_INBOUND, windows.PIPE_ACCESS_DUPLEX} {
		for _, ownerUser := range []bool{false, true} {
			for _, broad := range widths {
				t.Run(fmt.Sprintf("access=%d/force_user_owner=%t/broad=%t", access, ownerUser, broad), func(t *testing.T) {
					descriptor := sd.String()
					controlMask := uint32(windows.FILE_GENERIC_READ | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA)
					if access == windows.PIPE_ACCESS_DUPLEX {
						controlMask |= windows.FILE_GENERIC_WRITE
					}
					if broad {
						// Positive control: add the instance bit, and for DUPLEX
						// also the remaining write rights needed by a server endpoint.
						// No copied grants or token defaults: one direct user ACE,
						// explicit group/owner and the same Medium label as production.
						descriptor = fmt.Sprintf("O:%sG:%sD:P(A;;0x%x;;;%s)S:(ML;;NWNR;;;ME)", owner.String(), user, controlMask, user)
					}
					fixture, err := windows.SecurityDescriptorFromString(descriptor)
					if err != nil {
						t.Fatal(err)
					}
					if ownerUser {
						fixture, err = fixture.ToAbsolute()
						if err != nil {
							t.Fatal(err)
						}
						sid, err := windows.StringToSid(user)
						if err != nil {
							t.Fatal(err)
						}
						if err = fixture.SetOwner(sid, false); err != nil {
							t.Fatal(err)
						}
					}
					sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: fixture}
					name, _ := windows.UTF16PtrFromString(l.name + ".rights")
					const mode = windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS
					first, err := windows.CreateNamedPipe(name, access|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, mode, 2, MaxFrame, MaxFrame, 0, &sa)
					if err != nil {
						t.Fatal("first fixture instance", err)
					}
					defer windows.CloseHandle(first)
					installed, err := windows.GetSecurityInfo(first, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
					if err != nil {
						t.Fatal("fixture security readback", err)
					}
					if !strings.Contains(installed.String(), "(ML;;NWNR;;;ME)") {
						t.Fatal("fixture is not explicitly Medium NWNR")
					}
					installedOwner, _, err := installed.Owner()
					if err != nil {
						t.Fatal(err)
					}
					wantOwner := owner.String()
					if ownerUser {
						wantOwner = user
					}
					wantSID, err := windows.StringToSid(wantOwner)
					if err != nil || !windows.EqualSid(installedOwner, wantSID) {
						t.Fatal("fixture owner mismatch", err)
					}
					if broad {
						acl, _, err := installed.DACL()
						if err != nil || acl.AceCount != 1 {
							t.Fatal("control DACL", err)
						}
						var ace *windows.ACCESS_ALLOWED_ACE
						if err := windows.GetAce(acl, 0, &ace); err != nil {
							t.Fatal(err)
						}
						userSID, err := windows.StringToSid(user)
						if err != nil {
							t.Fatal(err)
						}
						if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || uint32(ace.Mask) != controlMask || !windows.EqualSid((*windows.SID)(unsafe.Pointer(&ace.SidStart)), userSID) {
							t.Fatal("control user grant mismatch")
						}
					}
					// The second creation must not fall back to the elevated process's
					// default descriptor while impersonating a standard client. Give
					// this endpoint an explicit user owner/group and Medium label too.
					clientSD, err := windows.SecurityDescriptorFromString(fixture.String())
					if err != nil {
						t.Fatal(err)
					}
					clientSD, err = clientSD.ToAbsolute()
					if err != nil {
						t.Fatal(err)
					}
					clientSID, err := windows.StringToSid(user)
					if err != nil {
						t.Fatal(err)
					}
					if err := clientSD.SetOwner(clientSID, false); err != nil {
						t.Fatal(err)
					}
					if err := clientSD.SetGroup(clientSID, false); err != nil {
						t.Fatal(err)
					}
					clientSA := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: clientSD}
					withPipeToken(t, token, func() {
						if access == windows.PIPE_ACCESS_DUPLEX && !broad {
							// Diagnose the original CI probe separately: opening a CLIENT
							// endpoint is not evidence that another SERVER can be created.
							client, openErr := windows.CreateFile(name, windows.FILE_APPEND_DATA, 0, nil, windows.OPEN_EXISTING, 0, 0)
							t.Logf("client_open_append_error=%v", openErr)
							if openErr == nil {
								windows.CloseHandle(client)
								if err := windows.DisconnectNamedPipe(first); err != nil {
									t.Fatal(err)
								}
							}
						}
						// This really creates another SERVER endpoint, with a free slot.
						second, err := windows.CreateNamedPipe(name, access, mode, 2, MaxFrame, MaxFrame, 0, &clientSA)
						if err == nil {
							windows.CloseHandle(second)
						}
						if broad {
							if err != nil {
								t.Fatal("positive control cannot create instance", err)
							}
						} else if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
							t.Fatal("CREATE_INSTANCE-Recht", err)
						}
					})
				})
			}
		}
	}
}

// Even an elevated CI runner must exercise the DACL with a standard Medium
// token. LUA_TOKEN removes administrative groups; DISABLE_MAX_PRIVILEGE removes
// privileges. RestrictedSidCount=0: no second access check against restricting
// SIDs. Verify both the source and result; inherited restrictions are not safe
// evidence that the pipe DACL denied an otherwise authorized standard user.
func standardPipeToken(t *testing.T) windows.Token {
	var source, token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ADJUST_PRIVILEGES, &source); err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if count := pipeRestrictingSIDCount(t, source); count != 0 {
		t.Fatalf("source already has %d restricting SIDs", count)
	}
	admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	disable := windows.SIDAndAttributes{Sid: admin}
	advapi := windows.NewLazySystemDLL("advapi32.dll")
	const disableMaxPrivilege, luaToken = 1, 4
	ok, _, err := advapi.NewProc("CreateRestrictedToken").Call(uintptr(source), disableMaxPrivilege|luaToken, 1, uintptr(unsafe.Pointer(&disable)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&token)))
	if ok == 0 {
		t.Fatal("CreateRestrictedToken", err)
	}
	t.Cleanup(func() { token.Close() })
	t.Logf("source_restricting_sids=%d filtered_restricting_sids=%d", pipeRestrictingSIDCount(t, source), pipeRestrictingSIDCount(t, token))
	medium, _ := windows.StringToSid("S-1-16-8192")
	label := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{Sid: medium, Attributes: windows.SE_GROUP_INTEGRITY}}
	if err = windows.SetTokenInformation(token, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), uint32(unsafe.Sizeof(label))+uint32(medium.Len())); err != nil {
		t.Fatal(err)
	}
	// DISABLE_MAX_PRIVILEGE retains SeChangeNotifyPrivilege; disable that too.
	if err = windows.AdjustTokenPrivileges(token, true, nil, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
	original, err := source.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := token.GetTokenUser()
	if err != nil || !windows.EqualSid(original.User.Sid, actual.User.Sid) {
		t.Fatal("client user changed", err)
	}
	assertStandardPipeToken(t, token)
	return token
}

func assertStandardPipeToken(t *testing.T, token windows.Token) {
	t.Helper()
	if count := pipeRestrictingSIDCount(t, token); count != 0 {
		t.Fatalf("%d restricting SIDs would add a second access check", count)
	}
	medium, err := windows.StringToSid("S-1-16-8192")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	var size uint32
	if err = windows.GetTokenInformation(token, windows.TokenElevation, &buf[0], 4, &size); err != nil {
		t.Fatal(err)
	}
	if *(*uint32)(unsafe.Pointer(&buf[0])) != 0 {
		t.Fatal("client token is elevated")
	}
	if err = windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buf[0], uint32(len(buf)), &size); err != nil {
		t.Fatal(err)
	}
	if !windows.EqualSid((*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0])).Label.Sid, medium) {
		t.Fatal("client token is not Medium")
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups.AllGroups() {
		if windows.EqualSid(g.Sid, admin) && (g.Attributes&windows.SE_GROUP_ENABLED != 0 || g.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY == 0) {
			t.Fatal("admin SID must be deny-only")
		}
	}
	if err = windows.GetTokenInformation(token, windows.TokenPrivileges, &buf[0], uint32(len(buf)), &size); err != nil {
		t.Fatal(err)
	}
	for _, p := range (*windows.Tokenprivileges)(unsafe.Pointer(&buf[0])).AllPrivileges() {
		if p.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
			t.Fatal("enabled client privilege")
		}
	}
	t.Log("token_elevated=false integrity=Medium admin=absent_or_deny_only privileges_enabled=0 restricting_sids=0")
}

func withPipeToken(t *testing.T, token windows.Token, probe func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ok, _, err := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateLoggedOnUser").Call(uintptr(token))
	if ok == 0 {
		t.Fatal("ImpersonateLoggedOnUser", err)
	}
	defer func() {
		if err := windows.RevertToSelf(); err != nil {
			panic(err)
		}
	}()
	probe()
}

func pipeRestrictingSIDCount(t *testing.T, token windows.Token) uint32 {
	t.Helper()
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenRestrictedSids, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		t.Fatal("restricted SID size", err)
	}
	buf := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenRestrictedSids, &buf[0], size, &size); err != nil {
		t.Fatal("restricted SIDs", err)
	}
	return (*windows.Tokengroups)(unsafe.Pointer(&buf[0])).GroupCount
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
