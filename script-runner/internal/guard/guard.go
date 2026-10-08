// Package guard ist der Wächter: PID 1 der Sandbox. Er meldet sich beim
// vermittler (hello), nimmt GENAU EINEN job an, startet `python -I` und
// räumt danach den ganzen Prozessbaum ab, bevor er die Verbindung schließt
// (Vertrag §5, §7, Fassung 2 V1/V3). Danach endet der Container; Dockers
// Restart-Regel liefert eine neue Instanz mit leerem tmpfs.
package guard

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

// MinLifetime hält jede Instanz mindestens so lange ab ihrem Start am
// Leben. Läuft ein Container ≥ 10 s, setzt dockerd den Restart-Backoff
// zurück (Messung B1).
const MinLifetime = 10500 * time.Millisecond

// MaxJobFrame ist die Grenze des Wächters für den job-Rahmen und für
// Rahmen des Bootstraps. Die Grenzen des Vertrags setzt der vermittler durch.
const MaxJobFrame = 8 << 20

// Config sind die festen Orte in der Sandbox.
type Config struct {
	SocketPath string
	WorkDir    string
	Python     string
	Bootstrap  string
}

// DefaultConfig sind die Orte im ausgelieferten Image.
func DefaultConfig() Config {
	return Config{
		SocketPath: "/run/script-runner/sandbox.sock",
		WorkDir:    "/work",
		Python:     "/usr/local/bin/python3",
		Bootstrap:  "/usr/local/lib/script-runner/bootstrap.py",
	}
}

// parseSeccomp liest das Feld `Seccomp:` aus /proc/self/status; -1, wenn es
// fehlt oder unlesbar ist.
func parseSeccomp(status string) int {
	for _, line := range strings.Split(status, "\n") {
		rest, ok := strings.CutPrefix(line, "Seccomp:")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return -1
		}
		return n
	}
	return -1
}

// isolation leitet die Isolation aus `uname -r` ab (Vertrag §5). gVisor
// meldet eine eigene Kennung, gemessen: „4.19.0-gvisor“.
func isolation(release string) string {
	if strings.Contains(strings.ToLower(release), "gvisor") {
		return contract.IsolationGVisor
	}
	return contract.IsolationStandard
}

func newInstanceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// writeFiles legt die Dateien des Pins unter root ab. Nur relative Pfade
// ohne Ausbruch; Dateien 0600, Ordner 0700 — nie ausführbar.
func writeFiles(root string, files []frame.File) error {
	for _, f := range files {
		if !gitref.ValidPinPath(f.Path) {
			return errors.New("guard: unzulässiger Dateipfad im job")
		}
		data, err := base64.StdEncoding.DecodeString(f.DataB64)
		if err != nil {
			return errors.New("guard: Datei nicht lesbar")
		}
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(full, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// childEnv ist die ganze Umgebung des Skripts. Nichts vom Container, nichts
// vom Wächter.
func childEnv() []string {
	return []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=/work",
		"TMPDIR=/tmp",
		"LANG=C.UTF-8",
	}
}

// others sagt, ob in procDir außer PID 1 und dem eigenen Prozess noch ein
// Prozess steht.
func others(procDir string) bool {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return true
	}
	self := strconv.Itoa(os.Getpid())
	for _, e := range entries {
		name := e.Name()
		if _, err := strconv.Atoi(name); err != nil || name == "1" || name == self {
			continue
		}
		return true
	}
	return false
}

// finishOps sind die Schritte nach dem Lauf; getrennt, damit die
// Reihenfolge ohne echte Prozesse prüfbar ist.
type finishOps interface {
	SendExited()
	KillAll()
	ReapAll()
	OthersAlive() bool
	Close()
	SleepUntil(time.Time)
}

// finish hält die Reihenfolge aus Fassung 2 V1 ein: exited → kill(-1) →
// waitpid → Restprozesse prüfen → close → warten bis MinLifetime. Lebt nach
// kill(-1) noch etwas, endet der Wächter sofort (V3); der Container reißt
// dann alles mit. true heißt: sofort beenden.
func finish(o finishOps, start time.Time, min time.Duration) bool {
	o.SendExited()
	o.KillAll()
	o.ReapAll()
	if o.OthersAlive() {
		return true
	}
	o.Close()
	o.SleepUntil(start.Add(min))
	return false
}
