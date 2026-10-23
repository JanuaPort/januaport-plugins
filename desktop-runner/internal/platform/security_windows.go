package platform

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"
)

var errPlatform = errors.New("windows_operation")

type DPAPI struct{}

var entropy = []byte("JanuaPort/Desktop-Laeufer/v1/CurrentUser")

func blob(b []byte) windows.DataBlob {
	v := windows.DataBlob{Size: uint32(len(b))}
	if len(b) > 0 {
		v.Data = &b[0]
	}
	return v
}
func (DPAPI) Seal(b []byte) ([]byte, error) {
	in, ent := blob(b), blob(entropy)
	var out windows.DataBlob
	if e := windows.CryptProtectData(&in, nil, &ent, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); e != nil {
		return nil, errPlatform
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
func (DPAPI) Open(b []byte) ([]byte, error) {
	in, ent := blob(b), blob(entropy)
	var out windows.DataBlob
	if e := windows.CryptUnprotectData(&in, nil, &ent, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); e != nil {
		return nil, errPlatform
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
func userSID() (*windows.SID, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, e
	}
	return u.User.Sid, nil
}
func PrivateSD() (*windows.SECURITY_DESCRIPTOR, error) {
	sid, e := userSID()
	if e != nil {
		return nil, e
	}
	return windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid.String() + ")")
}
func SecureRoot(root string) error {
	abs, e := filepath.Abs(root)
	if e != nil {
		return e
	}
	// Refuse junctions/reparse points on every existing path component.
	for p := abs; ; p = filepath.Dir(p) {
		u, e := windows.UTF16PtrFromString(p)
		if e != nil {
			return e
		}
		attr, e := windows.GetFileAttributes(u)
		if e == nil && attr&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errPlatform
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if e = os.MkdirAll(filepath.Dir(abs), 0700); e != nil {
		return e
	}
	sd, e := PrivateSD()
	if e != nil {
		return e
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	ptr, e := windows.UTF16PtrFromString(abs)
	if e != nil {
		return e
	}
	e = windows.CreateDirectory(ptr, &sa)
	if e != nil && e != windows.ERROR_ALREADY_EXISTS {
		return e
	}
	acl, _, e := sd.DACL()
	if e != nil {
		return e
	}
	if e = windows.SetNamedSecurityInfo(abs, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		return e
	}
	return CheckPrivateACL(abs)
}
func CheckPrivateACL(root string) error {
	sid, e := userSID()
	if e != nil {
		return e
	}
	sd, e := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	// One explicit allow ACE, no inherited or alternative principal.
	return checkPrivateDescriptor(sd, sid)
}
func checkPrivateDescriptor(sd *windows.SECURITY_DESCRIPTOR, sid *windows.SID) error {
	s := sd.String()
	if !strings.Contains(s, "D:P") || strings.Count(s, "(") != 1 || !strings.Contains(s, ";;;"+sid.String()+")") {
		return errPlatform
	}
	return nil
}
func Lock() (func(), error) {
	sid, e := userSID()
	if e != nil {
		return nil, e
	}
	name, e := windows.UTF16PtrFromString("Global\\JanuaPort-Desktop-" + sid.String())
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateMutex(nil, false, name)
	if e != nil {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, errPlatform
	}
	return func() { windows.CloseHandle(h) }, nil
}
func WindowsEnv() []string {
	root, e := windows.GetWindowsDirectory()
	if e != nil {
		return nil
	}
	return []string{"SystemRoot=" + root, "WINDIR=" + root, "OPENBLAS_NUM_THREADS=1", "OMP_NUM_THREADS=1"}
}
func Root() (string, error) {
	base, e := os.UserCacheDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(base, "JanuaPort", "desktop-runner"), nil
}
func pinThread() func() { runtime.LockOSThread(); return runtime.UnlockOSThread }
