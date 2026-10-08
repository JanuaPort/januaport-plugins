//go:build linux

package guard

import "testing"

func TestIncompleteTail(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"", 0},
		{"a\xc3\xa4", 3},     // ä vollständig
		{"a\xc3", 1},         // ä angebrochen
		{"a\xe2\x82", 1},     // € angebrochen
		{"a\xf0\x9f\x98", 1}, // Emoji angebrochen
		{"a\xff", 2},         // ungültig, nicht aufhalten
	}
	for _, tt := range tests {
		if got := incompleteTail([]byte(tt.in)); got != tt.want {
			t.Errorf("incompleteTail(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
