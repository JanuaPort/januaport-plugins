//go:build !windows

package platform

import (
	"context"
	"errors"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
)

var errPlatform = errors.New("windows_required")

type DPAPI struct{}

func (DPAPI) Seal([]byte) ([]byte, error)                          { return nil, errPlatform }
func (DPAPI) Open([]byte) ([]byte, error)                          { return nil, errPlatform }
func SecureRoot(string) error                                      { return errPlatform }
func Root() (string, error)                                        { return "", errPlatform }
func Lock() (func(), error)                                        { return nil, errPlatform }
func WindowsEnv() []string                                         { return nil }
func Session() (client.Session, error)                             { return client.Session{}, errPlatform }
func Spawn(context.Context, client.Launch) (client.Process, error) { return nil, errPlatform }
func Install() (bool, error)                                       { return false, errPlatform }
func Uninstall() error                                             { return errPlatform }
func Installed() bool                                              { return false }
