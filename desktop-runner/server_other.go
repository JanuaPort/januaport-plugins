//go:build !linux

package main

import (
	"io"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
)

func runServer([]string, io.Writer) error { return client.ErrProtocol }
