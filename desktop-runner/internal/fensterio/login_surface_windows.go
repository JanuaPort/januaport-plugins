//go:build windows

package fensterio

import (
	"time"
	"unsafe"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"golang.org/x/sys/windows"
)

const (
	loginControlID  = 3101
	logoutControlID = 3102
	wmLoginReady    = 0x8000 + 104
	wmLoginTimeout  = 0x8000 + 105
	loginBarHeight  = 56
)

func (s *Surface) createLoginControls(instance uintptr) error {
	if !s.o.ShowLoginBar() {
		return nil
	}
	create := func(class, text string, id uintptr, style uintptr) uintptr {
		h, _, _ := pCreate.Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(class))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))), 0x40000000|0x10000000|style, 0, 0, 0, 0, s.hwnd, id, instance, 0)
		return h
	}
	s.loginButton = create("BUTTON", "Anmelden", loginControlID, 0x10000)
	s.logoutButton = create("BUTTON", "Abmelden", logoutControlID, 0x10000)
	s.accountLabel = create("STATIC", "", 0, 0x80) // SS_NOPREFIX: Anzeigenamen sind wörtlicher Text
	if s.loginButton == 0 || s.logoutButton == 0 || s.accountLabel == 0 {
		return ErrWebView
	}
	s.Account("", "Bitte mit dem eigenen Konto anmelden.", false, false)
	return nil
}
func (s *Surface) bounds(w, h int) (fenster.Rect, fenster.Rect) {
	view, bar := fenster.Layout(w, h, s.o.TestDevice)
	if s.o.login != nil && s.o.ShowLoginBar() {
		n := min(loginBarHeight, view.H)
		view.Y += n
		view.H -= n
	}
	return view, bar
}
func (s *Surface) layoutLogin(w, y int) {
	if s.loginButton == 0 {
		return
	}
	pMove.Call(s.loginButton, 4, uintptr(y+4), 100, 24, 1)
	pMove.Call(s.logoutButton, 108, uintptr(y+4), 100, 24, 1)
	pMove.Call(s.accountLabel, 216, uintptr(y+4), uintptr(max(0, w-220)), 48, 1)
}
func (s *Surface) Account(name, message string, busy, signed bool) {
	if s.closed || !s.o.ShowLoginBar() || s.accountLabel == 0 {
		return
	}
	text := message
	if name != "" {
		text = name + "\r\n" + message
	}
	pSetText.Call(s.accountLabel, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
	enabled := uintptr(1)
	if busy {
		enabled = 0
	}
	pEnable.Call(s.loginButton, enabled)
	enabled = 0
	if signed && !busy {
		enabled = 1
	}
	pEnable.Call(s.logoutButton, enabled)
}

// UpdateApps läuft auf dem UI-Thread nach dem nächsten geprüften Manifest.
// Nur die Anzeige wechselt; Login, Gateway, Rechte und Browserdaten bleiben unberührt.
func (s *Surface) UpdateApps(l AppList) {
	if s.closed || l.ManifestSeq < s.o.ManifestSeq {
		return
	}
	mode, ok := l.Modes[s.o.Name]
	if !ok {
		return // CloseMissing behandelt verschwundene Apps.
	}
	s.o.Mode, s.o.ManifestSeq = mode, l.ManifestSeq
	if s.o.ShowLoginBar() && s.o.login != nil {
		if s.loginButton == 0 {
			instance, err := moduleHandle()
			if err != nil || s.createLoginControls(instance) != nil {
				s.requestClose()
				return
			}
		}
		s.o.login.Pump(s)
	} else {
		for _, h := range []uintptr{s.loginButton, s.logoutButton, s.accountLabel} {
			if h != 0 {
				pDestroy.Call(h)
			}
		}
		s.loginButton, s.logoutButton, s.accountLabel = 0, 0, 0
	}
	s.layout()
}

// Quiesce wartet asynchron auf das leere Dokument. Alte Skripte dürfen die
// gerade gelöschten Daten nicht wieder anlegen. Nachrichten und Navigation sind gesperrt.
func (s *Surface) Quiesce(done func(bool)) error {
	if s.closed || s.view == nil {
		return ErrWebView
	}
	s.resetting = true
	s.quietDone = done
	s.authEpoch++
	epoch := s.authEpoch
	if s.authTimer != nil {
		s.authTimer.Stop()
	}
	s.authTimer = time.AfterFunc(20*time.Second, func() { pPost.Call(s.hwnd, wmLoginTimeout, epoch, 0) })
	if e := s.chromium.SetVisible(false); e != nil {
		return ErrWebView
	}
	return s.view.Navigate("about:blank")
}
func (s *Surface) Reload() error {
	if s.closed || s.view == nil {
		return ErrWebView
	}
	s.resetting = false
	if s.authTimer != nil {
		s.authTimer.Stop()
	}
	if e := s.view.Navigate(fenster.StartURL(s.o.Name)); e != nil {
		return ErrWebView
	}
	return s.chromium.SetVisible(true)
}
func (s *Surface) FailLogin() {
	if s.o.login != nil {
		s.o.login.Close()
	}
	s.o.Host.Fail()
	s.requestClose()
}
