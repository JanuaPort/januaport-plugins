//go:build windows

package fensterio

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/edge"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/envsnap"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/platform"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/wv2com"
)

// SurfaceConfig sind die Rückrufe des Fensters an den UI-Thread des Bedienfelds.
type SurfaceConfig struct {
	// OnLink: Ein externer https-Link wurde abgefangen. Der Mensch entscheidet im nativen Dialog.
	OnLink func(Link)
	// OnClosed: Das Fenster ist weg (UI-Thread).
	OnClosed func()
	// Probe meldet jedes native Ereignis mit Einzelheit; nur die nativen Sonden setzen es.
	Probe func(event, detail string)

	// noThreadCheck schaltet die Prüfung des COM-Threads ab; nur die nativen Sonden erzwingen damit den
	// Fehlstart der Umgebung. Unexportiert: das Bedienfeld kann es nicht setzen.
	noThreadCheck bool
	// noResourceFilter und noNetLock schalten Schicht 1 und 2 für die Ursachensonde ab (nur in-package).
	noResourceFilter bool
	noNetLock        bool
	// noWebRTCPolicy lässt das Pflichtargument für UDP weg (nur die Kontrolle der Sonde, dass der Weg sonst offen ist).
	noWebRTCPolicy bool
	// allowPermissions erlaubt in der Sonde alle Berechtigungen (nur als sehende Kontrolle, dass die Seite sonst
	// etwas bekäme); im Betrieb ist es immer aus.
	allowPermissions bool
	// extraBrowserArgs erzwingt in der Sonde einen widersprechenden Eingriff (das Fenster darf dann nicht öffnen).
	extraBrowserArgs []string
}

// Surface ist ein natives Fenster mit WebView. Alle Methoden laufen auf dem UI-Thread, der es angelegt hat.
// Jedes sicherheitsrelevante Ereignis wird nativ gebunden (NavigationStarting, FrameNavigationStarting,
// NavigationCompleted, NewWindowRequested, DownloadStarting, WebMessageReceived mit Quelle, PermissionRequested, ProcessFailed);
// scheitert eines, öffnet das Fenster nicht (RunSteps). Dazu kommt WebResourceRequested für alle Anfragen
// (Schicht 1, fenster.ResourceAllowed) und die Netzsperre im Browserprozess (Schicht 2, fenster.NetworkLockArgs):
// NavigationStarting bricht die Navigation ab, verhindert aber die Anfrage nicht allein.
type Surface struct {
	o        *Opened
	cfg      SurfaceConfig
	hwnd     uintptr
	bar      uintptr
	chromium *edge.Chromium
	view     *edge.ICoreWebView2
	closed   bool
	quit     bool // WM_QUIT kam während des Starts und wurde weitergereicht
	traced   int  // Zahl der Fensternachrichten, die die Sonde schon in der Spur hat

	loginButton, logoutButton, accountLabel uintptr
	resetting                               bool
	quietDone                               func(bool)
	authTimer                               *time.Timer
	authEpoch                               uintptr
}

var (
	user32       = windows.NewLazySystemDLL("user32.dll")
	pRegister    = user32.NewProc("RegisterClassExW")
	pCreate      = user32.NewProc("CreateWindowExW")
	pDefProc     = user32.NewProc("DefWindowProcW")
	pDestroy     = user32.NewProc("DestroyWindow")
	pSetText     = user32.NewProc("SetWindowTextW")
	pEnable      = user32.NewProc("EnableWindow")
	pMove        = user32.NewProc("MoveWindow")
	pClientRect  = user32.NewProc("GetClientRect")
	pShow        = user32.NewProc("ShowWindow")
	pPost        = user32.NewProc("PostMessageW")
	pForeground  = user32.NewProc("SetForegroundWindow")
	pLoadCursor  = user32.NewProc("LoadCursorW")
	pWindowRect  = user32.NewProc("GetWindowRect")
	pToScreen    = user32.NewProc("ClientToScreen")
	surfaceProcs = sync.OnceValue(func() uintptr { return syscall.NewCallback(surfaceProc) })

	classOnce sync.Once
	classErr  error

	surfacesMu sync.Mutex
	surfaces   = map[uintptr]*Surface{}
)

type wndClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}

type winRect struct{ Left, Top, Right, Bottom int32 }

const (
	wmSize     = 0x0005
	wmSetFocus = 0x0007
	wmClose    = 0x0010
	wmDestroy  = 0x0002
	swRestore  = 9
	className  = "JanuaPortApp"
)

// NewSurface legt Fenster, WebView und alle Sperren an. Es blockiert kurz (WebView2-Start) und gehört auf
// den UI-Thread; Embed pumpt dabei Nachrichten, damit Tray und Halt bedienbar bleiben.
func NewSurface(o *Opened, cfg SurfaceConfig) (*Surface, error) {
	s := &Surface{o: o, cfg: cfg, chromium: edge.NewChromium()}
	app := o.Name
	if o.api != nil && o.gateway != nil {
		o.login = NewLogin(o.gateway, o.Host, o.runLogin, func() { pPost.Call(s.hwnd, wmLoginReady, 0, 0) })
	}
	steps := []Step{
		{"webview2 runtime", func() error { _, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString(""); return err }},
		{"win32", s.findProcs},
		{"com apartment", func() error {
			if cfg.noThreadCheck {
				return nil
			}
			return checkThread()
		}},
		{"window class", func() error { classOnce.Do(registerClass); return classErr }},
		{"window", s.createWindow},
		// Das Benutzerdatenverzeichnis liegt unter der Zustandswurzel (der Widerruf des Läufers löscht es mit) und
		// bekommt dieselbe private DACL wie der Läufer-Ordner.
		{"userdata", func() error { return platform.MkdirPrivate(o.UserDataDir, false) }},
		{"bind PermissionRequested", func() error {
			state := edge.CoreWebView2PermissionStateDeny
			if cfg.allowPermissions {
				state = edge.CoreWebView2PermissionStateAllow
			}
			s.chromium.SetGlobalPermission(state)
			s.chromium.PermissionCallback = func(kind edge.CoreWebView2PermissionKind, state edge.CoreWebView2PermissionState) {
				s.trace("PermissionRequested", fmt.Sprintf("art=%d antwort=%d", kind, state))
			}
			return nil
		}},
		{"bind ProcessFailed", func() error {
			s.chromium.ProcessFailedCallback = func(*edge.ICoreWebView2, *edge.ICoreWebView2ProcessFailedEventArgs) {
				s.trace("ProcessFailed", "")
				s.o.Host.Fail()
				s.requestClose()
			}
			s.chromium.SetErrorCallback(func(error) {})
			return nil
		}},
		{"umgebung", func() error {
			// Auflage SEC (a): Die Browserprozesse erben diese Umgebung. Variablen mit Namen JNPT* (Konfiguration,
			// Opt-ins, vielleicht Träger) werden vorher entfernt; das Bedienfeld liest keine davon.
			// Jede WEBVIEW2_*-Variable ist ein Eingriff in den Start des Browsers (Ordner, Argumente): Das Fenster
			// öffnet nicht. Das kommt vor allem anderen, auch vor dem Entfernen der JNPT*-Variablen.
			// Verglichen wird die Momentaufnahme vor dem Lader-Init (envsnap) mit der Umgebung jetzt: Der Go-Lader
			// setzt fünf Variablen im Paket-Init auf leer, das ist erwartet; alles andere ist ein Eingriff.
			if findings := webview2Findings(envsnap.Start(), os.Environ()); len(findings) > 0 {
				s.trace("Eingriff", "WEBVIEW2-Variablen: "+strings.Join(findings, ","))
				return ErrWebView
			}
			for _, name := range jnptNames(os.Environ()) {
				if err := os.Unsetenv(name); err != nil {
					return err
				}
			}
			return nil
		}},
		{"embed begin", func() error {
			s.chromium.DataPath = o.UserDataDir
			// Schicht 2: Netz im Browserprozess aus (tote Proxy-Adresse, DNS aus) und spekulatives Laden aus;
			// Schicht 1 ist Pflicht daneben, und der Schritt "browser pruefen" verlangt das Ergebnis nach dem Start.
			var args []string
			if !cfg.noNetLock {
				for _, a := range fenster.NetworkLockArgs() {
					if cfg.noWebRTCPolicy && strings.HasPrefix(a, "--webrtc-ip-handling-policy") {
						continue
					}
					args = append(args, a)
				}
			}
			s.chromium.AdditionalBrowserArgs = append(args, cfg.extraBrowserArgs...)
			return s.chromium.Begin(s.hwnd)
		}},
		{"embed wait", func() error {
			// Begrenzt (startFrist): Die Rückrufe der Umgebung kommen als Nachrichten auf diesem Thread. Bei Fehler,
			// Frist oder Beenden werden spätere Rückrufe wirkungslos und das Fenster bleibt zu.
			err := WaitFor(startFrist, s.pumpOnce, func() (bool, error) {
				if s.quit {
					return false, ErrBeendet
				}
				return s.chromium.Ready()
			})
			if err != nil {
				s.chromium.Cancel()
				return err
			}
			controller := s.chromium.GetController()
			if controller == nil {
				return ErrWebView
			}
			v, err := controller.GetCoreWebView2()
			if err != nil || v == nil {
				return ErrWebView
			}
			s.view = v
			return nil
		}},
		{"browser pruefen", func() error {
			// Fail-closed nach Wirkung: Die Kommandozeile des Browserprozesses muss unsere Argumente exakt tragen.
			// Fehlt eine, oder widerspricht eine (Umgebung, Richtlinie, Versehen), öffnet das Fenster nicht.
			pid, ok := wv2com.BrowserProcessID(s.com())
			if !ok {
				return ErrWebView
			}
			line, err := processCommandLine(pid)
			if err != nil {
				return ErrWebView
			}
			if cfg.Probe != nil {
				s.trace("BrowserKommandozeile", fenster.DiagText(line))
			}
			problems := fenster.BrowserCommandLineProblems(line)
			for _, p := range problems {
				s.trace("BrowserArgument", p)
			}
			if len(problems) > 0 && !cfg.noNetLock && !cfg.noWebRTCPolicy {
				return ErrWebView
			}
			return nil
		}},
		{"settings", s.applySettings},
		{"bind NavigationStarting", func() error {
			return s.bindNavigation(wv2com.IdxAddNavigationStarting)
		}},
		{"bind FrameNavigationStarting", func() error {
			return s.bindNavigation(wv2com.IdxAddFrameNavigationStarting)
		}},
		{"bind WebResourceRequested", func() error {
			if cfg.noResourceFilter {
				return nil
			}
			// Schicht 1: Jede Anfrage des Browsers, nicht nur Navigationen, geht durch fenster.ResourceAllowed.
			// Nur der eigene virtuelle Host kommt durch, alles andere bekommt eine lokale 403-Antwort.
			env := unsafe.Pointer(s.chromium.Environment())
			h := wv2com.NewHandler(wv2com.IIDWebResourceHandler, func(_, args uintptr) uintptr {
				a := wv2com.P(args)
				uri, ok := wv2com.RequestURI(a)
				context, _ := wv2com.GetInt32(a, wv2com.IdxResGetContext)
				if ok && fenster.ResourceAllowed(app, uri, context == wv2com.ResourceContextDocument) {
					return 0
				}
				s.trace("WebResourceBlockiert", fmt.Sprintf("kontext=%d url=%s", context, fenster.DiagText(uri)))
				if !wv2com.BlockedResponse(env, a) {
					s.requestClose()
				}
				return 0
			})
			if err := wv2com.AddEvent(s.com(), wv2com.IdxAddWebResourceRequested, h); err != nil {
				return err
			}
			return wv2com.AddWebResourceRequestedFilter(s.com(), "*", wv2com.ResourceContextAll)
		}},
		{"bind NewWindowRequested", func() error {
			h := wv2com.NewHandler(wv2com.IIDNewWindowHandler, func(_, args uintptr) uintptr {
				a := wv2com.P(args)
				uri, _ := wv2com.GetString(a, wv2com.IdxNewWindowURI)
				s.trace("NewWindowRequested", uri)
				v := NewWindowVerdict(app, uri)
				if !wv2com.PutBool(a, wv2com.IdxNewWindowPutHandled, true) {
					s.requestClose()
				}
				s.link(v)
				return 0
			})
			return wv2com.AddEvent(s.com(), wv2com.IdxAddNewWindowRequested, h)
		}},
		{"bind DownloadStarting", func() error {
			v4, err := wv2com.QueryInterface(s.com(), &wv2com.IIDWebView2_4)
			if err != nil {
				return err
			}
			defer wv2com.Release(v4)
			h := wv2com.NewHandler(wv2com.IIDDownloadStartingHandler, func(_, args uintptr) uintptr {
				a := wv2com.P(args)
				s.trace("DownloadStarting", "")
				if !wv2com.PutBool(a, wv2com.IdxDownloadPutCancel, true) || !wv2com.PutBool(a, wv2com.IdxDownloadPutHandled, true) {
					s.requestClose()
				}
				return 0
			})
			return wv2com.AddEvent(v4, wv2com.IdxAddDownloadStarting, h)
		}},
		{"bind WebMessageReceived", func() error {
			h := wv2com.NewHandler(wv2com.IIDWebMessageHandler, func(_, args uintptr) uintptr {
				if s.resetting || s.closed {
					return 0
				}
				a := wv2com.P(args)
				source, hr, nilPtr, ok := wv2com.ReadString(a, wv2com.IdxMessageSource)
				if !ok {
					// Fail-closed: Ohne lesbare Quelle wird verworfen. Die Spur nennt das HRESULT und die Seite, auf der
					// die WebView gerade steht, nie den Inhalt der Nachricht.
					current, _ := wv2com.GetString(s.com(), wv2com.IdxGetSource)
					s.trace("WebMessageVerworfen", fmt.Sprintf("quelle nicht lesbar hr=%#08x zeiger_leer=%v aktuelle_seite=%s", uint32(hr), nilPtr, fenster.DiagText(current)))
					return 0
				}
				msg, ok := wv2com.GetString(a, wv2com.IdxMessageTryGetAsStr)
				if !ok {
					s.trace("WebMessageVerworfen", "kein String, Quelle "+fenster.DiagText(source))
					return 0
				}
				s.trace("WebMessageReceived", source)
				if s.cfg.Probe != nil {
					allowed, why := fenster.SourceVerdict(app, MessageSource(source))
					s.trace("MessageVerdict", fmt.Sprintf("quelle=%s ok=%v grund=%q laenge=%d", fenster.DiagText(source), allowed, why, len(msg)))
				}
				s.o.Host.Receive(MessageSource(source), msg)
				return 0
			})
			return wv2com.AddEvent(s.com(), wv2com.IdxAddWebMessageReceived, h)
		}},
		{"bind NavigationCompleted", func() error {
			h := wv2com.NewHandler(wv2com.IIDNavigationCompletedHandler, func(_, args uintptr) uintptr {
				a := wv2com.P(args)
				success, ok1 := wv2com.GetInt32(a, 3)
				status, ok2 := wv2com.GetInt32(a, 4)
				s.trace("NavigationCompleted", fmt.Sprintf("erfolg=%v status=%d lesbar=%v", success != 0, status, ok1 && ok2))
				if s.quietDone != nil && !s.closed {
					source, ok := wv2com.GetString(s.com(), wv2com.IdxGetSource)
					if ok && source == "about:blank" {
						done := s.quietDone
						s.quietDone = nil
						done(ok1 && success != 0)
					}
				}
				return 0
			})
			return wv2com.AddEvent(s.com(), wv2com.IdxAddNavigationCompleted, h)
		}},
		{"virtual host", func() error {
			host, ok := fenster.AppHost(app)
			v3 := s.chromium.GetICoreWebView2_3()
			if !ok || v3 == nil {
				return ErrWebView
			}
			return v3.SetVirtualHostNameToFolderMapping(host, o.Dir, edge.COREWEBVIEW2_HOST_RESOURCE_ACCESS_KIND(edge.COREWEBVIEW2_HOST_RESOURCE_ACCESS_KIND_DENY))
		}},
		{"initial browser data", func() error {
			done, good := false, false
			if e := s.ClearBrowsingData(func(ok bool) { done = true; good = ok }); e != nil {
				return ErrWebView
			}
			if e := WaitFor(startFrist, s.pumpOnce, func() (bool, error) {
				if s.closed || s.quit {
					return false, ErrBeendet
				}
				return done, nil
			}); e != nil || !good {
				return ErrWebView
			}
			return nil
		}},
		{"show", func() error {
			s.layout()
			pShow.Call(s.hwnd, 1)
			// Ohne sichtbare WebView meldet die Seite "hidden", drosselt Zeitgeber und bekommt keine Eingabe (Lauf 12).
			if err := s.chromium.SetVisible(true); err != nil {
				return ErrWebView
			}
			// Das Bedienfeld hat gerade die Eingabe (Klick auf den Menüeintrag) und darf das neue Fenster nach vorn holen;
			// ohne aktives Fenster hat die Seite keinen Fokus. Danach gehört der Fokus der WebView.
			pForeground.Call(s.hwnd)
			s.chromium.Focus()
			s.traceGeometry()
			return nil
		}},
		{"navigate", func() error {
			url := fenster.StartURL(app)
			if url == "" {
				return ErrWebView
			}
			return s.view.Navigate(url)
		}},
	}
	if err := RunSteps(steps); err != nil {
		s.destroy()
		return nil, err
	}
	return s, nil
}

