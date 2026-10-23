package gitref

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// DesktopSegment implements the portable names in the Desktop-Läufer contract.
// Keep the script path rules intact; Windows adds device names and folding.
func DesktopSegment(s string) bool {
	if !ValidSegment(s) || !utf8.ValidString(s) || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"|?*`, r) {
			return false
		}
	}
	base, _, _ := strings.Cut(strings.ToUpper(s), ".")
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

func DesktopPath(p string) bool {
	if !ValidPinPath(p) {
		return false
	}
	for _, s := range strings.Split(p, "/") {
		if !DesktopSegment(s) {
			return false
		}
	}
	return true
}

// DesktopFold is the exact Unicode folding required for tree uniqueness.
func DesktopFold(s string) string {
	return strings.Map(func(r rune) rune { return unicode.ToLower(unicode.ToUpper(r)) }, s)
}
