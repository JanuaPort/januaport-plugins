package platform

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
)

// #1392 / SEC W1: Eine `.pth` in einem Wheel bleibt wirkungslos, weil der Läufer `site` nie einschaltet. Der Test
// nimmt das echte Embed-Python aus der Allowlist (.probe, wie TestWindowsPythonEchterStartUndManipulation) und
// ein reines Kanarien-Wheel, dessen `.pth` eine Markerdatei anlegen würde.
func TestWindowsPthImWheelBleibtWirkungslos(t *testing.T) {
	py, e := os.ReadFile("../../.probe/python-3.12.10-embed-amd64.zip")
	if os.IsNotExist(e) {
		t.Skip("kuratierte ZIP in .probe erforderlich")
	}
	if e != nil {
		t.Fatal(e)
	}
	tmp, e := os.MkdirTemp("../../.probe", "pth-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(tmp)
	root, _ := filepath.Abs(tmp)
	marker := filepath.Join(root, "marker-ausgefuehrt")
	pth := "import os; open(r'" + marker + "', 'w').close()\n"

	var wb bytes.Buffer
	zw := zip.NewWriter(&wb)
	for _, f := range [][2]string{{"canary.pth", pth}, {"canary.py", "WERT = 42\n"}} {
		w, _ := zw.Create(f[0])
		w.Write([]byte(f[1]))
	}
	if e = zw.Close(); e != nil {
		t.Fatal(e)
	}
	whl := wb.Bytes()
	h := sha256.Sum256(whl)
	manifest := "name: demo\ndescription: test\nentry: main.py\ninput_schema: {type: object}\ntools: []\ntimeout_s: 10\nrequirements: requirements.lock\npython: python-3.12.10-embed-amd64.zip\n"
	commit, raw := testfixture.Package(map[string][]byte{
		"main.py":                            []byte("pass"),
		"jnpt-desktop.yaml":                  []byte(manifest),
		"python-3.12.10-embed-amd64.zip":     py,
		"requirements.lock":                  []byte("canary==1.0 --hash=sha256:" + hex.EncodeToString(h[:]) + "\n"),
		"wheels/canary-1.0-py3-none-any.whl": whl,
	}, "")
	pin := client.Pin{Pin: "demo", Commit: commit, Path: "demo", Algo: "sha256", DeclaredTools: []string{}, Schedules: []client.Schedule{}}
	env, e := client.Prepare(filepath.Join(root, "envs"), raw, pin)
	if e != nil {
		t.Fatal("reines Wheel nicht angenommen:", e)
	}
	runtimeDir := filepath.Join(root, "envs", env.Dir, "runtime")
	pthName := filepath.Join(runtimeDir, "python312._pth")
	original, e := os.ReadFile(pthName)
	if e != nil {
		t.Fatal(e)
	}
	run := func() string {
		t.Helper()
		cmd := exec.Command(filepath.Join(runtimeDir, "python.exe"), "-I", "-B", "-c", "import canary, sys; print(canary.WERT, 'site' in sys.modules)")
		cmd.Dir = runtimeDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Python: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}

	// Dieselbe `.pth` liegt zusätzlich neben python.exe: ein Ort, den `site` lesen würde, wäre es an.
	if e = os.WriteFile(filepath.Join(runtimeDir, "canary.pth"), []byte(pth), 0o600); e != nil {
		t.Fatal(e)
	}
	// Das Wheel ist benutzbar (positive Kontrolle), site ist aus, der Marker entsteht nicht.
	if got := run(); got != "42 False" {
		t.Fatalf("Wheel nicht importierbar oder site geladen: %q", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal(".pth aus dem Wheel wurde ausgeführt")
	}

	// Kanarienvogel scharf: mit `import site` in der `._pth` führt dieselbe Anordnung die `.pth` aus. Ohne diese
	// Gegenprobe könnte der Test auch grün sein, weil die `.pth` an einem tauben Ort liegt.
	if e = os.WriteFile(pthName, append(append([]byte{}, original...), []byte("import site\n")...), 0o600); e != nil {
		t.Fatal(e)
	}
	if got := run(); got != "42 True" {
		t.Fatalf("Gegenprobe: %q", got)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("Kanarienvogel stumm: auch mit import site entsteht kein Marker, der Test belegt nichts")
	}
}
