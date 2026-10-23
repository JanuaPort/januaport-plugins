// Kopie aus script-runner/internal/libs/lock.go ab Grenzen des Lock-Formats. Wächter: TestCopiedLibraryFunctions.
package client

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

func invalid(string) error  { return ErrHash }
func tooLarge(string) error { return ErrHash }

// Grenzen des Lock-Formats. Ein Lock ist Handarbeit oder Werkzeugausgabe, kein
// Datenspeicher.
const (
	maxLockFileBytes = 1 << 20
	maxLockEntries   = 256
	maxLockLineBytes = 4096
	maxEntryHashes   = 64
)

// Entry ist eine Zeile des Locks: Name (PEP 503 normalisiert), Version und die
// SHA-256-Hashes der erlaubten Wheel-Dateien.
type Entry struct {
	Name    string
	Version string
	Hashes  []string
}

// Lock ist ein geprüftes Lock. Hash ist die SHA-256 der Datei-Bytes; sie ist
// der Name des Verzeichnisses unter libs/.
type Lock struct {
	Hash    string
	Entries []Entry
}

var (
	nameRe          = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	versionRe       = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._+!]*[A-Za-z0-9])?$`)
	lockHashTokenRe = regexp.MustCompile(`^--hash=sha256:([0-9a-f]{64})$`)
	dashRunRe       = regexp.MustCompile(`[-_.]+`)
)

// normalizeName ist die Normalisierung nach PEP 503.
func normalizeName(s string) string {
	return dashRunRe.ReplaceAllString(strings.ToLower(s), "-")
}

// ParseLock liest ein Lock im pip-Format streng: Kommentare und Leerzeilen,
// Zeilenfortsetzung mit Rückstrich, je Eintrag `name==version` und mindestens
// ein `--hash=sha256:<64 Hex>`. Alles andere — Optionen (--index-url, -r, -e),
// URLs, Extras, Marker, Versionsbereiche, andere Hash-Verfahren, doppelte
// Namen — ist libs_invalid.
func ParseLock(data []byte) (Lock, error) {
	if len(data) > maxLockFileBytes {
		return Lock{}, invalid("Lock über 1 MiB")
	}
	for _, b := range data {
		if b != '\t' && b != '\n' && b != '\r' && (b < 0x20 || b > 0x7e) {
			return Lock{}, invalid("Lock enthält Zeichen außerhalb von ASCII")
		}
	}
	var entries []Entry
	seen := map[string]bool{}
	var cur []string
	cont := false
	flush := func() error {
		line := strings.TrimSpace(strings.Join(cur, " "))
		cur = nil
		if line == "" {
			return nil
		}
		e, err := parseEntry(line)
		if err != nil {
			return err
		}
		if seen[e.Name] {
			return invalid("Name doppelt im Lock")
		}
		if len(entries) >= maxLockEntries {
			return invalid("mehr als 256 Einträge im Lock")
		}
		seen[e.Name] = true
		entries = append(entries, e)
		return nil
	}
	for _, raw := range strings.Split(string(data), "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		if len(raw) > maxLockLineBytes {
			return Lock{}, invalid("Zeile über 4096 Zeichen")
		}
		if strings.ContainsRune(raw, '\r') {
			return Lock{}, invalid("Wagenrücklauf mitten in der Zeile")
		}
		line := strings.TrimSpace(stripComment(raw))
		cont = strings.HasSuffix(line, "\\")
		if cont {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\\"))
		}
		if line != "" {
			cur = append(cur, line)
		}
		if !cont {
			if err := flush(); err != nil {
				return Lock{}, err
			}
		}
	}
	if cont {
		return Lock{}, invalid("Zeilenfortsetzung am Dateiende")
	}
	if err := flush(); err != nil {
		return Lock{}, err
	}
	if len(entries) == 0 {
		return Lock{}, invalid("Lock ohne Einträge")
	}
	sum := sha256.Sum256(data)
	return Lock{Hash: hex.EncodeToString(sum[:]), Entries: entries}, nil
}

// stripComment entfernt einen Kommentar: `#` am Zeilenanfang oder nach
// Leerraum (wie pip).
func stripComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

func parseEntry(line string) (Entry, error) {
	tokens := strings.Fields(line)
	name, version, ok := strings.Cut(tokens[0], "==")
	if !ok || !nameRe.MatchString(name) || !versionRe.MatchString(version) {
		return Entry{}, invalid("Eintrag ist nicht name==version")
	}
	e := Entry{Name: normalizeName(name), Version: version}
	dup := map[string]bool{}
	for _, tok := range tokens[1:] {
		m := lockHashTokenRe.FindStringSubmatch(tok)
		if m == nil {
			return Entry{}, invalid("unzulässiges Token im Lock-Eintrag")
		}
		if dup[m[1]] {
			return Entry{}, invalid("Hash doppelt im Eintrag")
		}
		dup[m[1]] = true
		e.Hashes = append(e.Hashes, m[1])
	}
	if len(e.Hashes) == 0 {
		return Entry{}, invalid("Eintrag ohne sha256-Hash")
	}
	if len(e.Hashes) > maxEntryHashes {
		return Entry{}, invalid("zu viele Hashes im Eintrag")
	}
	return e, nil
}
