//go:build windows

package main

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// Kompiliert unter Linux-Cross-Build; die Ausführung ist eine native Probe (BOX).
func TestHandleBenutzbar(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "handle")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, tc := range []struct {
		name   string
		handle windows.Handle
		want   bool
	}{
		{"null", 0, false},
		{"ungültig", windows.InvalidHandle, false},
		{"Datei", windows.Handle(file.Fd()), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := handleUsable(tc.handle); got != tc.want {
				t.Fatalf("handleUsable = %t, erwartet %t", got, tc.want)
			}
		})
	}
}
