//go:build !linux

package storagepins

// The server fetcher is Linux-only; other builds fail closed.
func ReadFile(string, int64) ([]byte, error)          { return nil, ErrInvalid }
func EnsureDirectory(string) error                    { return ErrInvalid }
func randomName() string                              { return "" }
func atomicWrite(string, []byte, func() string) error { return ErrInvalid }
func WriteFile(string, []byte, int64) error           { return ErrInvalid }
func DirectoryNames(string) ([]string, error)         { return nil, ErrInvalid }
func RemoveFile(string) error                         { return ErrInvalid }
