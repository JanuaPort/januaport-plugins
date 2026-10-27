//go:build !linux

package client

import "testing"

func TestTokenNichtInInspect(t *testing.T) {
	t.Skip("Echter Linux-Job benötigt den Linux-Prüfläufer; im Linux-CI mit Docker verpflichtend")
}

func TestJobLiestGeraetetokenNicht(t *testing.T) {
	t.Skip("Datei-/UID-Isolation benötigt Linux; im Linux-CI mit Docker verpflichtend")
}
