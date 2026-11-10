//go:build windows

package main

import (
	"errors"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// #1240 F-T, native Windows-CI: anmelden, zweite Anmeldung 1409, abmelden, wieder anmelden, an einem Fenster nur
// für Nachrichten. Der echte Tastendruck und die Prüfung „kein Fokusraub“ laufen nicht hier, sondern auf dem
// Testgerät (GATE/BOX); hier wird nichts gedrückt.

const hwndMessage = ^uintptr(2) // HWND_MESSAGE (-3): Fenster nur für Nachrichten, ohne Anzeige, ohne Fokus.

func messageOnlyWindow(t *testing.T) uintptr {
	t.Helper()
	instance, _, _ := getModuleHandle.Call(0)
	r, _, err := createWindowEx.Call(0, uintptr(unsafe.Pointer(wide("STATIC"))), 0, 0, 0, 0, 0, 0, hwndMessage, 0, instance, 0)
	if r == 0 {
		t.Skipf("Kein Fenster nur für Nachrichten möglich (kein Desktop oder keine Fensterstation): %v", err)
	}
	t.Cleanup(func() { _, _, _ = destroyWindow.Call(r) })
	return r
}

func TestWindowsHotkeyAnmeldenDoppeltAbmeldenNeu(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	if nativeLoadError != nil {
		t.Skipf("Windows-API nicht verfügbar: %v", nativeLoadError)
	}
	window := messageOnlyWindow(t)
	const key = "Strg+Alt+F12"
	if err := registerHotkey(window, 1, key); err != nil {
		if errors.Is(err, syscall.Errno(errHotkeyAlreadyRegistered)) {
			t.Skip("Strg+Alt+F12 ist auf dieser Maschine systemweit belegt; die Probe braucht ein freies Kürzel")
		}
		t.Fatalf("erste Anmeldung: %v", err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			_ = unregisterHotkey(window, 1)
		}
	})
	// Dasselbe Kürzel ein zweites Mal ist für das System belegt: genau der Fall „ein anderes Programm hält es“.
	err := registerHotkey(window, 2, key)
	if !errors.Is(err, syscall.Errno(errHotkeyAlreadyRegistered)) {
		t.Fatalf("zweite Anmeldung: %v, erwartet 1409", err)
	}
	if err = unregisterHotkey(window, 1); err != nil {
		t.Fatalf("Abmeldung: %v", err)
	}
	released = true
	// Nach der Abmeldung ist das Kürzel wieder frei.
	if err = registerHotkey(window, 3, key); err != nil {
		t.Fatalf("erneute Anmeldung: %v", err)
	}
	if err = unregisterHotkey(window, 3); err != nil {
		t.Fatalf("zweite Abmeldung: %v", err)
	}
	// Eine Abmeldung ohne Anmeldung ist ein Fehler, kein Absturz.
	if err = unregisterHotkey(window, 3); err == nil {
		t.Fatal("Abmeldung ohne Anmeldung meldet keinen Fehler")
	}
}

// Nur Strg+Alt+F1 bis F12 erreichen das System; alles andere scheitert vor dem Aufruf, ohne Anmeldung.
func TestWindowsHotkeyNurErlaubteTasten(t *testing.T) {
	if nativeLoadError != nil {
		t.Skipf("Windows-API nicht verfügbar: %v", nativeLoadError)
	}
	window := messageOnlyWindow(t)
	for _, key := range []string{"", "Strg+Alt+7", "Strg+Alt+Q", "Strg+Alt+F13", "Alt+F1", "Strg+Shift+F1", "strg+alt+f1"} {
		if err := registerHotkey(window, 9, key); err == nil {
			_ = unregisterHotkey(window, 9)
			t.Errorf("%q angemeldet", key)
		}
	}
}

// Der Tastenzustand liefert ohne Druck keinen Absturz und ist in der CI-Sitzung nicht gedrückt.
func TestWindowsModifiersDownLaeuft(t *testing.T) {
	if nativeLoadError != nil {
		t.Skipf("Windows-API nicht verfügbar: %v", nativeLoadError)
	}
	_ = modifiersDown()
}
