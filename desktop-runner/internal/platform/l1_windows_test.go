//go:build windows

package platform

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
)

func TestWindowsK8UhrmarkeDPAPINeustart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := SecureRoot(root); err != nil {
		t.Fatal(err)
	}
	st, err := client.OpenState(root, DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if _, err = st.ObserveTime(now); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "clock")
	raw, err := os.ReadFile(path)
	if err != nil || bytes.Contains(raw, []byte("2026")) {
		t.Fatal("Uhrmarke im Klartext", err)
	}
	if err = CheckPrivateACL(path); err != nil {
		t.Fatal(err)
	}
	st, err = client.OpenState(root, DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.ObserveTime(now.Add(-time.Hour))
	if err != nil || !got.Equal(now) {
		t.Fatal("Uhrmarke verloren", got, err)
	}
	raw[len(raw)/2] ^= 1
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ObserveTime(now); err == nil {
		t.Fatal("manipulierte DPAPI-Uhrmarke akzeptiert")
	}
}
