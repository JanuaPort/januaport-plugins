//go:build windows

// jnpt-bedienfeld is a native tray client. Build with -ldflags=-H=windowsgui.
package main

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fensterio"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/panel"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/panelio"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

type guid struct {
	Data1        uint32
	Data2, Data3 uint16
	Data4        [8]byte
}

var (
	user             = systemDLL("user32.dll")
	shell            = systemDLL("shell32.dll")
	registerClass    = user.proc("RegisterClassExW")
	createWindowEx   = user.proc("CreateWindowExW")
	defWindowProc    = user.proc("DefWindowProcW")
	getMessage       = user.proc("GetMessageW")
	translateMessage = user.proc("TranslateMessage")
	dispatchMessage  = user.proc("DispatchMessageW")
	showWindow       = user.proc("ShowWindow")
	postMessage      = user.proc("PostMessageW")
	postQuit         = user.proc("PostQuitMessage")
	destroyWindow    = user.proc("DestroyWindow")
	setTimer         = user.proc("SetTimer")
	notifyIcon       = shell.proc("Shell_NotifyIconW")
	createPopupMenu  = user.proc("CreatePopupMenu")
	appendMenu       = user.proc("AppendMenuW")
	trackPopupMenu   = user.proc("TrackPopupMenu")
	destroyMenu      = user.proc("DestroyMenu")
	getCursorPos     = user.proc("GetCursorPos")
	createIcon       = user.proc("CreateIcon")
	destroyIcon      = user.proc("DestroyIcon")
	registerMessage  = user.proc("RegisterWindowMessageW")
	sendMessage      = user.proc("SendMessageW")
	getStockObject   = systemDLL("gdi32.dll").proc("GetStockObject")
	getSystemMetrics = user.proc("GetSystemMetrics")
	loadCursor       = user.proc("LoadCursorW")
	getModuleHandle  = systemDLL("kernel32.dll").proc("GetModuleHandleW")
)

const (
	WS_EX_NOACTIVATE = 0x08000000
	dialogExStyle    = WS_EX_NOACTIVATE | 0x00000008 // TOPMOST; never activate.
	buttonStyle      = 0x40000000 | 0x10000000       // CHILD | VISIBLE, ordinary push button.
	showNoActivate   = 4                             // SW_SHOWNOACTIVATE
	wmEvents         = 0x8001
	wmTray           = 0x8002
	trayFlags        = 1 | 2 | 4 // MESSAGE | ICON | TIP. Kein NIF_INFO: Der Hinweis ist ein eigener NIM_MODIFY (notify).
)

// Hinweis bei neuer Rückfrage (#1240), die einzige Stelle mit NIF_INFO. Fester Text, nie Inhalt der Rückfrage;
// ohne Ton, ohne Aktivierung. Der Dialog öffnet weiterhin nur per Klick.
const (
	nimModify       = 1
	nifInfo         = 0x10
	noticeInfoFlags = 1 | 0x10 // NIIF_INFO | NIIF_NOSOUND
)

type point struct{ X, Y int32 }
type winMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type notifyData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                guid
	BalloonIcon         uintptr
}

// This state is owned exclusively by the locked UI thread. Goroutines only
// put Go values in events and PostMessage a wake-up (no Go pointers in LPARAM).
type application struct {
	model          *panel.Model
	window, dialog uintptr
	instance       uintptr
	dialogID       string
	icons          map[string]uintptr
	tray           bool
	taskbarCreated uint32
	events         chan panelio.Event
	requests       chan panelio.Request
	generation     uint64
	cancel         context.CancelFunc
	ctx            context.Context

	// K6b-2: Apps. Alles Blockierende (Zustand lesen, Gateway, Brücke, Pipe-Gespräch) läuft in fensterio-
	// Goroutinen; hier stehen nur Kanäle, Karten und der Wecker.
	rt        *fensterio.Runtime
	exchange  *fensterio.PanelExchange
	gen       atomic.Uint64
	appLists  chan fensterio.AppList
	apps      fensterio.AppList // letzter geprüfter Anzeigestand, nur UI-Thread
	opened    chan openResult
	surfaces  map[string]*fensterio.Surface
	opening   map[string]bool
	aux       uintptr
	auxLink   *fensterio.Link
	embedding bool
}

