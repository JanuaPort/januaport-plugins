//go:build windows

package fensterio

import (
	"errors"
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/edge"
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
}

// Surface ist ein natives Fenster mit WebView. Alle Methoden laufen auf dem UI-Thread, der es angelegt hat.
// Jedes sicherheitsrelevante Ereignis wird nativ gebunden (NavigationStarting, FrameNavigationStarting,
// NewWindowRequested, DownloadStarting, WebMessageReceived mit Quelle, PermissionRequested, ProcessFailed);
// scheitert eines, öffnet das Fenster nicht (RunSteps). WebResourceRequested wird nicht benutzt: Die
// Bibliothek beendet bei einem COM-Fehler dort den Prozess (log.Fatal), und CSP plus Navigationssperre
// decken die Unterressourcen.
type Surface struct {
	o        *Opened
	cfg      SurfaceConfig
	hwnd     uintptr
	bar      uintptr
	chromium *edge.Chromium
	view     *edge.ICoreWebView2
	closed   bool
	quit     bool // WM_QUIT kam während des Starts und wurde weitergereicht
}

var (
	user32       = windows.NewLazySystemDLL("user32.dll")
	pRegister    = user32.NewProc("RegisterClassExW")
	pCreate      = user32.NewProc("CreateWindowExW")
	pDefProc     = user32.NewProc("DefWindowProcW")
	pDestroy     = user32.NewProc("DestroyWindow")
	pMove        = user32.NewProc("MoveWindow")
	pClientRect  = user32.NewProc("GetClientRect")
	pShow        = user32.NewProc("ShowWindow")
	pPost        = user32.NewProc("PostMessageW")
	pForeground  = user32.NewProc("SetForegroundWindow")
	pLoadCursor  = user32.NewProc("LoadCursorW")
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
	wmSize    = 0x0005
	wmClose   = 0x0010
	wmDestroy = 0x0002
	swRestore = 9
	className = "JanuaPortApp"
)

// NewSurface legt Fenster, WebView und alle Sperren an. Es blockiert kurz (WebView2-Start) und gehört auf
// den UI-Thread; Embed pumpt dabei Nachrichten, damit Tray und Halt bedienbar bleiben.
func NewSurface(o *Opened, cfg SurfaceConfig) (*Surface, error) {
	s := &Surface{o: o, cfg: cfg, chromium: edge.NewChromium()}
	app := o.Name
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
			s.chromium.SetGlobalPermission(edge.CoreWebView2PermissionStateDeny)
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
		{"embed begin", func() error {
			s.chromium.DataPath = o.UserDataDir
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
		{"settings", s.applySettings},
		{"bind NavigationStarting", func() error {
			return s.bindNavigation(wv2com.IdxAddNavigationStarting)
		}},
		{"bind FrameNavigationStarting", func() error {
			return s.bindNavigation(wv2com.IdxAddFrameNavigationStarting)
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
				a := wv2com.P(args)
				source, ok := wv2com.GetString(a, wv2com.IdxMessageSource)
				if !ok {
					return 0
				}
				msg, ok := wv2com.GetString(a, wv2com.IdxMessageTryGetAsStr)
				if !ok {
					return 0
				}
				s.trace("WebMessageReceived", source)
				s.o.Host.Receive(MessageSource(source), msg)
				return 0
			})
			return wv2com.AddEvent(s.com(), wv2com.IdxAddWebMessageReceived, h)
		}},
		{"virtual host", func() error {
			host, ok := fenster.AppHost(app)
			v3 := s.chromium.GetICoreWebView2_3()
			if !ok || v3 == nil {
				return ErrWebView
			}
			return v3.SetVirtualHostNameToFolderMapping(host, o.Dir, edge.COREWEBVIEW2_HOST_RESOURCE_ACCESS_KIND(edge.COREWEBVIEW2_HOST_RESOURCE_ACCESS_KIND_DENY))
		}},
		{"show", func() error {
			s.layout()
			pShow.Call(s.hwnd, 1)
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

func (s *Surface) trace(event, detail string) {
	if s.cfg.Probe != nil {
		s.cfg.Probe(event, detail)
	}
}

func (s *Surface) com() wv2com.Obj { return unsafe.Pointer(s.view) }

func (s *Surface) findProcs() error {
	for _, p := range []*windows.LazyProc{pRegister, pCreate, pDefProc, pDestroy, pMove, pClientRect, pShow, pPost, pForeground, pLoadCursor} {
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
	return nil
}

func (s *Surface) bindNavigation(addIdx int) error {
	app := s.o.Name
	h := wv2com.NewHandler(wv2com.IIDNavigationStartingHandler, func(_, args uintptr) uintptr {
		a := wv2com.P(args)
		v := NavVerdict{Cancel: true}
		if uri, ok := wv2com.GetString(a, wv2com.IdxNavURI); ok {
			s.trace("NavigationStarting", uri)
			v = NavigationVerdict(app, uri)
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
	view, bar := fenster.Layout(int(r.Right-r.Left), int(r.Bottom-r.Top), s.o.TestDevice)
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
	for _, b := range s.o.Host.Drain() {
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
		switch msg {
		case wmSize:
			if s.view != nil {
				s.layout()
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
