package hygiene

import (
	"strings"
	"testing"
)

// SMB-Zugangsdaten der Box (JanuaPort/januaport#1429): Sicherung und Ablage
// nutzen getrennte Dateien. Ein Ablage-Konto, das auch die Vollsicherungen
// lesen darf (ein Sicherungsartefakt ist die vollständige Installation), hebelt
// die Trennung zwischen Host-Sicherung und KI-Ablage aus.

func mountOptionen(t *testing.T, datei string) string {
	t.Helper()
	for _, z := range strings.Split(readFile(t, "../../../box/smb/"+datei), "\n") {
		if strings.HasPrefix(z, "Options=") {
			return z
		}
	}
	t.Fatalf("%s: keine Options-Zeile", datei)
	return ""
}

func TestSMBMountsUseSeparateCredentials(t *testing.T) {
	for _, tc := range []struct{ datei, erwartet, verboten string }{
		{"srv-jnpt-sicherung.mount", "credentials=/etc/jnpt/smb/sicherung.cred", "ablage.cred"},
		{"srv-jnpt-ablage-beispiel.mount.example", "credentials=/etc/jnpt/smb/ablage.cred", "sicherung.cred"},
		{"srv-jnpt-ablage-ki.mount.example", "credentials=/etc/jnpt/smb/ablage.cred", "sicherung.cred"},
	} {
		t.Run(tc.datei, func(t *testing.T) {
			o := mountOptionen(t, tc.datei)
			if !strings.Contains(o, tc.erwartet) {
				t.Errorf("Options = %q, erwartet %q", o, tc.erwartet)
			}
			if strings.Contains(o, tc.verboten) {
				t.Errorf("Options = %q darf %q nicht nennen", o, tc.verboten)
			}
		})
	}
}

func TestRunbookCreatesBothSMBCredentialFiles(t *testing.T) {
	runbook := readFile(t, "../../../box/RUNBOOK.md")
	for _, will := range []string{
		"cat > /etc/jnpt/smb/sicherung.cred",
		"chmod 600 /etc/jnpt/smb/sicherung.cred",
		"cat > /etc/jnpt/smb/ablage.cred",
		"chmod 600 /etc/jnpt/smb/ablage.cred",
	} {
		if !strings.Contains(runbook, will) {
			t.Errorf("RUNBOOK.md: %q fehlt", will)
		}
	}
}