var app *application

func wide(s string) *uint16 { return syscall.StringToUTF16Ptr(s) }

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// WebView2 verlangt ein COM-STA auf genau diesem Thread; ohne COM öffnet kein App-Fenster (fester Fehler), das Bedienfeld läuft weiter.
	leave, _ := fensterio.EnterUI()
	defer leave()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &application{model: panel.New(), icons: map[string]uintptr{}, events: make(chan panelio.Event, 64), requests: make(chan panelio.Request, 32), cancel: cancel}
	app = a
	a.ctx = ctx
	a.appLists = make(chan fensterio.AppList, 4)
	a.opened = make(chan openResult, 8)
	a.surfaces = map[string]*fensterio.Surface{}
	a.opening = map[string]bool{}
	a.exchange = fensterio.NewPanelExchange(a.sendCommand)
	a.instance, _, _ = getModuleHandle.Call(0)
	if a.instance == 0 {
		return
	}
	class := wide("JanuaPortBedienfeld")
	cursor, _, _ := loadCursor.Call(0, 32512)
	wc := windowClass{Instance: a.instance, Proc: syscall.NewCallback(windowProc), Name: class, Background: 6, Cursor: cursor}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if ok, _, _ := registerClass.Call(uintptr(unsafe.Pointer(&wc))); ok == 0 {
		return
	}
	a.window, _, _ = createWindowEx.Call(WS_EX_NOACTIVATE, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, 0, 0, a.instance, 0)
	if a.window == 0 {
		return
	}
	defer destroyWindow.Call(a.window)
	for color, rgb := range map[string]uint32{"green": 0x26935c, "yellow": 0xe0a124, "red": 0xcf3b3b, "purple": 0x913fc4} {
		a.icons[color] = makeIcon(rgb)
		if a.icons[color] == 0 {
			return
		}
		defer destroyIcon.Call(a.icons[color])
	}
	id, _, _ := registerMessage.Call(uintptr(unsafe.Pointer(wide("TaskbarCreated"))))
	a.taskbarCreated = uint32(id)
	a.render()
	defer func() {
		n := notifyData{Window: a.window, ID: 1}
		n.Size = uint32(unsafe.Sizeof(n))
		notifyIcon.Call(2, uintptr(unsafe.Pointer(&n)))
	}()
	if id, _, _ := setTimer.Call(a.window, 1, 200, 0); id == 0 {
		return
	}
	// K6b-2: Apps. Die Laufzeit liest nur (Reader), schreibt nie in den Zustand und löscht bei Widerruf nichts;
	// Gateway, Brücke und Pipe-Gespräch laufen in Goroutinen von fensterio, nie auf diesem Thread.
	if rt, err := fensterio.NewWindowsRuntime(a.exchange.Func(), a.wake); err == nil {
		a.rt = rt
		go rt.Watch(ctx, 15*time.Second, func(l fensterio.AppList) {
			select {
			case a.appLists <- l:
				a.wake()
			case <-ctx.Done():
			}
		})
	}
	if nativeLoadError == nil {
		go panelio.Run(ctx, pipe.Dial, a.requests, func(e panelio.Event) bool {
			select {
			case a.events <- e:
			case <-ctx.Done():
				return false
			}
			ok, _, _ := postMessage.Call(a.window, wmEvents, 0, 0)
			return ok != 0
		})
	}
	var msg winMessage
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func makeIcon(rgb uint32) uintptr {
	var mask [32]byte // monochrome AND mask, 16x16, word-aligned
	var pixels [16 * 16]uint32
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if (x-7)*(x-7)+(y-7)*(y-7) <= 45 {
				pixels[y*16+x] = 0xff000000 | rgb
			} else {
				mask[y*2+x/8] |= 1 << uint(7-x%8)
			}
		}
	}
	h, _, _ := createIcon.Call(0, 16, 16, 1, 32, uintptr(unsafe.Pointer(&mask[0])), uintptr(unsafe.Pointer(&pixels[0])))
	return h
}

