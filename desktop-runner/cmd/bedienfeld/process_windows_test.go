//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Native CI only: inspect the actual production executable's PEB while
// suspended, then resume it. No probe switch or environment hook in production.
// The existing Windows job runs this test via go test ./....
func TestWindowsBedienfeldProzesssonde(t *testing.T) {
	compare := windows.NewLazySystemDLL("kernelbase.dll").NewProc("CompareObjectHandles")
	if err := compare.Find(); err != nil {
		t.Skipf("Handle-Identitätsprüfung nicht verfügbar: kernelbase.dll!CompareObjectHandles: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "jnpt-bedienfeld.exe")
	build := exec.Command("go", "build", "-ldflags=-H=windowsgui", "-o", exe, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	event, err := windows.CreateEvent(&sa, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	for _, inherit := range []bool{false, true} {
		t.Run(map[bool]string{false: "ohne_Vererbung", true: "Gegenprobe"}[inherit], func(t *testing.T) {
			var env []uint16
			for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "PATH"} {
				if v := os.Getenv(key); v != "" {
					env = append(env, windows.StringToUTF16(key+"="+v)...)
				}
			}
			env = append(env, 0)
			line := windows.StringToUTF16(`"` + exe + `"`)
			si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
			var pi windows.ProcessInformation
			if err := windows.CreateProcess(nil, &line[0], nil, nil, inherit, windows.CREATE_SUSPENDED|windows.CREATE_UNICODE_ENVIRONMENT, &env[0], nil, &si, &pi); err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(pi.Thread)
			defer windows.CloseHandle(pi.Process)
			defer windows.TerminateProcess(pi.Process, 0)
			var info windows.PROCESS_BASIC_INFORMATION
			if err := windows.NtQueryInformationProcess(pi.Process, 0, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil); err != nil {
				t.Fatal(err)
			}
			read := func(addr uintptr, dst unsafe.Pointer, size uintptr) {
				t.Helper()
				if err := windows.ReadProcessMemory(pi.Process, addr, (*byte)(dst), size, nil); err != nil {
					t.Fatal(err)
				}
			}
			var peb windows.PEB
			read(uintptr(unsafe.Pointer(info.PebBaseAddress)), unsafe.Pointer(&peb), unsafe.Sizeof(peb))
			var params windows.RTL_USER_PROCESS_PARAMETERS
			read(uintptr(unsafe.Pointer(peb.ProcessParameters)), unsafe.Pointer(&params), unsafe.Sizeof(params))
			cmd := make([]uint16, int(params.CommandLine.Length)/2)
			if len(cmd) == 0 {
				t.Fatal("leere Kommandozeile")
			}
			read(uintptr(unsafe.Pointer(params.CommandLine.Buffer)), unsafe.Pointer(&cmd[0]), uintptr(len(cmd)*2))
			if strings.Trim(windows.UTF16ToString(cmd), `"`) != exe {
				t.Fatal("Argumente am Bedienfeld")
			}
			if params.EnvironmentSize == 0 || params.EnvironmentSize > 1<<20 {
				t.Fatal("ungültige Env-Größe")
			}
			remoteEnv := make([]uint16, params.EnvironmentSize/2)
			read(uintptr(params.Environment), unsafe.Pointer(&remoteEnv[0]), params.EnvironmentSize)
			if strings.Contains(strings.ToLower(string(utf16Runes(remoteEnv))), "jnpt") {
				t.Fatal("jnpt in Umgebung")
			}
			var dup windows.Handle
			err := windows.DuplicateHandle(pi.Process, event, windows.CurrentProcess(), &dup, 0, false, windows.DUPLICATE_SAME_ACCESS)
			found := false
			if err == nil {
				r, _, _ := compare.Call(uintptr(event), uintptr(dup))
				found = r != 0
				windows.CloseHandle(dup)
			}
			if found != inherit {
				t.Fatal("Vererbungs-Sonde/Gegenprobe", found, inherit)
			}
			if !inherit {
				if _, err := windows.ResumeThread(pi.Thread); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func utf16Runes(v []uint16) []rune {
	// Keep embedded NUL separators: UTF16ToString would inspect only entry one.
	r := make([]rune, len(v))
	for i, c := range v {
		r[i] = rune(c)
	}
	return r
}
