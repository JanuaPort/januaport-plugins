// Package runlog bounds the explicit sandbox log channel (M4/M6, SEC B2).
package runlog

import "unicode/utf8"

const MaxBytes = 64 << 10
const Marker = "\n[gekürzt]\n"

// Buffer has fixed storage, including reserved space for the truncation marker.
// It never retains the incoming string. Append returns only accepted new bytes
// for immediate forwarding, so timeouts retain logs already sent.
type Buffer struct {
	data      [MaxBytes]byte
	n         int
	truncated bool
}

func (b *Buffer) Append(s string) string {
	return b.append(s, MaxBytes-len(Marker))
}

// Collect stores already bounded guard sections unchanged, including their
// marker. Unlike Append, it need not stream a stable prefix: on overflow it
// may replace its retained tail. This avoids truncating the guard's marker
// a second time at a multibyte boundary.
func (b *Buffer) Collect(s string) {
	b.append(s, MaxBytes)
}

func (b *Buffer) append(s string, limit int) string {
	if b.truncated {
		return ""
	}
	start := b.n
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		n := utf8.RuneLen(r)
		if b.n+n > limit {
			if b.n > MaxBytes-len(Marker) {
				b.n = MaxBytes - len(Marker)
				for b.n > 0 && !utf8.RuneStart(b.data[b.n]) {
					b.n--
				}
			}
			b.n += copy(b.data[b.n:], Marker)
			b.truncated = true
			break
		}
		b.n += utf8.EncodeRune(b.data[b.n:], r)
		s = s[size:]
	}
	if limit == MaxBytes {
		return ""
	}
	return string(b.data[start:b.n])
}

func (b *Buffer) String() string  { return string(b.data[:b.n]) }
func (b *Buffer) Capacity() int   { return len(b.data) }
func (b *Buffer) Truncated() bool { return b.truncated }
