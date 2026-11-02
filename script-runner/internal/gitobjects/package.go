// Package gitobjects verifies storage packages without Git, subprocesses or IO.
package gitobjects

import "errors"

// ErrHash is the only outward package error; no input is echoed.
var ErrHash = errors.New("hash_mismatch")

// Verify binds every object and the complete graph to the signed pin.
func Verify(b []byte, commit, path string) error {
	if decodePackage(b, "sha256", commit, path) != nil {
		return ErrHash
	}
	return nil
}

// Canonical implements the shared signing contract, including duplicate and
// Unicode rejection. Callers must separately enforce their exact schema.
func Canonical(b []byte) ([]byte, error) {
	out, err := canonical(b)
	if err != nil {
		return nil, ErrHash
	}
	return out, nil
}
