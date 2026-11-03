package pipe

import (
	"crypto/sha256"
	"fmt"
	"path"
	"strings"
)

func PipeName(sid string) string {
	h := sha256.Sum256([]byte(sid))
	return fmt.Sprintf(`\\.\pipe\JanuaPort.Bedienfeld.%x`, h[:8])
}

// B1, L3-1, H1: protected DACL; no AppContainer/capability grants, no create-
// instance bit (FILE_APPEND_DATA). Explicit medium integrity: no read/write up.
func pipeSDDL(user, logon string) string {
	return "D:P(A;;0x12008b;;;" + user + ")(A;;0x12008b;;;" + logon + ")S:(ML;;NWNR;;;ME)"
}

type Peer struct{ Image, UserSID, Publisher string }

func normalized(p string) string { return strings.ToLower(strings.ReplaceAll(p, `\`, "/")) }
func sameFolder(own, peer, base string) bool {
	a, b := normalized(own), normalized(peer)
	absolute := strings.HasPrefix(a, "/") || len(a) > 3 && a[1] == ':' && a[2] == '/'
	return absolute && path.Clean(a) == a && path.Clean(b) == b && path.Dir(a) == path.Dir(b) && path.Base(b) == base
}

func verifyServer(own, server Peer) error {
	if !sameFolder(own.Image, server.Image, "desktop-runner.exe") || own.UserSID == "" || own.UserSID != server.UserSID || own.Publisher == "" || own.Publisher != server.Publisher {
		return ErrDisturbance
	}
	return nil
}
