//go:build linux

package storagepins

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openDirectory pins every directory component, not just the final filename:
// O_NOFOLLOW alone on a pathname would still follow a hostile inbox/pkg link.
func openDirectory(path string) (*os.File, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, ErrInvalid
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, ErrInvalid
	}
	for _, part := range strings.Split(strings.TrimPrefix(abs, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, ErrInvalid
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "storage-dir"), nil
}

// ReadFile never follows links, blocks on FIFOs, or reads more than limit+1.
func ReadFile(path string, limit int64) ([]byte, error) {
	if limit < 0 || limit > MaxTotalBytes {
		return nil, ErrInvalid
	}
	dir, e := openDirectory(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer func() { _ = dir.Close() }()
	fd, e := unix.Openat(int(dir.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, ErrInvalid
	}
	f := os.NewFile(uintptr(fd), "storage-file")
	defer func() { _ = f.Close() }()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return nil, ErrInvalid
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, ErrInvalid
	}
	return b, nil
}

func randomName() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return ""
	}
	return ".state-" + hex.EncodeToString(b[:])
}

func atomicWrite(root string, b []byte, name func() string) error {
	return atomicFile(root, "state.json", b, MaxListBytes, name)
}

// WriteFile publishes bounded opaque inbox bytes with the same durable,
// exclusive-temp protocol as state.json. Only the courier calls this API.
func WriteFile(path string, b []byte, limit int64) error {
	return atomicFile(filepath.Dir(path), filepath.Base(path), b, limit, randomName)
}

func atomicFile(root, target string, b []byte, limit int64, name func() string) error {
	if limit < 0 || limit > MaxPackageBytes || int64(len(b)) > limit || target == "." || target == ".." || strings.ContainsAny(target, "/\\") {
		return ErrInvalid
	}
	dir, e := openDirectory(root)
	if e != nil {
		return e
	}
	defer func() { _ = dir.Close() }()
	tmp := name()
	if !strings.HasPrefix(tmp, ".state-") || strings.ContainsAny(tmp, "/\\") {
		return ErrInvalid
	}
	fd, e := unix.Openat(int(dir.Fd()), tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return ErrInvalid
	}
	defer func() { _ = unix.Unlinkat(int(dir.Fd()), tmp, 0) }()
	f := os.NewFile(uintptr(fd), "storage-temp")
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	closed := f.Close()
	if e != nil || closed != nil {
		return ErrInvalid
	}
	if unix.Renameat(int(dir.Fd()), tmp, int(dir.Fd()), target) != nil || dir.Sync() != nil {
		return ErrInvalid
	}
	return nil
}

// DirectoryNames and RemoveFile keep cleanup anchored to a no-follow directory.
func DirectoryNames(root string) ([]string, error) {
	dir, e := openDirectory(root)
	if e != nil {
		return nil, e
	}
	defer func() { _ = dir.Close() }()
	entries, e := dir.ReadDir(-1)
	if e != nil {
		return nil, ErrInvalid
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}

func RemoveFile(path string) error {
	dir, e := openDirectory(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer func() { _ = dir.Close() }()
	if e = unix.Unlinkat(int(dir.Fd()), filepath.Base(path), 0); e != nil && e != unix.ENOENT {
		return ErrInvalid
	}
	if dir.Sync() != nil {
		return ErrInvalid
	}
	return nil
}

// EnsureDirectory creates only speicher, never inbox, beneath the trusted
// store root. An existing symlink is rejected by openDirectory.
func EnsureDirectory(root string) error {
	parent, e := openDirectory(filepath.Dir(root))
	if e != nil {
		return e
	}
	defer func() { _ = parent.Close() }()
	e = unix.Mkdirat(int(parent.Fd()), filepath.Base(root), 0700)
	if e != nil && e != unix.EEXIST {
		return ErrInvalid
	}
	dir, e := openDirectory(root)
	if e != nil {
		return e
	}
	return dir.Close()
}