const (
	pmRemove   = 1
	wmQuit     = 0x0012
	qsAllInput = 0x04FF
)

var (
	pMsgWait     = user32.NewProc("MsgWaitForMultipleObjects")
	pPeekMsg     = user32.NewProc("PeekMessageW")
	pTranslate   = user32.NewProc("TranslateMessage")
	pDispatchMsg = user32.NewProc("DispatchMessageW")
	pPostQuit    = user32.NewProc("PostQuitMessage")
)

type pumpMsg struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}

// pumpOnce wartet höchstens 50 ms auf Nachrichten und verteilt alle anstehenden. Kommt WM_QUIT, wird es für
// die Hauptschleife erneut eingestellt und der Start abgebrochen.
func (s *Surface) pumpOnce() {
	pMsgWait.Call(0, 0, 0, 50, qsAllInput)
	var m pumpMsg
	for {
		r, _, _ := pPeekMsg.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
		if r == 0 {
			return
		}
		if m.Message == wmQuit {
			pPostQuit.Call(m.WParam)
			s.quit = true
			return
		}
		pTranslate.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMsg.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// traceGeometry nennt der Sonde, wo das Fenster liegt und wohin man klicken muss (Mitte der Fläche der WebView, in
// Bildschirmkoordinaten). Nur mit Probe.
func (s *Surface) traceGeometry() {
	if s.cfg.Probe == nil {
		return
	}
	var win winRect
	pWindowRect.Call(s.hwnd, uintptr(unsafe.Pointer(&win)))
	x, y := s.viewCenterScreen()
	s.trace("Fenster", fmt.Sprintf("rect=(%d,%d,%d,%d) flaeche_mitte_bildschirm=(%d,%d)", win.Left, win.Top, win.Right, win.Bottom, x, y))
}

// viewCenterScreen liefert die Mitte der Fläche der WebView in Bildschirmkoordinaten (Ziel für Klick und Pixelprobe).
func (s *Surface) viewCenterScreen() (x, y int32) {
	var client winRect
	pClientRect.Call(s.hwnd, uintptr(unsafe.Pointer(&client)))
	view, _ := s.bounds(int(client.Right-client.Left), int(client.Bottom-client.Top))
	pt := struct{ X, Y int32 }{int32(view.X + view.W/2), int32(view.Y + view.H/2)}
	pToScreen.Call(s.hwnd, uintptr(unsafe.Pointer(&pt)))
	return pt.X, pt.Y
}

// traceMessage hält die Eingabenachrichten des Fensters fest, die für Klick und Fokus zählen: Ein Klick in die WebView
// erreicht das Elternfenster als WM_PARENTNOTIFY mit der Maustaste, ein Klick auf die Leiste oder den Rand als
// WM_LBUTTONDOWN. Höchstens 60 Einträge je Fenster.
func (s *Surface) traceMessage(msg, wp, lp uintptr) {
	const (
		wmActivate, wmKillFocus, wmMouseActivate, wmLButtonDown, wmParentNotify = 0x0006, 0x0008, 0x0021, 0x0201, 0x0210
	)
	if s.traced >= 60 {
		return
	}
	x, y := int16(lp&0xffff), int16((lp>>16)&0xffff)
	switch msg {
	case wmParentNotify:
		if event := wp & 0xffff; event == wmLButtonDown || event == 0x0204 || event == 0x0207 {
			s.traced++
			s.trace("Fenster", fmt.Sprintf("WM_PARENTNOTIFY maustaste=%#x x=%d y=%d", event, x, y))
		}
	case wmLButtonDown:
		s.traced++
		s.trace("Fenster", fmt.Sprintf("WM_LBUTTONDOWN x=%d y=%d", x, y))
	case wmMouseActivate:
		s.traced++
		s.trace("Fenster", "WM_MOUSEACTIVATE")
	case wmActivate:
		s.traced++
		s.trace("Fenster", fmt.Sprintf("WM_ACTIVATE zustand=%d", wp&0xffff))
	case wmSetFocus:
		s.traced++
		s.trace("Fenster", "WM_SETFOCUS")
	case wmKillFocus:
		s.traced++
		s.trace("Fenster", "WM_KILLFOCUS")
	}
}

func (s *Surface) trace(event, detail string) {
	if s.cfg.Probe != nil {
		s.cfg.Probe(event, detail)
	}
}

func (s *Surface) com() wv2com.Obj { return unsafe.Pointer(s.view) }

func (s *Surface) findProcs() error {
	for _, p := range []*windows.LazyProc{pRegister, pCreate, pDefProc, pDestroy, pMove, pClientRect, pShow, pPost, pForeground, pLoadCursor, pSetText, pEnable} {
		if err := p.Find(); err != nil {
			return err
		}
	}
	return nil
}

func registerClass() {
	instance, err := moduleHandle()
	if err != nil {
		classErr = err
		return
	}
	cursor, _, _ := pLoadCursor.Call(0, 32512)
	wc := wndClass{Instance: instance, Proc: surfaceProcs(), Name: windows.StringToUTF16Ptr(className), Background: 6, Cursor: cursor}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if ok, _, e := pRegister.Call(uintptr(unsafe.Pointer(&wc))); ok == 0 && !errors.Is(e, windows.ERROR_CLASS_ALREADY_EXISTS) {
		classErr = ErrWebView
	}
}

func (s *Surface) createWindow() error {
	instance, err := moduleHandle()
	if err != nil {
		return err
	}
	const (
		wsOverlapped = 0x00CF0000 // WS_OVERLAPPEDWINDOW
		useDefault   = 0x80000000
	)
	h, _, _ := pCreate.Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(className))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(fenster.WindowTitle(s.o.Name)))),
		wsOverlapped, useDefault, useDefault, 1000, 700, 0, 0, instance, 0)
	if h == 0 {
		return ErrWebView
	}
	s.hwnd = h
	surfacesMu.Lock()
	surfaces[h] = s
	surfacesMu.Unlock()
	if s.o.TestDevice {
		const wsChildVisibleCenter = 0x40000000 | 0x10000000 | 0x1 | 0x80 // CHILD | VISIBLE | SS_CENTER | SS_NOPREFIX
		b, _, _ := pCreate.Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("STATIC"))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(fenster.TestBarText))),
			wsChildVisibleCenter, 0, 0, 0, 0, h, 0, instance, 0)
		if b == 0 {
			return ErrWebView
		}
		s.bar = b
	}
	if s.o.login != nil {
		return s.createLoginControls(instance)
	}
	return nil
}

