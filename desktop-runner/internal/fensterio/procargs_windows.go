//go:build windows

package fensterio

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// readProcessParams liest Kommandozeile und Umgebung eines Prozesses des eigenen Kontos aus dessen PEB. Die
// Umgebung kommt nur für die Sonden mit; der Adapter braucht allein die Kommandozeile.
func readProcessParams(pid uint32, withEnv bool) (cmdline, env string, err error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return "", "", err
	}
	defer windows.CloseHandle(h)
	var info windows.PROCESS_BASIC_INFORMATION
	if err = windows.NtQueryInformationProcess(h, 0, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return "", "", err
	}
	read := func(addr uintptr, dst unsafe.Pointer, size uintptr) error {
		return windows.ReadProcessMemory(h, addr, (*byte)(dst), size, nil)
	}
	var peb windows.PEB
	if err = read(uintptr(unsafe.Pointer(info.PebBaseAddress)), unsafe.Pointer(&peb), unsafe.Sizeof(peb)); err != nil {
		return "", "", err
	}
	var params windows.RTL_USER_PROCESS_PARAMETERS
	if err = read(uintptr(unsafe.Pointer(peb.ProcessParameters)), unsafe.Pointer(&params), unsafe.Sizeof(params)); err != nil {
		return "", "", err
	}
	cmd := make([]uint16, int(params.CommandLine.Length)/2)
	if len(cmd) > 0 {
		if err = read(uintptr(unsafe.Pointer(params.CommandLine.Buffer)), unsafe.Pointer(&cmd[0]), uintptr(len(cmd)*2)); err != nil {
			return "", "", err
		}
	}
	var envText []rune
	if withEnv && params.EnvironmentSize > 0 && params.EnvironmentSize < 1<<20 {
		raw := make([]uint16, params.EnvironmentSize/2)
		if err = read(uintptr(params.Environment), unsafe.Pointer(&raw[0]), params.EnvironmentSize); err != nil {
			return "", "", err
		}
		for _, c := range raw {
			envText = append(envText, rune(c))
		}
	}
	return windows.UTF16ToString(cmd), string(envText), nil
}

// processCommandLine liest die Kommandozeile des Browserprozesses, den die WebView gestartet hat.
func processCommandLine(pid uint32) (string, error) {
	cmd, _, err := readProcessParams(pid, false)
	return cmd, err
}
