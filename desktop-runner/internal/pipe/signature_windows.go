//go:build windows

package pipe

import (
	"encoding/hex"
	"golang.org/x/sys/windows"
	"runtime"
	"unsafe"
)

// publisher reads the signer certificate from WinVerifyTrust's verified chain,
// not an unverified certificate embedded alongside a valid signature. Compare
// the complete DER subject (publisher identity), allowing certificate renewal.
// Pin the file against write/delete during verification; no UI or network fetch.
func publisher(image string) (string, error) {
	path, err := windows.UTF16PtrFromString(image)
	if err != nil {
		return "", ErrDisturbance
	}
	h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return "", ErrDisturbance
	}
	defer windows.CloseHandle(h)
	info := windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: path, File: h}
	data := windows.WinTrustData{Size: uint32(unsafe.Sizeof(windows.WinTrustData{})), UIChoice: windows.WTD_UI_NONE, RevocationChecks: windows.WTD_REVOKE_WHOLECHAIN,
		UnionChoice: windows.WTD_CHOICE_FILE, FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&info), StateAction: windows.WTD_STATEACTION_VERIFY,
		ProvFlags: windows.WTD_CACHE_ONLY_URL_RETRIEVAL | windows.WTD_REVOCATION_CHECK_CHAIN_EXCLUDE_ROOT | windows.WTD_DISABLE_MD2_MD4}
	defer func() {
		data.StateAction = windows.WTD_STATEACTION_CLOSE
		windows.WinVerifyTrustEx(0, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
		runtime.KeepAlive(info)
	}()
	if windows.WinVerifyTrustEx(0, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data) != nil {
		return "", ErrDisturbance
	}
	dll := windows.NewLazySystemDLL("wintrust.dll")
	provider, _, _ := dll.NewProc("WTHelperProvDataFromStateData").Call(uintptr(data.StateData))
	if provider == 0 {
		return "", ErrDisturbance
	}
	signer, _, _ := dll.NewProc("WTHelperGetProvSignerFromChain").Call(provider, 0, 0, 0)
	if signer == 0 {
		return "", ErrDisturbance
	}
	cert, _, _ := dll.NewProc("WTHelperGetProvCertFromChain").Call(signer, 0)
	if cert == 0 {
		return "", ErrDisturbance
	}
	// CRYPT_PROVIDER_CERT starts with cbStruct and an aligned PCCERT_CONTEXT.
	type providerCert struct {
		Size uint32
		Cert *windows.CertContext
	}
	// The API returns a native address. Copy the small prefix through the OS
	// rather than retaining a uintptr-to-Go-pointer conversion across calls.
	var c providerCert
	var read uintptr
	if windows.ReadProcessMemory(windows.CurrentProcess(), cert, (*byte)(unsafe.Pointer(&c)), unsafe.Sizeof(c), &read) != nil || read != unsafe.Sizeof(c) {
		return "", ErrDisturbance
	}
	if c.Cert == nil || c.Cert.CertInfo == nil {
		return "", ErrDisturbance
	}
	subject := c.Cert.CertInfo.Subject
	if subject.Size == 0 || subject.Size > 65536 || subject.Data == nil {
		return "", ErrDisturbance
	}
	return hex.EncodeToString(unsafe.Slice(subject.Data, subject.Size)), nil
}