func (s *Surface) bindNavigation(addIdx int) error {
	app := s.o.Name
	h := wv2com.NewHandler(wv2com.IIDNavigationStartingHandler, func(_, args uintptr) uintptr {
		a := wv2com.P(args)
		v := NavVerdict{Cancel: true}
		if uri, ok := wv2com.GetString(a, wv2com.IdxNavURI); ok {
			s.trace("NavigationStarting", uri)
			if s.resetting {
				v = NavVerdict{Cancel: uri != "about:blank" || addIdx != wv2com.IdxAddNavigationStarting}
			} else {
				v = NavigationVerdict(app, uri)
			}
		}
		if v.Cancel && !wv2com.PutBool(a, wv2com.IdxNavPutCancel, true) {
			s.requestClose()
		}
		s.link(v)
		return 0
	})
	return wv2com.AddEvent(s.com(), addIdx, h)
}

func (s *Surface) link(v NavVerdict) {
	if v.Link != nil && s.cfg.OnLink != nil {
		s.cfg.OnLink(*v.Link)
	}
}

// applySettings setzt genau die Sollliste aus fenster.Settings(). Eine Einstellung ohne Setter oder ein
// fehlgeschlagener Aufruf ist ein Fehler: Das Fenster öffnet dann nicht.
func (s *Surface) applySettings() error {
	st, err := s.chromium.GetSettings()
	if err != nil || st == nil {
		return ErrWebView
	}
	s4 := st.GetICoreWebView2Settings4()
	appliers := map[string]func(bool) error{
		"AreBrowserAcceleratorKeysEnabled": st.PutAreBrowserAcceleratorKeysEnabled,
		"AreDefaultContextMenusEnabled":    st.PutAreDefaultContextMenusEnabled,
		"AreDefaultScriptDialogsEnabled":   st.PutAreDefaultScriptDialogsEnabled,
		"AreDevToolsEnabled":               st.PutAreDevToolsEnabled,
		"AreHostObjectsAllowed":            st.PutAreHostObjectsAllowed,
		"IsBuiltInErrorPageEnabled":        st.PutIsBuiltInErrorPageEnabled,
		"IsPinchZoomEnabled":               st.PutIsPinchZoomEnabled,
		"IsScriptEnabled":                  st.PutIsScriptEnabled,
		"IsStatusBarEnabled":               st.PutIsStatusBarEnabled,
		"IsSwipeNavigationEnabled":         st.PutIsSwipeNavigationEnabled,
		"IsWebMessageEnabled":              st.PutIsWebMessageEnabled,
		"IsZoomControlEnabled":             st.PutIsZoomControlEnabled,
	}
	if s4 != nil {
		appliers["IsGeneralAutofillEnabled"] = s4.PutIsGeneralAutofillEnabled
		appliers["IsPasswordAutosaveEnabled"] = s4.PutIsPasswordAutosaveEnabled
	}
	for _, set := range fenster.Settings() {
		apply := appliers[set.Name]
		if apply == nil || apply(set.An) != nil {
			return ErrWebView
		}
	}
	return nil
}

