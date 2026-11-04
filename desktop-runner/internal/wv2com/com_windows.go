//go:build windows

// Package wv2com ist der dünne eigene COM-Adapter neben go-webview2 (SEC K6b-2). Die Bibliothek reicht
// NavigationStarting, NewWindowRequested, DownloadStarting und die Quelle einer Webnachricht nicht durch;
// dieses Paket bindet sie direkt an die COM-Schnittstellen. Vtable-Positionen und IIDs stammen aus
// WebView2.h des Microsoft.Web.WebView2-SDK 1.0.2535.41 und stehen unten einzeln belegt. Es enthält keine
// Entscheidung, nur Aufrufe, Handler-Objekte und das Freigeben von Zeichenketten.
package wv2com

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Obj ist ein COM-Interfacezeiger.
type Obj = unsafe.Pointer

// Vtable-Indizes (C-Vtable inkl. IUnknown 0..2) aus WebView2.h, SDK 1.0.2535.41.
const (
	// ICoreWebView2
	IdxAddNavigationStarting      = 7
	IdxAddWebResourceRequested    = 55
	IdxAddResourceFilter          = 57
	IdxAddNavigationCompleted     = 15 // nur Diagnose der Sonden
	IdxAddFrameNavigationStarting = 17
	IdxAddWebMessageReceived      = 34
	IdxAddNewWindowRequested      = 44
	// ICoreWebView2_4
	IdxAddDownloadStarting = 75
	// ICoreWebView2_13
	IdxGetProfile = 105
	// ICoreWebView2Profile2
	IdxClearBrowsingDataAll = 12
	// ICoreWebView2NavigationStartingEventArgs
	IdxNavURI       = 3
	IdxNavPutCancel = 8
	// ICoreWebView2NewWindowRequestedEventArgs
	IdxNewWindowURI        = 3
	IdxNewWindowPutHandled = 6
	// ICoreWebView2DownloadStartingEventArgs
	IdxDownloadPutCancel  = 5
	IdxDownloadPutHandled = 9
	// ICoreWebView2WebMessageReceivedEventArgs
	IdxMessageSource      = 3
	IdxMessageTryGetAsStr = 5
)

// IIDs aus WebView2.h.
var (
	iidUnknown                    = mustGUID("{00000000-0000-0000-C000-000000000046}")
	IIDWebView2_4                 = mustGUID("{20d02d59-6df2-42dc-bd06-f98a694b1302}")
	IIDWebView2_13                = mustGUID("{F75F09A8-667E-4983-88D6-C8773F315E84}")
	IIDProfile2                   = mustGUID("{fa740d4b-5eae-4344-a8ad-74be31925397}")
	IIDNavigationStartingHandler  = mustGUID("{9adbe429-f36d-432b-9ddc-f8881fbd76e3}")
	IIDWebResourceHandler         = mustGUID("{ab00b74c-15f1-4646-80e8-e76341d25d71}")
	IIDNavigationCompletedHandler = mustGUID("{d33a35bf-1c49-4f98-93ab-006e0533fe1c}")
	IIDNewWindowHandler           = mustGUID("{d4c185fe-c81c-4989-97af-2d3fa7ab5651}")
	IIDDownloadStartingHandler    = mustGUID("{efedc989-c396-41ca-83f7-07f845a55724}")
	IIDWebMessageHandler          = mustGUID("{57213f19-00e6-49fa-8e07-898ea01ecbd2}")
	IIDClearBrowsingDataHandler   = mustGUID("{e9710a06-1d1d-49b2-8234-226f35846ae5}")
)

// mustGUID liefert bei einem Schreibfehler die Null-GUID: Die Anfrage danach scheitert, das Fenster öffnet nicht.
func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		return windows.GUID{}
	}
	return g
}

var (
	ErrCOM         = errors.New("com_aufruf_fehlgeschlagen")
	ErrNoInterface = errors.New("com_schnittstelle_fehlt")
)

// P macht aus einem von nativem Code übergebenen Zeigerwert einen Interfacezeiger. Der Umweg über die
// Adresse der Variablen hält den Zeigerwert außerhalb von unsafe.Pointer(uintptr)-Umwandlungen.
func P(u uintptr) Obj { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

func fnAt(o Obj, idx int) uintptr {
	vt := *(*unsafe.Pointer)(o)
	return *(*uintptr)(unsafe.Add(vt, idx*int(unsafe.Sizeof(uintptr(0)))))
}

// Call ruft die Methode idx der Vtable von o auf und liefert den HRESULT.
func Call(o Obj, idx int, args ...uintptr) uintptr {
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(o))
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(fnAt(o, idx), all...)
	return r
}

// Failed ist FAILED(hr).
func Failed(hr uintptr) bool { return int32(hr) < 0 }

