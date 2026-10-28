//go:build linux

package platform

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"golang.org/x/sys/unix"
)

var errPlatform = errors.New("linux_security")

// FileIntegrity authenticates plaintext; confidentiality is enforced by 0700 /
// 0600 and the separate job mount namespace, not DPAPI or a shared job UID.
type FileIntegrity struct{ key []byte }

func (c FileIntegrity) ReadFile(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !privateInfo(info, 0600) || info.Size() > 20<<20 {
		return nil, errPlatform
	}
	return io.ReadAll(io.LimitReader(f, 20<<20+1))
}

func (c FileIntegrity) Seal(b []byte) ([]byte, error) {
	m := hmac.New(sha256.New, c.key)
	m.Write(b)
	return append(m.Sum(nil), b...), nil
}
func (c FileIntegrity) Open(b []byte) ([]byte, error) {
	if len(b) < sha256.Size {
		return nil, errPlatform
	}
	m := hmac.New(sha256.New, c.key)
	m.Write(b[sha256.Size:])
	if !hmac.Equal(m.Sum(nil), b[:sha256.Size]) {
		return nil, errPlatform
	}
	return append([]byte{}, b[sha256.Size:]...), nil
}
func privateInfo(info os.FileInfo, mode os.FileMode) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid()) && info.Mode().Perm() == mode
}
func SecureRoot(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for p := abs; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return errPlatform
		}
		if e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return errPlatform
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil || !privateInfo(info, 0700) {
		return errPlatform
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil {
			return errPlatform
		}
		if info.IsDir() {
			if SecureRoot(filepath.Join(abs, entry.Name())) != nil {
				return errPlatform
			}
		} else if !info.Mode().IsRegular() || !privateInfo(info, 0600) {
			return errPlatform
		}
	}
	return nil
}
func NewFileIntegrity(root string) (FileIntegrity, error) {
	if err := SecureRoot(root); err != nil {
		return FileIntegrity{}, err
	}
	path := filepath.Join(root, "integrity-key")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err == nil {
		f := os.NewFile(uintptr(fd), path)
		key := make([]byte, 32)
		if _, err = rand.Read(key); err == nil {
			_, err = f.Write(key)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return FileIntegrity{}, err
		}
	} else if !errors.Is(err, unix.EEXIST) {
		return FileIntegrity{}, err
	}
	fd, err = unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return FileIntegrity{}, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !privateInfo(info, 0600) {
		return FileIntegrity{}, errPlatform
	}
	key, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil || len(key) != 32 {
		return FileIntegrity{}, errPlatform
	}
	return FileIntegrity{key}, nil
}

// The lock lives outside the revocable state directory so unlinking credentials
// cannot create a second lock inode while the old runner is still stopping.
func LockFile(path string) (func(), error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || !privateInfo(info, 0600) {
		f.Close()
		return nil, errPlatform
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

// Legacy Windows entry points stay fail-closed on Linux. runServer supplies
// FileIntegrity explicitly and never calls a Python/session/DPAPI adapter.
type DPAPI struct{}

func (DPAPI) Seal([]byte) ([]byte, error)                          { return nil, errPlatform }
func (DPAPI) Open([]byte) ([]byte, error)                          { return nil, errPlatform }
func Root() (string, error)                                        { return "", errPlatform }
func Lock() (func(), error)                                        { return nil, errPlatform }
func WindowsEnv() []string                                         { return nil }
func Session() (client.Session, error)                             { return client.Session{}, errPlatform }
func Spawn(context.Context, client.Launch) (client.Process, error) { return nil, errPlatform }
func Install() (bool, error)                                       { return false, errPlatform }
func Uninstall() error                                             { return errPlatform }
func Installed() bool                                              { return false }
