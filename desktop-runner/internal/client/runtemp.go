package client

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrTemp: Laufverzeichnis nicht sicher anzulegen oder zu entfernen.
var ErrTemp = errors.New("run_temp")

// RunID leitet aus dem Lauf-Schlüssel (job_id oder run_key) eine pfadsichere ID
// ab. run_key enthält Schrägstriche und ist daher nie selbst ein Verzeichnisname.
func RunID(key string) string { return sum([]byte(key)) }

// ValidRunID ist das einzige Format, das je Teil eines Pfades wird:
// genau 64 kleine Hex-Zeichen. Kein Trenner, kein Laufwerk, kein "..".
func ValidRunID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// RunTemps verwaltet je Lauf ein frisches, privates Verzeichnis unter Base
// (außerhalb von Umgebung, Journal und Geheimnissen). Mkdir ist die Naht für
// den OS-Schutz: exklusiv bedeutet "scheitert, wenn der Pfad existiert".
type RunTemps struct {
	Base  string
	Mkdir func(path string, exclusive bool) error
}

func (t RunTemps) mkdir(path string, exclusive bool) error {
	if t.Mkdir != nil {
		return t.Mkdir(path, exclusive)
	}
	if exclusive {
		return os.Mkdir(path, 0700)
	}
	return os.MkdirAll(path, 0700)
}

func isPlainDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().Type() == fs.ModeDir
}

// Create legt das Verzeichnis für id neu an. Ein vorhandenes wird nie übernommen.
func (t RunTemps) Create(id string) (string, error) {
	if !ValidRunID(id) || !filepath.IsAbs(t.Base) {
		return "", ErrTemp
	}
	if err := t.mkdir(t.Base, false); err != nil {
		return "", err
	}
	if !isPlainDir(t.Base) {
		return "", ErrTemp
	}
	dir := filepath.Join(t.Base, id)
	if err := t.mkdir(dir, true); err != nil {
		return "", err
	}
	return dir, nil
}

// Remove löscht genau ein eigenes Laufverzeichnis, nichts anderes.
func (t RunTemps) Remove(dir string) error {
	if filepath.Dir(dir) != filepath.Clean(t.Base) || !ValidRunID(filepath.Base(dir)) {
		return ErrTemp
	}
	return removeTree(dir)
}

// Sweep räumt beim Start alles unter Base ab, was ein früherer Lauf liegen ließ.
func (t RunTemps) Sweep() error {
	if _, err := os.Lstat(t.Base); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if !isPlainDir(t.Base) {
		return ErrTemp
	}
	entries, err := os.ReadDir(t.Base)
	if err != nil {
		return err
	}
	var first error
	for _, e := range entries {
		if err = removeTree(filepath.Join(t.Base, e.Name())); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// removeTree steigt nur in echte Verzeichnisse ab. Symlinks, Junctions und
// andere Reparse-Punkte (Lstat meldet sie nicht als reines Verzeichnis) werden
// als Verweis entfernt, nie betreten. Bewusst kein os.RemoveAll: dessen
// Junction-Verhalten hat sich zwischen Go-Versionen geändert.
func removeTree(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode().Type() == fs.ModeDir {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		var first error
		for _, e := range entries {
			if err = removeTree(filepath.Join(path, e.Name())); err != nil && first == nil {
				first = err
			}
		}
		if first != nil {
			return first
		}
	}
	err = os.Remove(path)
	if err != nil && info.Mode().Type()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
		// Schreibgeschützt: Attribut lösen und einmal wiederholen. Nie bei Verweisen,
		// denn Chmod würde dem Verweis folgen.
		if os.Chmod(path, 0700) == nil {
			err = os.Remove(path)
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Kein Wert aus dem echten Profil oder Läuferzustand erreicht das Kind; TEMP und
// TMP zeigen ausschließlich auf das Laufverzeichnis.
var profileVariables = map[string]bool{
	"LOCALAPPDATA": true, "USERPROFILE": true, "APPDATA": true,
	"HOME": true, "HOMEDRIVE": true, "HOMEPATH": true,
	"TEMP": true, "TMP": true, "TMPDIR": true,
}

func childEnv(base []string, tmp string) []string {
	out := make([]string, 0, len(base)+2)
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if !profileVariables[strings.ToUpper(key)] {
			out = append(out, kv)
		}
	}
	return append(out, "TEMP="+tmp, "TMP="+tmp)
}