func (a *application) apply(e panel.Effects) {
	if e.Quit {
		a.closeSurfaces()
		a.closeAux()
		a.cancel()
		destroyWindow.Call(a.window)
		return
	}
	if e.OpenApp != "" {
		a.openApp(e.OpenApp)
	}
	if len(e.Commands) > 0 || e.Reconnect {
		select {
		case a.requests <- panelio.Request{Generation: a.generation, Commands: e.Commands, Reconnect: e.Reconnect}:
		default:
			// A full command queue never silently loses a halt or an answer.
			a.model.Connection(panel.Disconnected)
			// Only this UI thread produces requests; drain unsent commands and
			// request reconnect even when the old server keeps heartbeating.
		drain:
			for {
				select {
				case <-a.requests:
				default:
					break drain
				}
			}
			a.requests <- panelio.Request{Generation: a.generation, Reconnect: true}
		}
	}
	a.render()
}

func (a *application) eventsReady() {
	for {
		select {
		case e := <-a.events:
			if e.Generation < a.generation {
				continue
			}
			a.generation = e.Generation
			a.gen.Store(e.Generation)
			if e.Raw == nil {
				a.exchange.Reset()
				a.apply(a.model.Connection(e.State))
			} else {
				a.exchange.Deliver(e.Raw)
				a.apply(a.model.Receive(e.Raw, time.Now()))
			}
		default:
			a.drainApps()
			return
		}
	}
}

func (a *application) render() {
	v := a.model.View()
	n := notifyData{Window: a.window, ID: 1, Flags: trayFlags, Callback: wmTray, Icon: a.icons[v.Color]}
	n.Size = uint32(unsafe.Sizeof(n))
	copy(n.Tip[:], syscall.StringToUTF16(v.Tooltip))
	try := func(modify bool) bool {
		op := uintptr(0)
		if modify {
			op = nimModify
		}
		ok, _, _ := notifyIcon.Call(op, uintptr(unsafe.Pointer(&n)))
		return ok != 0
	}
	a.tray = panel.TrayUpdate(a.tray, try)
	if a.tray && a.model.TakeNotice() {
		a.notify()
	}
	if a.dialog != 0 && (v.Dialog == nil || !v.Dialog.Visible || v.Dialog.ID != a.dialogID) {
		h := a.dialog
		a.dialog = 0
		a.dialogID = ""
		destroyWindow.Call(h)
	}
	if a.dialog == 0 && v.Dialog != nil && v.Dialog.Visible {
		a.openDialog(v.Dialog)
	}
}

// notify setzt den Hinweis am vorhandenen Symbol. Nur NIF_INFO, damit Symbol und Tooltip unberührt bleiben.
func (a *application) notify() {
	n := notifyData{Window: a.window, ID: 1, Flags: nifInfo, InfoFlags: noticeInfoFlags}
	n.Size = uint32(unsafe.Sizeof(n))
	copy(n.Info[:], syscall.StringToUTF16(panel.NoticeText))
	copy(n.InfoTitle[:], syscall.StringToUTF16(panel.NoticeTitle))
	notifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&n)))
}

func (a *application) menu() {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	items := a.model.View().Menu
	for i, item := range items {
		flags := uintptr(0)
		if !item.Enabled {
			flags = 1
		}
		// Win32 interprets ampersands as mnemonic markers; display labels verbatim.
		appendMenu.Call(menu, flags, uintptr(i+1), uintptr(unsafe.Pointer(wide(strings.ReplaceAll(item.Text, "&", "&&")))))
	}
	var p point
	getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	choice, _, _ := trackPopupMenu.Call(menu, 0x100|0x80|2, uintptr(p.X), uintptr(p.Y), 0, a.window, 0)
	// IDs bind setups to pin/name, so a nested native menu loop cannot
	// retarget a click when a status event replaces or reorders the list.
	if choice > 0 && int(choice) <= len(items) {
		a.apply(a.model.Choose(items[choice-1].ID, time.Now()))
	}
}

