//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Written only during package initialization, before the UI or pipe goroutine.
// A missing API leaves the initial model in Störung: no connection can clear it.
var nativeLoadError error

type nativeDLL struct {
	dll *syscall.DLL
	err error
}

type nativeProc struct {
	proc *syscall.Proc
	err  error
}

func systemDLL(name string) nativeDLL {
	load := syscall.NewLazyDLL("kernel32.dll").NewProc("LoadLibraryExW")
	if err := load.Find(); err != nil {
		return nativeDLL{err: err}
	}
	h, _, err := load.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(name))), 0, 0x00000800) // LOAD_LIBRARY_SEARCH_SYSTEM32
	if h == 0 {
		return nativeDLL{err: err}
	}
	return nativeDLL{dll: &syscall.DLL{Name: name, Handle: syscall.Handle(h)}}
}

func (d nativeDLL) proc(name string) nativeProc {
	p := nativeProc{err: d.err}
	if p.err == nil {
		p.proc, p.err = d.dll.FindProc(name)
	}
	if p.err != nil {
		nativeLoadError = p.err
	}
	return p
}

func (p nativeProc) Call(args ...uintptr) (uintptr, uintptr, error) {
	if p.err != nil {
		return 0, 0, p.err
	}
	return p.proc.Call(args...)
}
