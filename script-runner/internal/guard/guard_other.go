//go:build !linux

package guard

import (
	"fmt"
	"os"
)

// Run gibt es nur unter Linux: der Wächter braucht prctl, kill(-1) und die
// PID-1-Semantik eines Container-Namespace.
func Run(Config) int {
	fmt.Fprintln(os.Stderr, "guard: nur unter Linux")
	return 1
}
