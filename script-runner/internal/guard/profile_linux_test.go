//go:build linux

package guard

import (
	"syscall"
	"testing"
)

// F3 B2: nur EPERM auf process_vm_readv belegt unser Profil. EINVAL heißt:
// der Aufruf hat den Kernel erreicht (Docker-Standard), Erfolg oder ENOSYS
// heißt: kein oder ein fremder Filter.
func TestProfileFromErrno(t *testing.T) {
	tests := []struct {
		errno syscall.Errno
		want  string
	}{
		{syscall.EPERM, "jnpt"},
		{syscall.EINVAL, "other"},
		{syscall.ENOSYS, "other"},
		{syscall.EACCES, "other"},
		{0, "other"},
	}
	for _, tt := range tests {
		if got := profileFromErrno(tt.errno); got != tt.want {
			t.Errorf("profileFromErrno(%v) = %q, want %q", tt.errno, got, tt.want)
		}
	}
}

// Im Test-Container (Docker-Standardprofil oder ohne Filter) darf der
// Selbsttest nie „jnpt“ melden.
func TestSelfProfileOutsideSandbox(t *testing.T) {
	if got := selfProfile(); got != "other" {
		t.Fatalf("selfProfile() = %q ohne unser Profil", got)
	}
}