// QueryInterface fragt o nach iid. Fehlt die Schnittstelle, ist das ein fester Fehler (kein stilles Weglaufen).
func QueryInterface(o Obj, iid *windows.GUID) (Obj, error) {
	if o == nil {
		return nil, ErrNoInterface
	}
	var out unsafe.Pointer
	if Failed(Call(o, 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))) || out == nil {
		return nil, ErrNoInterface
	}
	return out, nil
}

// Release gibt eine Referenz frei.
func Release(o Obj) {
	if o != nil {
		Call(o, 2)
	}
}

// takeString liest einen LPWSTR-Ausgabewert und gibt ihn mit CoTaskMemFree frei.
func takeString(get func(out uintptr) uintptr) (string, bool) {
	var p *uint16
	if Failed(get(uintptr(unsafe.Pointer(&p)))) || p == nil {
		return "", false
	}
	s := windows.UTF16PtrToString(p)
	windows.CoTaskMemFree(unsafe.Pointer(p))
	return s, true
}

// ReadString wie GetString, nennt aber bei einem Fehlschlag den Grund: das HRESULT des Aufrufs und ob der Zeiger
// trotz Erfolg leer war. Nur für die Diagnose der Sonden; der Wert selbst erscheint nie in Ausgaben.
func ReadString(o Obj, idx int) (s string, hr uintptr, nilPtr, ok bool) {
	var p *uint16
	hr = Call(o, idx, uintptr(unsafe.Pointer(&p)))
	if Failed(hr) {
		return "", hr, p == nil, false
	}
	if p == nil {
		return "", hr, true, false
	}
	s = windows.UTF16PtrToString(p)
	windows.CoTaskMemFree(unsafe.Pointer(p))
	return s, hr, false, true
}

// GetString ruft einen get_X-Zugriff mit LPWSTR-Ergebnis.
func GetString(o Obj, idx int) (string, bool) {
	return takeString(func(out uintptr) uintptr { return Call(o, idx, out) })
}

// Indizes für WebResourceRequested (Schicht 1).
const (
	IdxResGetRequest        = 3 // ICoreWebView2WebResourceRequestedEventArgs::get_Request
	IdxResPutResponse       = 5
	IdxResGetContext        = 7
	IdxRequestGetURI        = 3 // ICoreWebView2WebResourceRequest::get_Uri
	IdxEnvCreateResponse    = 4 // ICoreWebView2Environment::CreateWebResourceResponse
	ResourceContextAll      = 0
	ResourceContextDocument = 1
	blockedStatus           = 403
)

// AddWebResourceRequestedFilter registriert einen Filter (uri mit * als Muster, ctx wie COREWEBVIEW2_WEB_RESOURCE_CONTEXT).
func AddWebResourceRequestedFilter(webview Obj, filter string, ctx int32) error {
	p, err := windows.UTF16PtrFromString(filter)
	if err != nil {
		return ErrCOM
	}
	if Failed(Call(webview, IdxAddResourceFilter, uintptr(unsafe.Pointer(p)), uintptr(ctx))) {
		return ErrCOM
	}
	return nil
}

// RequestURI liest die URL der Anfrage eines WebResourceRequested-Ereignisses.
func RequestURI(args Obj) (string, bool) {
	var req unsafe.Pointer
	if Failed(Call(args, IdxResGetRequest, uintptr(unsafe.Pointer(&req)))) || req == nil {
		return "", false
	}
	defer Release(req)
	return GetString(req, IdxRequestGetURI)
}

// BlockedResponse beantwortet die Anfrage lokal mit 403, ohne Inhalt: Sie verlässt den Browser nie.
// Gelingt das nicht, ist false die Antwort, und der Aufrufer schließt das Fenster.
func BlockedResponse(env, args Obj) bool {
	reason, err1 := windows.UTF16PtrFromString("Blocked")
	headers, err2 := windows.UTF16PtrFromString("")
	if err1 != nil || err2 != nil || env == nil {
		return false
	}
	var resp unsafe.Pointer
	if Failed(Call(env, IdxEnvCreateResponse, 0, blockedStatus, uintptr(unsafe.Pointer(reason)), uintptr(unsafe.Pointer(headers)), uintptr(unsafe.Pointer(&resp)))) || resp == nil {
		return false
	}
	defer Release(resp)
	return !Failed(Call(args, IdxResPutResponse, uintptr(resp)))
}

// IdxGetSource ist ICoreWebView2::get_Source (die Adresse des Dokuments, auf dem die WebView steht).
const IdxGetSource = 4

// IdxGetBrowserProcessID ist ICoreWebView2::get_BrowserProcessId.
const IdxGetBrowserProcessID = 37

// BrowserProcessID liefert die Prozess-ID des Browserprozesses dieser WebView.
func BrowserProcessID(webview Obj) (uint32, bool) {
	var pid uint32
	if Failed(Call(webview, IdxGetBrowserProcessID, uintptr(unsafe.Pointer(&pid)))) || pid == 0 {
		return 0, false
	}
	return pid, true
}

