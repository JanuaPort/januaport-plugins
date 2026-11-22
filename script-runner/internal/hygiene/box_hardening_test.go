package hygiene

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// schreibe_datei steht in `if` und läuft dort ohne `set -e`. Ein stiller
// Schreibfehler hieße „geändert", obwohl nichts auf der Platte steht
// (JanuaPort/januaport#1539). Der Test zieht fehler() und schreibe_datei() aus
// dem echten Skript und fährt sie ohne root.

const hardeningSkript = hardeningPath

// skriptZeilen liefert die Zeilen von der ersten, die mit anfang beginnt, bis
// einschließlich der nächsten `}` am Zeilenanfang (oder nur die eine Zeile).
func skriptZeilen(t *testing.T, quelle, anfang string) string {
	t.Helper()
	var aus []string
	drin := false
	for _, z := range strings.Split(quelle, "\n") {
		if !drin && strings.HasPrefix(z, anfang) {
			drin = true
		}
		if drin {
			aus = append(aus, z)
			if z == "}" || (len(aus) == 1 && strings.HasSuffix(z, "}")) {
				return strings.Join(aus, "\n")
			}
		}
	}
	t.Fatalf("%s nicht in %s gefunden", anfang, hardeningSkript)
	return ""
}

// fahre führt fehler() und schreibe_datei() mit der Vorbereitung vor aus und
// liefert Exit-Code, stdout und stderr.
func fahre(t *testing.T, vorbereitung string, env ...string) (int, string, string) {
	t.Helper()
	quelle := readFile(t, hardeningSkript)
	skript := "set -euo pipefail\n" +
		skriptZeilen(t, quelle, "fehler()") + "\n" +
		skriptZeilen(t, quelle, "schreibe_datei()") + "\n" +
		vorbereitung + "\n" +
		"if schreibe_datei \"$ZIEL\" 0644 <<<'inhalt'; then echo GEAENDERT; fi\n" +
		"echo WEITER\n"
	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(skript)
	cmd.Env = append(os.Environ(), env...)
	var aus, err2 bytes.Buffer
	cmd.Stdout, cmd.Stderr = &aus, &err2
	code := 0
	if runErr := cmd.Run(); runErr != nil {
		ee, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Skipf("bash nicht startbar: %v", runErr)
		}
		code = ee.ExitCode()
	}
	return code, aus.String(), err2.String()
}

func erwarteAbbruch(t *testing.T, code int, aus, fehl string) {
	t.Helper()
	if code == 0 {
		t.Errorf("Exit-Code 0, erwartet ungleich 0\nstdout: %s\nstderr: %s", aus, fehl)
	}
	if strings.Contains(aus, "GEAENDERT") || strings.Contains(aus, "WEITER") {
		t.Errorf("Skript lief nach dem Schreibfehler weiter: %q", aus)
	}
	if !strings.Contains(fehl, "FEHLER") {
		t.Errorf("stderr ohne FEHLER-Meldung: %q", fehl)
	}
}

// Ziel unter einer gewöhnlichen Datei: install -D scheitert auch als root.
func TestSchreibeDateiBrichtBeiInstallFehlerAb(t *testing.T) {
	dir := t.TempDir()
	datei := filepath.Join(dir, "datei")
	if err := os.WriteFile(datei, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, aus, fehl := fahre(t, "", "ZIEL="+filepath.Join(datei, "unterordner", "ziel"))
	erwarteAbbruch(t, code, aus, fehl)
}

// Temp-Datei auf /dev/full: cat scheitert mit „No space left". install ist
// ersetzt (legt nur das leere Ziel an, liest nie vom Gerät), damit der Test
// auch ohne root nur den cat-Fehler sieht.
func TestSchreibeDateiBrichtBeiCatFehlerAb(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full fehlt")
	}
	dir := t.TempDir()
	voll := filepath.Join(dir, "voll")
	if err := os.Symlink("/dev/full", voll); err != nil {
		t.Skipf("Symlink nicht möglich: %v", err)
	}
	ziel := filepath.Join(dir, "ziel")
	vorbereitung := `mktemp() { printf '%s\n' "$VOLL"; }
install() { : >"${!#}"; }`
	code, aus, fehl := fahre(t, vorbereitung, "ZIEL="+ziel, "VOLL="+voll)
	erwarteAbbruch(t, code, aus, fehl)
	if _, err := os.Stat(ziel); err == nil {
		t.Error("Ziel wurde trotz Schreibfehler angelegt")
	}
}

func TestSchreibeDateiSchreibtWeiterhinBeiErfolg(t *testing.T) {
	dir := t.TempDir()
	ziel := filepath.Join(dir, "ziel")
	vorbereitung := `install() { local a=("$@"); cp "${a[${#a[@]}-2]}" "${a[${#a[@]}-1]}"; }`
	code, aus, fehl := fahre(t, vorbereitung, "ZIEL="+ziel)
	if code != 0 || !strings.Contains(aus, "GEAENDERT") {
		t.Fatalf("Erfolgsfall: Exit %d, stdout %q, stderr %q", code, aus, fehl)
	}
	got, err := os.ReadFile(ziel) //nolint:gosec // Pfad liegt im t.TempDir() des Tests.
	if err != nil || string(got) != "inhalt\n" {
		t.Errorf("Ziel = %q, %v", got, err)
	}
}
