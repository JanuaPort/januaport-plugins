package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// #1537 Punkt 5: Die Instanzsperre ist eine exklusive LockFileEx-Sperre auf eine Datei im privat abgesicherten
// Wurzelordner, kein benanntes Objekt im globalen Namensraum. Ein anderes lokales Konto kann den Namen
// `Global\JanuaPort-Desktop-<SID>` vorab belegen, aber nicht den privaten Ordner.

func lockRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "desktop-runner")
}

// Eine zweite Instanz desselben Benutzers scheitert, auch im selben Prozess (die Sperre hängt am Handle), und kommt
// nach dem Freigeben der ersten durch.
func TestInstanzsperreZweiteInstanzScheitertBisZurFreigabe(t *testing.T) {
	root := lockRoot(t)
	unlock, err := lockIn(root)
	if err != nil {
		t.Fatal("erste Instanz:", err)
	}
	if second, err := lockIn(root); err == nil {
		second()
		t.Fatal("zweite Instanz bekam die Sperre")
	} else if _, ok := err.(*operationError); !ok {
		t.Fatalf("Fehler %T, erwartet der feste Operationsfehler", err)
	}
	unlock()
	third, err := lockIn(root)
	if err != nil {
		t.Fatal("nach der Freigabe:", err)
	}
	third()
}

// Die Freigabe darf mehrfach laufen (defer plus Fehlerpfad), ohne eine fremde Sperre zu lösen.
func TestInstanzsperreFreigabeIstIdempotent(t *testing.T) {
	root := lockRoot(t)
	unlock, err := lockIn(root)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	other, err := lockIn(root)
	if err != nil {
		t.Fatal(err)
	}
	defer other()
	unlock() // zweites Freigeben der ersten Instanz
	if again, err := lockIn(root); err == nil {
		again()
		t.Fatal("die doppelte Freigabe löste die Sperre der zweiten Instanz")
	}
}

// Kern der Härtung: Wer den Namen Global\JanuaPort-Desktop-<SID> belegt, sperrt den Läufer nicht mehr aus.
func TestInstanzsperreIstUnabhaengigVomGlobalenMutexNamen(t *testing.T) {
	sid, err := userSID()
	if err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(`Global\JanuaPort-Desktop-` + sid.String())
	if err != nil {
		t.Fatal(err)
	}
	squatter, _ := windows.CreateMutex(nil, true, name)
	if squatter == 0 {
		t.Skip("Mutex im globalen Namensraum nicht anlegbar")
	}
	defer windows.CloseHandle(squatter)
	unlock, err := lockIn(lockRoot(t))
	if err != nil {
		t.Fatal("belegter Mutexname sperrt den Läufer aus:", err)
	}
	unlock()
}

// Die Sperrdatei liegt im Wurzelordner, der vorher privat abgesichert wird, und erbt dessen einzigen Zugriffseintrag:
// nur der eigene Benutzer.
func TestInstanzsperreDateiImPrivatenWurzelordner(t *testing.T) {
	root := lockRoot(t)
	unlock, err := lockIn(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err = CheckPrivateACL(root); err != nil {
		t.Fatal("Wurzelordner nicht privat:", err)
	}
	file := filepath.Join(root, lockFileName)
	sd, err := windows.GetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal("Sperrdatei fehlt:", err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 1 {
		t.Fatalf("Sperrdatei hat mehr als einen Zugriffseintrag: %v", err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err = windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	sid, _ := userSID()
	if !sid.Equals((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
		t.Fatal("fremder Benutzer auf der Sperrdatei")
	}
}

// Widerruf: State.Revoke löscht die Wurzel samt Sperrdatei, auch wenn dieser Prozess sie noch hält. Ohne
// FILE_SHARE_DELETE bliebe der Ordner stehen und der Widerruf scheiterte.
func TestInstanzsperreBehindertDenWiderrufNicht(t *testing.T) {
	root := lockRoot(t)
	unlock, err := lockIn(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err = os.Remove(filepath.Join(root, lockFileName)); err != nil {
		t.Fatal("Sperrdatei nicht löschbar:", err)
	}
	if err = os.Remove(root); err != nil {
		t.Fatal("Wurzel nicht löschbar:", err)
	}
}

// Ein Verweis anstelle des Wurzelordners wird abgewiesen (secureDir), bevor eine Sperrdatei entsteht.
func TestInstanzsperreVerweigertJunctionAlsWurzel(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "ziel")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).Run(); err != nil {
		t.Skip("Junction nicht anlegbar:", err)
	}
	if unlock, err := lockIn(link); err == nil {
		unlock()
		t.Fatal("Junction als Wurzel akzeptiert")
	}
	if _, err := os.Stat(filepath.Join(target, lockFileName)); err == nil {
		t.Fatal("Sperrdatei hinter der Junction angelegt")
	}
}
