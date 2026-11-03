//go:build !windows

package platform

import (
	"context"
	"errors"
)

func CaptureScreenshot(context.Context) ([]byte, error) {
	return nil, errors.New("screenshot_requires_windows")
}
