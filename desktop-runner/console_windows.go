//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

var attachConsoleProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

const attachParentProcess = 0xFFFFFFFF // ATTACH_PARENT_PROCESS = (DWORD)-1

// attachConsole steht ganz am Anfang von main(). Scheitert irgendein Schritt, läuft der Prozess still
// weiter: Der Exit-Code trägt das Ergebnis, die Konsole ist nur Komfort.
func attachConsole() {
	stdout, _ := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	stderr, _ := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	plan := planConsole(os.Args[1:], handleUsable(stdout), handleUsable(stderr))
	if !plan.Attach {
		return
	}
	if attached, _, _ := attachConsoleProc.Call(attachParentProcess); attached == 0 {
		return
	}
	console, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	if plan.Stdout {
		os.Stdout = console
	}
	if plan.Stderr {
		os.Stderr = console
	}
}

// handleUsable: Ein GUI-Prozess ohne Umleitung bekommt NULL oder ungültige Standard-Handles.
func handleUsable(h windows.Handle) bool {
	if h == 0 || h == windows.InvalidHandle {
		return false
	}
	kind, err := windows.GetFileType(h)
	return err == nil && kind != windows.FILE_TYPE_UNKNOWN
}
