//go:build windows

package main

import (
	"errors"
	"syscall"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fensterio"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/panel"
)

// Tastenkürzel für Setups (#1240 F-T). Das ist die einzige Datei, die RegisterHotKey und UnregisterHotKey kennt
// (TestHotkeyWaechter*). Sie meldet an und ab und fragt den Tastenzustand; entschieden wird allein im Modell:
// WM_HOTKEY ruft nur panel.Model.PressHotkey, der Start folgt im Zeitgeber (PollHotkey) über Choose. Hier wird nie
// gewartet (kein Sleep im Nachrichtenzweig), kein Fenster geöffnet und kein Fokus gesetzt.

var (
	registerHotKey   = user.proc("RegisterHotKey")
	unregisterHotKey = user.proc("UnregisterHotKey")
	getAsyncKeyState = user.proc("GetAsyncKeyState")
	messageBeep      = user.proc("MessageBeep")
)

const (
	wmHotkey = 0x312

	modAlt      = 0x1
	modControl  = 0x2
	modNoRepeat = 0x4000 // Ein gehaltenes Kürzel löst nur einmal aus.
	// Genau Strg+Alt und MOD_NOREPEAT; Shift und Win gibt es nicht (TestHotkeyWaechterNoRepeatUndFesteModifier).
	hotkeyModifiers = modAlt | modControl | modNoRepeat

	vkF1                       = 0x70
	errHotkeyAlreadyRegistered = 1409

	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12 // Alt
	vkLWin    = 0x5b
	vkRWin    = 0x5c
)

var (
	errHotkeyKey    = errors.New("Kürzel nicht erlaubt")
	errHotkeyFailed = errors.New("Kürzel nicht anmeldbar")
)

// registerHotkey meldet Strg+Alt+F<n> an. Nur die zwölf erlaubten Tasten erreichen das System.
func registerHotkey(window uintptr, id int, key string) error {
	n, ok := panel.HotkeyNumber(key)
	if !ok {
		return errHotkeyKey
	}
	r, _, err := registerHotKey.Call(window, uintptr(id), hotkeyModifiers, uintptr(vkF1+n-1))
	return failure(r, err, errHotkeyFailed)
}

func unregisterHotkey(window uintptr, id int) error {
	r, _, err := unregisterHotKey.Call(window, uintptr(id))
	return failure(r, err, errHotkeyFailed)
}

// failure liefert den Windows-Fehler (zum Beispiel 1409), nie nil bei Misserfolg.
func failure(r uintptr, err, fallback error) error {
	if r != 0 {
		return nil
	}
	if e, ok := err.(syscall.Errno); !ok || e == 0 {
		return fallback
	}
	return err
}

// modifiersDown meldet, ob Strg, Alt, Shift oder Win gedrückt sind. Fehlt die API, gilt es als gedrückt: Das Modell
// weist dann ab, statt Tasten in das Fachprogramm zu schicken.
func modifiersDown() bool {
	if getAsyncKeyState.err != nil {
		return true
	}
	for _, vk := range []uintptr{vkControl, vkMenu, vkShift, vkLWin, vkRWin} {
		if state, _, _ := getAsyncKeyState.Call(vk); state&0x8000 != 0 {
			return true
		}
	}
	return false
}

// initHotkeys gibt dem Modell den Tastenzustand. Ohne diese Zeile weist das Modell jeden Druck ab.
func (a *application) initHotkeys() { a.model.SetModifierProbe(modifiersDown) }

// applyHotkeys übernimmt die Kürzel der geprüften App-Liste und gleicht mit dem System ab: alles ab, alles neu an.
func (a *application) applyHotkeys(l fensterio.AppList) {
	list := make([]panel.Hotkey, 0, len(l.Hotkeys))
	for _, h := range l.Hotkeys {
		list = append(list, panel.Hotkey{Key: h.Key, Pin: h.Pin, Setup: h.Setup})
	}
	a.model.SetHotkeys(list)
	a.model.SyncHotkeys(
		func(id int) { _ = unregisterHotkey(a.window, id) },
		func(id int, key string) error { return registerHotkey(a.window, id, key) },
	)
}

// releaseHotkeys gibt beim Beenden alle Kürzel frei.
func (a *application) releaseHotkeys() {
	a.model.ReleaseHotkeys(func(id int) { _ = unregisterHotkey(a.window, id) })
}

// hotkeyBeep spielt den Systemton einer Abweisung: ohne Fenster, ohne Fokus, ohne Sprechblase.
func (a *application) hotkeyBeep() {
	if a.model.TakeBeep() {
		messageBeep.Call(0)
	}
}
