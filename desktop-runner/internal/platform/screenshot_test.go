package platform

import (
	"context"
	"testing"
)

func TestScreenshotPNGBufferGrenze(t *testing.T) {
	b := &screenshotBuffer{ctx: context.Background()}
	if n, e := b.Write(make([]byte, 4<<20)); n != 4<<20 || e != nil {
		t.Fatal(n, e)
	}
	if n, e := b.Write([]byte{1}); n != 0 || e == nil || b.Len() != 4<<20 {
		t.Fatal("PNG limit", n, e, b.Len())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b = &screenshotBuffer{ctx: ctx}
	if n, e := b.Write([]byte{1}); n != 0 || e == nil {
		t.Fatal("cancelled encoding", n, e)
	}
}
