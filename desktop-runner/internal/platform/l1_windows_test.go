//go:build windows

package platform

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"golang.org/x/sys/windows"
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
	// CheckPrivateACL requires the explicit, protected directory DACL.
	// State files inherit its user-only ACE and need not be DACL-protected.
	if err = CheckPrivateACL(root); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 1 {
		t.Fatal("Uhrdatei braucht genau eine private Berechtigung", err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err = windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	sid, err := userSID()
	if err != nil {
		t.Fatal(err)
	}
	const fileAllAccess = 0x001f01ff
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != windows.INHERITED_ACE || ace.Mask != fileAllAccess || !sid.Equals((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
		t.Fatal("Uhrdatei erbt nicht ausschließlich volle Rechte des eigenen Benutzers")
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
