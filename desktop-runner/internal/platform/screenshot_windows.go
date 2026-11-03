package platform

import (
	"context"
	"image"
	"image/png"
	"runtime"
	"syscall"
	"unsafe"
)

// user32 and gdi32 are Windows KnownDLLs. All capture runs in the runner;
// Python receives only an acknowledgement, never a handle or image bytes.
var captureUser = syscall.NewLazyDLL("user32.dll")
var captureGDI = syscall.NewLazyDLL("gdi32.dll")
var captureGetDC = captureUser.NewProc("GetDC")
var captureReleaseDC = captureUser.NewProc("ReleaseDC")
var captureMetrics = captureUser.NewProc("GetSystemMetrics")
var captureDPI = captureUser.NewProc("SetThreadDpiAwarenessContext")
var captureCreateDC = captureGDI.NewProc("CreateCompatibleDC")
var captureDeleteDC = captureGDI.NewProc("DeleteDC")
var captureDIB = captureGDI.NewProc("CreateDIBSection")
var captureDeleteObject = captureGDI.NewProc("DeleteObject")
var captureSelect = captureGDI.NewProc("SelectObject")
var captureBitBlt = captureGDI.NewProc("BitBlt")
var captureFlush = captureGDI.NewProc("GdiFlush")

// CaptureScreenshot captures the whole primary screen, without CGO. A locked
// session may expose the lock screen; GDI availability depends on the desktop.
func CaptureScreenshot(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Primary-screen metrics must be physical pixels even at 125/150/200% DPI.
	// Restore this thread's context before letting Go reuse the OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if captureDPI.Find() != nil {
		return nil, errPlatform
	}
	previousDPI, _, _ := captureDPI.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4)
	if previousDPI == 0 {
		return nil, errPlatform
	}
	defer captureDPI.Call(previousDPI)
	width, _, _ := captureMetrics.Call(0)
	height, _, _ := captureMetrics.Call(1)
	// Bound native allocation as well as encoded output (up to 64M pixels).
	if width == 0 || height == 0 || width > 32768 || height > 32768 || width*height > 64<<20 {
		return nil, errPlatform
	}
	screen, _, _ := captureGetDC.Call(0)
	if screen == 0 {
		return nil, errPlatform
	}
	defer captureReleaseDC.Call(0, screen)
	dc, _, _ := captureCreateDC.Call(screen)
	if dc == 0 {
		return nil, errPlatform
	}
	defer captureDeleteDC.Call(dc)
	info := struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		Used, Important        uint32
	}{Size: 40, Width: int32(width), Height: -int32(height), Planes: 1, BitCount: 32}
	var pixels *byte
	bitmap, _, _ := captureDIB.Call(screen, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if bitmap == 0 {
		return nil, errPlatform
	}
	defer captureDeleteObject.Call(bitmap)
	if pixels == nil {
		return nil, errPlatform
	}
	previous, _, _ := captureSelect.Call(dc, bitmap)
	if previous == 0 || previous == ^uintptr(0) {
		return nil, errPlatform
	}
	defer captureSelect.Call(dc, previous)
	ok, _, _ := captureBitBlt.Call(dc, 0, 0, width, height, screen, 0, 0, 0x40cc0020) // SRCCOPY | CAPTUREBLT
	if ok == 0 {
		return nil, errPlatform
	}
	if ok, _, _ = captureFlush.Call(); ok == 0 {
		return nil, errPlatform
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw := unsafe.Slice(pixels, int(width*height*4))
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+2] = raw[i+2], raw[i]
		raw[i+3] = 255
	}
	img := &image.RGBA{Pix: raw, Stride: int(width) * 4, Rect: image.Rect(0, 0, int(width), int(height))}
	out := &screenshotBuffer{ctx: ctx}
	if err := png.Encode(out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
