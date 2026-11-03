package platform

import (
	"bytes"
	"context"
	"errors"
)

// PNG encoding stops before retaining more than the upload limit.
type screenshotBuffer struct {
	bytes.Buffer
	ctx context.Context
}

func (b *screenshotBuffer) Write(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 4<<20-b.Len() {
		return 0, errors.New("screenshot_size")
	}
	return b.Buffer.Write(p)
}
