//go:build linux

package client

import (
	"os"
	"path/filepath"
	"syscall"
)

func checkSecretsRoot(root string) error {
	for p := filepath.Clean(root); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrSecret
		}
		if p == filepath.Clean(root) {
			s, ok := info.Sys().(*syscall.Stat_t)
			if !ok || s.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0700 {
				return ErrSecret
			}
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
