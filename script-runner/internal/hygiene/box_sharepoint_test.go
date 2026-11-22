package hygiene

import (
	"regexp"
	"strings"
	"testing"
)

// SharePoint-Abgleich der Box (JanuaPort/januaport#1430). Die Unit läuft als
// 65532 unter ProtectSystem=strict: Dateisystem schreibgeschützt bis auf
// ReadWritePaths und StateDirectory. rclone schreibt das erneuerte Token in
// seine Config; liegt deren Ordner außerhalb dieser Liste oder gehört er root,
// scheitert das bei jedem Lauf (zehn vergebliche Versuche), und der Abgleich
// läuft trotzdem weiter, bis das Refresh-Token abläuft.

const (
	sharepointDir  = "../../../box/sharepoint/"
	rcloneConfDir  = "/var/lib/jnpt/rclone-ablage"
	runbookAnlegen = "install -d -o 65532 -g 65532 -m 0700 /var/lib/jnpt/rclone-bisync /var/lib/jnpt/rclone-ablage"
)

func TestAbgleichUnitMayWriteRcloneConfigDirectory(t *testing.T) {
	skript := readFile(t, sharepointDir+"jnpt-ablage-abgleich.sh")
	m := regexp.MustCompile(`(?m)^CONF=(\S+)`).FindStringSubmatch(skript)
	if m == nil {
		t.Fatal("jnpt-ablage-abgleich.sh: keine Zeile CONF=… — der Wächter prüft sonst ins Leere")
	}
	if !strings.HasPrefix(m[1], rcloneConfDir+"/") {
		t.Fatalf("CONF = %q, erwartet unter %s", m[1], rcloneConfDir)
	}
	unit := readFile(t, sharepointDir+"jnpt-ablage-abgleich.service")
	if !strings.Contains(unit, "ProtectSystem=strict") {
		t.Fatal("ProtectSystem=strict fehlt — die Härtung ist Teil der Unit")
	}
	var rw []string
	for _, z := range strings.Split(unit, "\n") {
		if v, ok := strings.CutPrefix(z, "ReadWritePaths="); ok {
			rw = append(rw, strings.Fields(v)...)
		}
	}
	found := false
	for _, p := range rw {
		found = found || p == rcloneConfDir
	}
	if !found {
		t.Errorf("ReadWritePaths = %v, %s fehlt — rclone kann das erneuerte Token nicht speichern", rw, rcloneConfDir)
	}
}

func TestEinrichterGivesConfigDirectoryTo65532(t *testing.T) {
	skript := readFile(t, sharepointDir+"sp-einrichten.py")
	for _, will := range []string{
		"os.chown(os.path.dirname(conf), 65532, 65532)",
		"os.chmod(os.path.dirname(conf), 0o700)",
	} {
		if !strings.Contains(skript, will) {
			t.Errorf("sp-einrichten.py: %q fehlt — der Config-Ordner bliebe root-eigen", will)
		}
	}
}

// Vor dem ersten `resync` als 65532 (noch ohne Unit, also ohne StateDirectory)
// müssen Arbeits- und Config-Verzeichnis mit diesem Eigentümer existieren.
func TestRunbookCreatesAbgleichDirectoriesBeforeFirstResync(t *testing.T) {
	runbook := readFile(t, "../../../box/RUNBOOK.md")
	i := strings.Index(runbook, runbookAnlegen)
	j := strings.Index(runbook, "jnpt-ablage-abgleich.sh resync")
	if i < 0 {
		t.Fatalf("RUNBOOK.md: %q fehlt", runbookAnlegen)
	}
	if j < 0 {
		t.Fatal("RUNBOOK.md: Erstlauf resync fehlt")
	}
	if i > j {
		t.Error("RUNBOOK.md: die Verzeichnisse müssen VOR dem ersten resync angelegt werden")
	}
}
