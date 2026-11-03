package pipe

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestPipeDACLStruktur(t *testing.T) {
	const user = "S-1-5-21-1-2-3-1001"
	const logon = "S-1-5-5-1-2"
	want := "D:P(A;;0x12008b;;;" + user + ")(A;;0x12008b;;;" + logon + ")S:(ML;;NWNR;;;ME)"
	if got := pipeSDDL(user, logon); got != want {
		t.Fatalf("DACL/Label: %s", got)
	}
	h := sha256.Sum256([]byte(user))
	if PipeName(user) != fmt.Sprintf(`\\.\pipe\JanuaPort.Bedienfeld.%x`, h[:8]) {
		t.Fatal("Pipe-Name")
	}
	b, err := os.ReadFile("transport_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"windows.PIPE_REJECT_REMOTE_CLIENTS", "windows.FILE_FLAG_FIRST_PIPE_INSTANCE", "windows.GetNamedPipeClientProcessId", "windows.GetNamedPipeServerProcessId"} {
		if !strings.Contains(string(b), s) {
			t.Errorf("fehlend: %s", s)
		}
	}
}

func TestClientPrueftServerbild(t *testing.T) {
	own := Peer{Image: `C:\JanuaPort\jnpt-bedienfeld.exe`, UserSID: "S-1-5-21-1", Publisher: "publisher"}
	good := Peer{Image: `C:\JanuaPort\desktop-runner.exe`, UserSID: own.UserSID, Publisher: own.Publisher}
	if verifyServer(own, good) != nil {
		t.Fatal("passender Server")
	}
	for _, bad := range []Peer{
		{Image: `C:\Fremd\desktop-runner.exe`, UserSID: good.UserSID, Publisher: good.Publisher},
		{Image: `C:\JanuaPort\other.exe`, UserSID: good.UserSID, Publisher: good.Publisher},
		{Image: good.Image, UserSID: "S-1-5-21-2", Publisher: good.Publisher},
		{Image: good.Image, UserSID: good.UserSID, Publisher: "other"},
		{Image: good.Image, UserSID: good.UserSID},
	} {
		if err := verifyServer(own, bad); err != ErrDisturbance {
			t.Fatalf("Server zugelassen: %+v (%v)", bad, err)
		}
	}
	if verifyServer(Peer{Image: own.Image, UserSID: own.UserSID}, Peer{Image: good.Image, UserSID: good.UserSID}) == nil {
		t.Fatal("unsignierte Peers")
	}
}
