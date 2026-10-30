package platform

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"
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
		return nil, winError("CryptProtectData", e)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
func (DPAPI) Open(b []byte) ([]byte, error) {
	in, ent := blob(b), blob(entropy)
	var out windows.DataBlob
	if e := windows.CryptUnprotectData(&in, nil, &ent, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); e != nil {
		return nil, winError("CryptUnprotectData", e)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
func userSID() (*windows.SID, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, winError("GetTokenUser", e)
	}
	return u.User.Sid, nil
}
func PrivateSD() (*windows.SECURITY_DESCRIPTOR, error) {
	sid, e := userSID()
	if e != nil {
		return nil, e
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid.String() + ")")
	if e != nil {
		return nil, winError("ConvertStringSecurityDescriptor", e)
	}
	return sd, nil
}
func SecureRoot(root string) error { return secureDir(root, false) }

// MkdirPrivate legt ein Verzeichnis mit der privaten DACL des Läufer-Ordners an
// und prüft sie danach. exclusive: ein vorhandenes Verzeichnis wird abgewiesen,
// bevor irgendetwas daran verändert wird (Laufverzeichnisse, #1245 L4).
func MkdirPrivate(path string, exclusive bool) error { return secureDir(path, exclusive) }

func secureDir(root string, exclusive bool) error {
	abs, e := filepath.Abs(root)
	if e != nil {
		return winError("RootAbsolutePath", e)
	}
	// Refuse junctions/reparse points on every existing path component.
	for p := abs; ; p = filepath.Dir(p) {
		u, e := windows.UTF16PtrFromString(p)
		if e != nil {
			return winError("RootPathUTF16", e)
		}
		attr, e := windows.GetFileAttributes(u)
		if e != nil && !errors.Is(e, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
			return winError("GetFileAttributes", e)
		}
		if e == nil && attr&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return winError("RootReparsePoint", nil)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if e = os.MkdirAll(filepath.Dir(abs), 0700); e != nil {
		return winError("CreateRootParents", e)
	}
	sd, e := PrivateSD()
	if e != nil {
		return e
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	ptr, e := windows.UTF16PtrFromString(abs)
	if e != nil {
		return winError("RootPathUTF16", e)
	}
	e = windows.CreateDirectory(ptr, &sa)
	if e == windows.ERROR_ALREADY_EXISTS && exclusive {
		return winError("DirectoryExists", e)
	}
	if e != nil && e != windows.ERROR_ALREADY_EXISTS {
		return winError("CreateDirectory", e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		return winError("GetSecurityDescriptorDacl", e)
	}
	if e = windows.SetNamedSecurityInfo(abs, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		return winError("SetNamedSecurityInfo", e)
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
		return winError("GetNamedSecurityInfo", e)
	}
	// One explicit allow ACE, no inherited or alternative principal.
	return checkPrivateDescriptor(sd, sid)
}
func checkPrivateDescriptor(sd *windows.SECURITY_DESCRIPTOR, sid *windows.SID) error {
	control, _, e := sd.Control()
	if e != nil {
		return winError("GetSecurityDescriptorControl", e)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return winError("PrivateACLUnprotected", nil)
	}
	acl, defaulted, e := sd.DACL()
	if e != nil {
		return winError("GetSecurityDescriptorDacl", e)
	}
	if acl == nil || defaulted || acl.AceCount != 1 {
		return winError("PrivateACLCount", nil)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if e = windows.GetAce(acl, 0, &ace); e != nil {
		return winError("GetAce", e)
	}
	if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return winError("PrivateACLType", nil)
	}
	if ace.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
		return winError("PrivateACLFlags", nil)
	}
	// FILE_ALL_ACCESS; require the exact directory/file rights we install.
	if ace.Mask != 0x001f01ff {
		return winError("PrivateACLRights", nil)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.IsValid() || !sid.Equals(aceSID) {
		return winError("PrivateACLPrincipal", nil)
	}
	runtime.KeepAlive(sd)
	return nil
}
func Lock() (func(), error) {
	sid, e := userSID()
	if e != nil {
		return nil, e
	}
	name, e := windows.UTF16PtrFromString("Global\\JanuaPort-Desktop-" + sid.String())
	if e != nil {
		return nil, winError("MutexNameUTF16", e)
	}
	h, e := windows.CreateMutex(nil, false, name)
	if e != nil {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, winError("CreateMutex", e)
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