// GetInt32 ruft einen get_X-Zugriff mit 32-Bit-Ergebnis (BOOL, Enum).
func GetInt32(o Obj, idx int) (int32, bool) {
	var v int32
	if Failed(Call(o, idx, uintptr(unsafe.Pointer(&v)))) {
		return 0, false
	}
	return v, true
}

// PutBool ruft ein put_X(BOOL).
func PutBool(o Obj, idx int, v bool) bool {
	b := uintptr(0)
	if v {
		b = 1
	}
	return !Failed(Call(o, idx, b))
}

// AddEvent registriert einen Handler über add_X(handler, &token).
func AddEvent(o Obj, addIdx int, handler Obj) error {
	var token int64
	if Failed(Call(o, addIdx, uintptr(handler), uintptr(unsafe.Pointer(&token)))) {
		return ErrCOM
	}
	return nil
}

// Handler ist die Go-Seite eines Ereignis-Handlers. sender und args sind die rohen Zeigerwerte (P macht
// daraus Interfacezeiger). Der Rückgabewert ist der HRESULT. Er läuft auf dem UI-Thread des Fensters.
type Handler func(sender, args uintptr) uintptr

type object struct {
	vtbl *[4]uintptr // muss das erste Feld sein: der COM-Zeiger zeigt auf die Vtable-Adresse
	fn   Handler
	iid  windows.GUID
	refs atomic.Int32
}

var (
	cbOnce                    sync.Once
	cbQI, cbAddRef, cbRelease uintptr
	cbInvoke                  uintptr
	objects                   sync.Map // uintptr(Objektzeiger) -> *object
	pinMu                     sync.Mutex
	pinner                    runtime.Pinner
	eNoInterface              = uintptr(0x80004002)
)

func initCallbacks() {
	cbQI = syscall.NewCallback(func(this, riid, out uintptr) uintptr {
		o := lookup(this)
		if o == nil || out == 0 {
			return eNoInterface
		}
		want := *(*windows.GUID)(P(riid))
		if want != iidUnknown && want != o.iid {
			*(*uintptr)(P(out)) = 0
			return eNoInterface
		}
		*(*uintptr)(P(out)) = this
		o.refs.Add(1)
		return 0
	})
	cbAddRef = syscall.NewCallback(func(this uintptr) uintptr {
		if o := lookup(this); o != nil {
			return uintptr(o.refs.Add(1))
		}
		return 1
	})
	// Objekte werden bewusst nie freigegeben: Ein Fenster legt einige wenige an, und ein Use-after-free im
	// Browserprozess wäre schlimmer als dieser kleine Rest.
	cbRelease = syscall.NewCallback(func(this uintptr) uintptr {
		if o := lookup(this); o != nil {
			if n := o.refs.Add(-1); n > 0 {
				return uintptr(n)
			}
			o.refs.Store(1)
		}
		return 1
	})
	cbInvoke = syscall.NewCallback(func(this, sender, args uintptr) uintptr {
		if o := lookup(this); o != nil && o.fn != nil {
			return o.fn(sender, args)
		}
		return 0
	})
}

func lookup(this uintptr) *object {
	if v, ok := objects.Load(this); ok {
		return v.(*object)
	}
	return nil
}

// NewHandler baut ein COM-Objekt, das Invoke an fn weitergibt und auf iid (und IUnknown) antwortet.
func NewHandler(iid windows.GUID, fn Handler) Obj {
	cbOnce.Do(initCallbacks)
	o := &object{fn: fn, iid: iid}
	o.refs.Store(1)
	o.vtbl = &[4]uintptr{cbQI, cbAddRef, cbRelease, cbInvoke}
	pinMu.Lock()
	pinner.Pin(o)
	pinner.Pin(o.vtbl)
	pinMu.Unlock()
	p := unsafe.Pointer(o)
	objects.Store(uintptr(p), o)
	return p
}

// ClearBrowsingDataAll löscht alle Browserdaten des Profils (A5) und meldet das Ende über done(hresult).
// Fehlt ICoreWebView2_13 oder Profile2 (altes Runtime), ist das ein fester Fehler.
func ClearBrowsingDataAll(webview Obj, done func(hr uintptr)) error {
	v13, err := QueryInterface(webview, &IIDWebView2_13)
	if err != nil {
		return err
	}
	defer Release(v13)
	var profile unsafe.Pointer
	if Failed(Call(v13, IdxGetProfile, uintptr(unsafe.Pointer(&profile)))) || profile == nil {
		return ErrNoInterface
	}
	defer Release(profile)
	p2, err := QueryInterface(profile, &IIDProfile2)
	if err != nil {
		return err
	}
	defer Release(p2)
	h := NewHandler(IIDClearBrowsingDataHandler, func(hr, _ uintptr) uintptr {
		done(hr)
		return 0
	})
	if Failed(Call(p2, IdxClearBrowsingDataAll, uintptr(h))) {
		return ErrCOM
	}
	return nil
}
