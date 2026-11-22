// Package sandbox hat keinen Go-Code: Das Verzeichnis enthält bootstrap.py (das
// Python-SDK) und seine Tests. Dieser Test startet die Python-Tests, wo ein
// Interpreter vorhanden ist, und überspringt sie ehrlich sonst.
package sandbox

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func findPython() (string, []string) {
	for _, c := range []struct {
		name string
		args []string
	}{{"python3", nil}, {"python", nil}, {"py", []string{"-3"}}} {
		p, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		// Der Microsoft-Store-Platzhalter „python“ besteht den Aufruf nicht.
		args := append(append([]string{}, c.args...), "-c", "import sys; print(sys.version_info[0])")
		if out, err := exec.Command(p, args...).Output(); err == nil && len(out) > 0 && out[0] == '3' {
			return p, c.args
		}
	}
	return "", nil
}

func TestPythonSDKUnits(t *testing.T) {
	py, pre := findPython()
	if py == "" {
		t.Skip("kein Python 3 gefunden: SDK-Tests nicht ausgeführt")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, append(append([]string{}, pre...), "-I", "-B", "test_etl_sdk.py")...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python-SDK-Tests fehlgeschlagen: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