func (s *Surface) layout() {
	var r winRect
	if ok, _, _ := pClientRect.Call(s.hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	view, bar := s.bounds(int(r.Right-r.Left), int(r.Bottom-r.Top))
	s.layoutLogin(int(r.Right-r.Left), bar.H)
	if s.bar != 0 {
		pMove.Call(s.bar, uintptr(bar.X), uintptr(bar.Y), uintptr(bar.W), uintptr(bar.H), 1)
	}
	s.chromium.SetSize(edge.Rect{Left: int32(view.X), Top: int32(view.Y), Right: int32(view.X + view.W), Bottom: int32(view.Y + view.H)})
}

// Pump gibt die fertigen Antworten der Brücke an die Seite (UI-Thread, nicht blockierend).
func (s *Surface) Pump() {
	if s.closed || s.view == nil {
		return
	}
	if s.o.login != nil {
		s.o.login.Pump(s)
	}
	for _, b := range s.o.Host.Drain() {
		if s.cfg.Probe != nil {
			s.trace("Antwort", fenster.ReplySummary(b))
		}
		if err := s.view.PostWebMessageAsString(string(b)); err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
			continue
		}
	}
}

// Focus holt ein schon offenes Fenster nach vorn.
func (s *Surface) Focus() {
	if !s.closed {
		pShow.Call(s.hwnd, swRestore)
		pForeground.Call(s.hwnd)
		if s.view != nil {
			s.chromium.Focus() // der Fokus gehört der Seite, sonst fragen Zwischenablage und Standort gar nicht erst
		}
	}
}

