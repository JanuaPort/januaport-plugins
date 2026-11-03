package platform

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Explicit CI/GATE opt-in only. A separate process bounds even a stuck GDI call.
func TestWindowsScreenshotGDI(t *testing.T) {
	if os.Getenv("JNPT_GDI_PROBE") != "1" {
		t.Skip("GDI only via LEAD CI/GATE opt-in in an interactive session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsScreenshotGDIChild$", "-test.v")
	cmd.Env = append(os.Environ(), "JNPT_GDI_CHILD=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("GDI exceeded deadline")
	}
	if err != nil {
		t.Fatalf("GDI child: %v\n%s", err, out)
	}
	if bytes.Contains(out, []byte("--- SKIP:")) {
		t.Skip(string(out))
	}
}
func TestWindowsScreenshotGDIChild(t *testing.T) {
	if os.Getenv("JNPT_GDI_PROBE") != "1" || os.Getenv("JNPT_GDI_CHILD") != "1" {
		t.Skip("GDI child only")
	}
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sessionID); err != nil {
		t.Fatal(err)
	}
	session, err := Session()
	if sessionID == 0 || (err == nil && !session.Active) {
		t.Skip("No interactive user session for GDI")
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := CaptureScreenshot(context.Background())
	if err != nil {
		t.Fatal("GDI capture in interactive session:", err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || len(b) > 4<<20 {
		t.Fatal("invalid capture", cfg, len(b), err)
	}
}
