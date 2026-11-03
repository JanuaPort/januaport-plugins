//go:build windows

package main

import (
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/panel"
)

// Real Windows loader, no windows or tray created. Native CI only.
func TestWindowsBedienfeldFehlenderProc(t *testing.T) {
	saved := nativeLoadError
	t.Cleanup(func() { nativeLoadError = saved })
	for _, tc := range []struct{ dll, proc string }{
		{"kernel32.dll", "JanuaPortDeliberatelyMissingProcedure"},
		{"JanuaPortDeliberatelyMissingLibrary.dll", "GetModuleHandleW"},
	} {
		t.Run(tc.dll, func(t *testing.T) {
			nativeLoadError = nil
			p := systemDLL(tc.dll).proc(tc.proc)
			if nativeLoadError == nil {
				t.Fatal("fehlende Windows-API nicht als Störung erkannt")
			}
			if r, _, err := p.Call(); r != 0 || err == nil {
				t.Fatal("fehlende Windows-API muss Fehler statt Panic liefern", r, err)
			}
			v := panel.New().View()
			if v.Color != "purple" || v.Tooltip != panel.FaultText {
				t.Fatal("Startzustand muss Störung bleiben", v)
			}
		})
	}
}
