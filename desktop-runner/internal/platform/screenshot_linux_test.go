package platform

import (
	"context"
	"testing"
)

func TestScreenshotLinuxStub(t *testing.T) {
	if b, e := CaptureScreenshot(context.Background()); e == nil || len(b) != 0 {
		t.Fatal("Linux capture must fail")
	}
}