func createWindow(exStyle uintptr, class, title string, style uintptr, x, y, w, h int, owner, id uintptr) uintptr {
	r, _, _ := createWindowEx.Call(exStyle, uintptr(unsafe.Pointer(wide(class))), uintptr(unsafe.Pointer(wide(title))), style, uintptr(x), uintptr(y), uintptr(w), uintptr(h), owner, id, app.instance, 0)
	return r
}

func (a *application) openDialog(d *panel.Dialog) {
	// No default push button, no initial focus, and no activation API. Child
	// buttons remain mouse/UIA-operable; the same-user UIA boundary is documented.
	sw, _, _ := getSystemMetrics.Call(0)
	sh, _, _ := getSystemMetrics.Call(1)
	x, y := max(0, (int(sw)-640)/2), max(0, (int(sh)-370)/2)
	h := createWindow(dialogExStyle, "JanuaPortBedienfeld", d.Title, 0x80000000|0x00c00000|0x00080000, x, y, 640, 370, a.window, 0)
	if h == 0 {
		a.model.HideDialog()
		return
	}
	a.dialog = h
	a.dialogID = d.ID
	font, _, _ := getStockObject.Call(17)                                                          // DEFAULT_GUI_FONT
	label := createWindow(0, "STATIC", d.Text, 0x40000000|0x10000000|0x80, 20, 20, 590, 180, h, 0) // SS_NOPREFIX, static only
	sendMessage.Call(label, 0x30, font, 0)
	for i, text := range d.Options {
		b := createWindow(0, "BUTTON", strings.ReplaceAll(text, "&", "&&"), buttonStyle, 20+(i%2)*300, 210+(i/2)*50, 285, 40, h, uintptr(100+i))
		if b == 0 {
			destroyWindow.Call(h)
			a.dialog = 0
			a.dialogID = ""
			a.model.HideDialog()
			return
		}
		sendMessage.Call(b, 0x30, font, 0)
	}
	showWindow.Call(h, showNoActivate)
}

func windowProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	a := app
	if a != nil {
		if a.taskbarCreated != 0 && msg == a.taskbarCreated {
			a.tray = false
			a.render()
			return 0
		}
		switch msg {
		case wmEvents:
			a.eventsReady()
			return 0
		case wmTray:
			if lp == 0x202 || lp == 0x205 {
				a.menu()
			}
			return 0
		case 0x113: // WM_TIMER
			a.model.Tick(time.Now())
			a.render()
			return 0
		case 0x21:
			return 3 // WM_MOUSEACTIVATE -> MA_NOACTIVATE
		case 0x111: // WM_COMMAND, only a mouse/UIA click on this dialog's button.
			if hwnd == a.aux && wp>>16 == 0 && lp != 0 && wp&0xffff >= 200 && wp&0xffff < 202 {
				a.auxCommand(int(wp&0xffff) - 200)
				return 0
			}
			if hwnd == a.dialog && wp>>16 == 0 && lp != 0 && wp&0xffff >= 100 && wp&0xffff < 104 {
				a.apply(a.model.Answer(int(wp&0xffff)-100, time.Now()))
			}
			return 0
		case 0x10: // WM_CLOSE
			if hwnd == a.aux {
				a.closeAux()
				return 0
			}
			if hwnd == a.dialog {
				a.model.HideDialog()
				a.render()
			} else {
				a.apply(a.model.Choose("exit", time.Now()))
			}
			return 0
		case 2:
			if hwnd == a.window {
				a.cancel()
				postQuit.Call(0)
			}
			return 0
		}
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wp, lp)
	return r
}
