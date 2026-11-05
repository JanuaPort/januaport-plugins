//go:build windows

package fensterio

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32               = windows.NewLazySystemDLL("ole32.dll")
	pCoInitializeEx     = ole32.NewProc("CoInitializeEx")
	pCoUninitialize     = ole32.NewProc("CoUninitialize")
	pCoGetApartmentType = ole32.NewProc("CoGetApartmentType")
)

const (
	coinitApartmentThreaded = 0x2
	aptTypeSTA              = 0
	aptTypeMainSTA          = 3
)

// EnterUI macht den aufrufenden Thread zum COM-Apartment (STA), wie WebView2 es verlangt. Der Aufrufer hat
// den Thread mit runtime.LockOSThread festgelegt und ruft leave auf demselben Thread auf. Ein Thread, der
// schon als MTA läuft, taugt nicht für die Oberfläche (fester Fehler).
func EnterUI() (leave func(), err error) {
	if err := pCoInitializeEx.Find(); err != nil {
		return func() {}, ErrWebView
	}
	hr, _, _ := pCoInitializeEx.Call(0, coinitApartmentThreaded)
	if int32(hr) < 0 { // zum Beispiel RPC_E_CHANGED_MODE
		return func() {}, ErrWebView
	}
	return func() { pCoUninitialize.Call() }, nil // S_OK und S_FALSE zählen beide
}

// checkThread prüft, dass der Thread ein STA ist. Fehlt COM, scheitert die WebView2-Umgebung erst später
// asynchron; hier wird es vorher mit festem Fehler erkannt.
func checkThread() error {
	if err := pCoGetApartmentType.Find(); err != nil {
		return ErrWebView
	}
	var kind, qualifier int32
	hr, _, _ := pCoGetApartmentType.Call(uintptr(unsafe.Pointer(&kind)), uintptr(unsafe.Pointer(&qualifier)))
	if int32(hr) < 0 || (kind != aptTypeSTA && kind != aptTypeMainSTA) {
		return ErrWebView
	}
	return nil
}
