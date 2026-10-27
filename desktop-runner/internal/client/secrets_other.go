//go:build !linux

package client

import "os"

func checkSecretsRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrSecret
	}
	return nil
}