// ClearBrowsingData löscht alle Browserdaten des Profils (A5); done läuft auf dem UI-Thread.
// Ohne die Schnittstelle (altes Runtime) ist das ein fester Fehler.
func (s *Surface) ClearBrowsingData(done func(ok bool)) error {
	return wv2com.ClearBrowsingDataAll(s.com(), func(hr uintptr) { done(!wv2com.Failed(hr)) })
}

// Close schließt das Fenster; OnClosed folgt aus WM_DESTROY.
func (s *Surface) Close() { s.destroy() }

func (s *Surface) requestClose() {
	if s.hwnd != 0 {
		pPost.Call(s.hwnd, wmClose, 0, 0)
	}
}

func (s *Surface) destroy() {
	s.chromium.Close()
	if s.hwnd != 0 && !s.closed {
		pDestroy.Call(s.hwnd)
	}
}

func (s *Surface) finish() {
	if s.closed {
		return
	}
	s.closed = true
	if s.authTimer != nil {
		s.authTimer.Stop()
	}
	s.quietDone = nil
	s.o.Close()
	surfacesMu.Lock()
	delete(surfaces, s.hwnd)
	surfacesMu.Unlock()
	if s.cfg.OnClosed != nil {
		s.cfg.OnClosed()
	}
}

func surfaceProc(hwnd, msg, wp, lp uintptr) uintptr {
	surfacesMu.Lock()
	s := surfaces[hwnd]
	surfacesMu.Unlock()
	if s != nil {
		if s.cfg.Probe != nil {
			s.traceMessage(msg, wp, lp)
		}
		switch msg {
		case wmLoginReady:
			s.Pump()
			return 0
		case wmLoginTimeout:
			if s.resetting && wp == s.authEpoch {
				s.FailLogin()
			}
			return 0
		case 0x0111: // WM_COMMAND, ausschließlich native Knöpfe
			if s.o.login != nil && wp>>16 == 0 {
				switch wp & 0xffff {
				case loginControlID:
					s.o.login.Start()
					s.o.login.Pump(s)
				case logoutControlID:
					s.o.login.SignOut(s)
				}
			}
			return 0
		case wmSize:
			if s.view != nil {
				// SIZE_MINIMIZED (1): unsichtbar schalten, sonst sichtbar; ein Fehler lässt sich hier nicht mehr melden.
				_ = s.chromium.SetVisible(wp != 1 && !s.resetting)
				s.layout()
			}
			return 0
		case wmSetFocus:
			if s.view != nil {
				s.chromium.Focus() // Aktivieren des Fensters: Fokus an die WebView weiterreichen
			}
			return 0
		case wmClose:
			s.destroy()
			return 0
		case wmDestroy:
			s.finish()
			return 0
		}
	}
	r, _, _ := pDefProc.Call(hwnd, msg, wp, lp)
	return r
}

// OpenInBrowser öffnet einen externen https-Link im Standardbrowser, nach der Prüfung noch einmal und über
// ShellExecuteW (nie über cmd /c start, ein & in der URL bliebe wirkungslos).
func OpenInBrowser(raw string) error {
	if _, ok := fenster.ExternalLink(raw); !ok {
		return ErrWebView
	}
	return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(raw), nil, nil, windows.SW_SHOWNORMAL)
}

var pModule = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")

func moduleHandle() (uintptr, error) {
	if err := pModule.Find(); err != nil {
		return 0, err
	}
	h, _, _ := pModule.Call(0)
	if h == 0 {
		return 0, ErrWebView
	}
	return h, nil
}
