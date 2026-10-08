package gitref

import (
	"strings"
	"testing"
)

func TestValidCommit(t *testing.T) {
	hex40 := strings.Repeat("a", 40)
	hex64 := strings.Repeat("0123456789abcdef", 4)
	tests := []struct {
		in   string
		want bool
	}{
		{hex40, true},
		{hex64, true},
		{strings.Repeat("a", 41), false},
		{strings.Repeat("a", 39), false},
		{strings.Repeat("a", 63), false},
		{"abc1234", false},              // kurzer Hash
		{"main", false},                 // Branch-Name
		{"refs/heads/main", false},      // voller Ref
		{"--upload-pack=x", false},      // Option
		{"-oProxyCommand=x", false},     // Option
		{strings.ToUpper(hex40), false}, // nur Kleinbuchstaben, wie git sie schreibt
		{hex40 + "\n", false},           // Zeilenumbruch
		{hex40[:20] + "\n" + hex40[:19], false},
		{"", false},
	}
	for _, tt := range tests {
		if got := ValidCommit(tt.in); got != tt.want {
			t.Errorf("ValidCommit(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// SEC-Zeile 2 (M7): Tabellentest des URL-Prüfers.
func TestCheckRepoURL(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{"ssh://git@git.example.com/org/scripts.git", true},
		{"ssh://git@git.example.com:2222/org/scripts.git", true},
		{"ssh://git.example.com/org/scripts", true},
		{"git@git.example.com:org/scripts.git", true},
		{"-oProxyCommand=touch /tmp/x", false},
		{"ssh://-oProxyCommand=touch/x", false},
		{"ssh://git@-oProxyCommand=x/org/r.git", false},
		{"-git@git.example.com:org/r.git", false},
		{"git@git.example.com:-org/r.git", false},
		{"ssh://git@git.example.com/-org/r.git", false},
		{"file:///srv/repo.git", false},
		{"/srv/repo.git", false},
		{"./repo.git", false},
		{"ext::sh -c touch% /tmp/pwned", false},
		{"fd::17", false},
		{"http://git.example.com/org/r.git", false},
		{"https://git.example.com/org/r.git", false},
		{"git://git.example.com/org/r.git", false},
		{"ssh://git@git.example.com/org/r.git?x=1", false},
		{"ssh://git@git.example.com/org/../r.git", false},
		{"ssh://git@git.example.com/org/r.git\n", false},
		{"ssh://git@git.example.com/org/r .git", false},
		{"ssh://git@git.example.com", false},
		{"ssh://git@git.example.com/", false},
		{"git@git.example.com:", false},
		{"", false},
	}
	for _, tt := range tests {
		err := CheckRepoURL(tt.in)
		if (err == nil) != tt.ok {
			t.Errorf("CheckRepoURL(%q) = %v, want ok=%v", tt.in, err, tt.ok)
		}
	}
}

func TestValidPinPath(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"invoice-check", true},
		{"tools/invoice_check", true},
		{"a.b/c", true},
		{"", false},
		{"/etc", false},
		{"..", false},
		{"a/../b", false},
		{"a/..", false},
		{".", false},
		{"a/./b", false},
		{"a//b", false},
		{"a/", false},
		{".git", false},
		{"a/.git/b", false},
		{"a/.GIT", false},
		{"a\\b", false},
		{"a\nb", false},
		{"-x", false},
	}
	for _, tt := range tests {
		if got := ValidPinPath(tt.in); got != tt.want {
			t.Errorf("ValidPinPath(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestValidPinName(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"invoice_check", true},
		{"a", true},
		{"a1_b2", true},
		{strings.Repeat("a", 48), true},
		{strings.Repeat("a", 49), false},
		{"", false},
		{"Invoice", false},
		{"invoice-check", false},
		{"a b", false},
	}
	for _, tt := range tests {
		if got := ValidPinName(tt.in); got != tt.want {
			t.Errorf("ValidPinName(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
