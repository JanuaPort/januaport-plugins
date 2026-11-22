package hygiene

import (
	"regexp"
	"strings"
	"testing"
)

// Das Runbook wird am Terminal beim Kunden abgetippt. Geheimnisse dürfen dort
// weder in den Shell-Verlauf noch in eine weltlesbare Datei geraten
// (JanuaPort/januaport#1539). Die Wächter prüfen die Befehlsblöcke des Runbooks.

const runbookPfad = "../../../box/RUNBOOK.md"

func runbookZeilen(t *testing.T) []string {
	t.Helper()
	return strings.Split(readFile(t, runbookPfad), "\n")
}

func TestRunbookGeheimnisNieAlsKlartextInPrintf(t *testing.T) {
	klartext := regexp.MustCompile(`^printf .*'<`)
	for i, z := range runbookZeilen(t) {
		if klartext.MatchString(z) {
			t.Errorf("%s:%d: Geheimnis als Klartext in printf (Shell-Verlauf): %s", runbookPfad, i+1, z)
		}
	}
}

func TestRunbookGeheimnisDateiNurMitUmask(t *testing.T) {
	geheimnisDatei := regexp.MustCompile(`> /tmp/(mk|ak)(\s|$)`)
	for i, z := range runbookZeilen(t) {
		if geheimnisDatei.MatchString(z) && !strings.Contains(z, "umask 077") {
			t.Errorf("%s:%d: Geheimnis-Datei in /tmp ohne umask 077 (weltlesbar): %s", runbookPfad, i+1, z)
		}
	}
}

func TestRunbookEingelesenesGeheimnisWirdGeloescht(t *testing.T) {
	lesen := regexp.MustCompile(`^read -rs (\w+)`)
	zeilen := runbookZeilen(t)
	for i, z := range zeilen {
		m := lesen.FindStringSubmatch(z)
		if m == nil {
			continue
		}
		rest := strings.Join(zeilen[i:min(i+8, len(zeilen))], "\n")
		if !strings.Contains(rest, "unset "+m[1]) {
			t.Errorf("%s:%d: read -rs %s ohne unset %s in den folgenden Zeilen", runbookPfad, i+1, m[1], m[1])
		}
	}
}

func TestRunbookTailscaleUpOhneSchluesselLiteralMitAcceptDnsAus(t *testing.T) {
	var gefunden int
	for i, z := range runbookZeilen(t) {
		if !strings.HasPrefix(z, "tailscale up ") {
			continue
		}
		gefunden++
		if !strings.Contains(z, "--accept-dns=false") {
			t.Errorf("%s:%d: tailscale up ohne --accept-dns=false (Pflicht, der Host-Resolver bleibt beim DHCP): %s", runbookPfad, i+1, z)
		}
		if strings.Contains(z, "--authkey ") && !strings.Contains(z, `--authkey "$`) {
			t.Errorf("%s:%d: --authkey als Literal statt aus read -rs: %s", runbookPfad, i+1, z)
		}
	}
	if gefunden == 0 {
		t.Fatal("kein tailscale up im Runbook gefunden")
	}
}

func TestRunbookEbicsDropInLaedtBeideComposeDateien(t *testing.T) {
	// Ohne Einrückung verglichen: das Drop-in steht je nach Runbook in einer Liste.
	var ohneEinrueckung []string
	for _, z := range runbookZeilen(t) {
		ohneEinrueckung = append(ohneEinrueckung, strings.TrimSpace(z))
	}
	soll := "[Service]\nExecStart=\nExecStart=/usr/bin/docker compose -f /opt/jnpt/ebics-abholer/docker-compose.ebics-abholer.yml -f /opt/jnpt/ebics-abholer/docker-compose.box-netz.yml run --rm -T --no-deps ebics-abholer"
	if !strings.Contains(strings.Join(ohneEinrueckung, "\n"), soll) {
		t.Errorf("%s: EBICS-Drop-in fehlt: %q", runbookPfad, soll)
	}
}
