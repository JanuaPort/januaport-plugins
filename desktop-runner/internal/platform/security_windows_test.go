package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsCIContext(t *testing.T) {
	elevated := windows.GetCurrentProcessToken().IsElevated()
	t.Logf("elevated=%t temp_on_profile_volume=%t", elevated, strings.EqualFold(filepath.VolumeName(os.TempDir()), filepath.VolumeName(os.Getenv("LOCALAPPDATA"))))
	if os.Getenv("DESKTOP_EXPECT_ELEVATED") == "1" && !elevated {
		t.Fatal("erhöhter Kontext erwartet")
	}
}

func TestWindowsFehlerBehaltenUrsache(t *testing.T) {
	_, err := (DPAPI{}).Open([]byte("invalid ciphertext"))
	var errno syscall.Errno
	if !errors.As(err, &errno) || errno == 0 {
		t.Fatalf("CryptUnprotectData verliert Windows-Fehler: %v", err)
	}
	if !strings.Contains(err.Error(), "CryptUnprotectData") {
		t.Fatal("Operation fehlt", err)
	}
	err = CheckPrivateACL(filepath.Join(t.TempDir(), "PRIVATE_PATH", "missing"))
	if !errors.As(err, &errno) || !strings.Contains(err.Error(), "GetNamedSecurityInfo") || strings.Contains(err.Error(), "PRIVATE_PATH") {
		t.Fatal("Diagnose enthält keinen sicheren Operations-/Fehlercode")
	}
}

func TestPrivateACLAliasUndVerbote(t *testing.T) {
	own, err := userSID()
	if err != nil {
		t.Fatal(err)
	}
	// Native SDDL canonicalization uses SY/BA/LA instead of numeric SIDs.
	// Exercise the same descriptor validator as the real filesystem adapter.
	for _, id := range []string{own.String(), "S-1-5-18", "S-1-5-32-544", own.String()[:strings.LastIndex(own.String(), "-")+1] + "500"} {
		sid, err := windows.StringToSid(id)
		if err != nil {
			t.Fatal(err)
		}
		sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + id + ")")
		if err != nil {
			t.Fatal(err)
		}
		if err = checkPrivateDescriptor(sd, sid); err != nil {
			t.Errorf("identische SID trotz SDDL-Alias abgewiesen: %v", err)
		}
	}
	for _, bad := range []string{
		"D:P", "D:NO_ACCESS_CONTROL",
		"D:(A;OICI;FA;;;" + own.String() + ")",
		"D:P(A;OICI;FA;;;WD)",
		"D:P(A;OICI;FA;;;" + own.String() + ")(A;;FR;;;WD)",
		"D:P(D;OICI;FA;;;" + own.String() + ")",
		"D:P(A;OICIID;FA;;;" + own.String() + ")",
		"D:P(A;OICIIO;FA;;;" + own.String() + ")",
		"D:P(A;;FA;;;" + own.String() + ")",
		"D:P(A;OICI;FR;;;" + own.String() + ")",
	} {
		sd, err := windows.SecurityDescriptorFromString(bad)
		if err != nil {
			t.Fatal("Test-SDDL", err)
		}
		if err = checkPrivateDescriptor(sd, own); err == nil {
			t.Error("unsichere DACL akzeptiert")
		}
	}
}

func TestPrivateACLAmDateisystem(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := SecureRoot(root); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	sid, err := userSID()
	if err != nil {
		t.Fatal(err)
	}
	for _, tail := range []string{"(A;;FR;;;WD)", "(A;;FR;;;BA)"} {
		sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid.String() + ")" + tail)
		if err != nil {
			t.Fatal(err)
		}
		acl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if err = windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			t.Fatal(err)
		}
		if err = CheckPrivateACL(root); err == nil {
			t.Fatal("zusätzlicher Zugriff akzeptiert")
		}
		if err = SecureRoot(root); err != nil {
			t.Fatal("Reparatur", err)
		}
	}
	// Repair must also remove the additional inherited grants from this child.
	sd, err := windows.GetNamedSecurityInfo(child, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 1 {
		t.Fatal("geerbte ACL nicht repariert", err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err = windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
		t.Fatal("fremde geerbte Berechtigung")
	}
}

func TestSecureRootVerweigertJunction(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	link := filepath.Join(base, "link")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	// Directory junction creation needs no symlink privilege.
	if err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).Run(); err != nil {
		t.Fatal("Test-Junction", err)
	}
	if err := SecureRoot(filepath.Join(link, "private")); err == nil || !strings.Contains(err.Error(), "RootReparsePoint") {
		t.Fatal("Junction nicht abgewiesen", err)
	}
	if _, err := os.Stat(filepath.Join(target, "private")); !os.IsNotExist(err) {
		t.Fatal("vor Abweisung geschrieben")
	}
}
