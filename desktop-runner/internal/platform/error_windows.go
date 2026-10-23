package platform

import (
	"errors"
	"fmt"
	"syscall"
)

// Operation names are fixed source literals. Keep only the OS error number:
// PathError, account names, input and credentials never enter diagnostics.
type operationError struct {
	op    string
	errno syscall.Errno
}

func (e *operationError) Error() string {
	if e.errno == 0 {
		return "windows_operation: " + e.op
	}
	return fmt.Sprintf("windows_operation: %s: errno=%d (%s)", e.op, uint32(e.errno), e.errno.Error())
}
func (e *operationError) Unwrap() error        { return e.errno }
func (e *operationError) Is(target error) bool { return target == errPlatform }
func winError(op string, err error) error {
	var errno syscall.Errno
	errors.As(err, &errno)
	return &operationError{op, errno}
}
