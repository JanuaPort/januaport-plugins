//go:build docker

// Docker-Sonden für die Python-Bibliotheken des Skript-Läufers
// (JanuaPort/januaport#1041, P1). Sie laufen im selben Stack wie die übrigen
// Sonden (probe/run.sh): numpy_ok, libs_readonly, libs_tampered, no_pip; die
// Sonde net_probe bleibt unverändert.
//
// Die Wheels für numpy_ok werden zur Laufzeit in einem Hilfscontainer mit pip
// geladen (nicht im Repo, nicht im Produkt) — oder aus P2_WHEELS_DIR kopiert.
package probe

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/januaport/januaport-plugins/script-runner/internal/libs"
)

// tamperedLockHash ist der Lock-Hash der Sonde libs_tampered; setup() setzt
// ihn beim Anlegen des Repositories.
var tamperedLockHash string

const probeWheelName = "probe_pkg-1.0-py3-none-any.whl"

// tamperWheelName ist das Wheel der Sonde libs_tampered: ein eigenes Lock, damit
// kein anderer Pin sein Verzeichnis anlegt.
const tamperWheelName = "tamper_pkg-1.0-py3-none-any.whl"

// probeWheel baut ein kleines Wheel mit einem Paket, einer .pth-Datei, die
// beim Verarbeiten durch site eine Umgebungsvariable setzte, und einem
// Entry-Point.
func probeWheel() []byte { return probeWheelFor("probe_pkg") }

func probeWheelFor(dist string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct{ name, data string }{
		{dist + "/__init__.py", "VALUE = 'ok'\n"},
		{"evil.pth", "import os; os.environ['PTH_RAN'] = '1'\n"},
		{dist + "-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: " + dist + "\nVersion: 1.0\n"},
		{dist + "-1.0.dist-info/WHEEL", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
		{dist + "-1.0.dist-info/entry_points.txt", "[console_scripts]\nprobe = " + dist + ":main\n"},
	} {
		w, _ := zw.Create(f.name)
		_, _ = w.Write([]byte(f.data))
	}
	_ = zw.Close()
	return buf.Bytes()
}

// lockFor schreibt ein Lock im pip-Format für die Wheels in dir.
func lockFor(dir string) (string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.whl"))
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	var b strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		parts := strings.Split(filepath.Base(f), "-")
		h := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s==%s \\\n    --hash=sha256:%s\n", parts[0], parts[1], hex.EncodeToString(h[:]))
	}
	return b.String(), nil
}

func writePinLibs(repo, pin string, wheels map[string][]byte, lockFrom map[string][]byte) (string, error) {
	dir := filepath.Join(repo, pin, "wheels")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Das Lock entsteht aus den Wheels in lockFrom (bei libs_tampered die
	// unveränderten), die Datei im Repository aus wheels.
	lockDir, err := os.MkdirTemp("", "p2-1041-lock")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(lockDir)
	for name, data := range lockFrom {
		if err := os.WriteFile(filepath.Join(lockDir, name), data, 0o644); err != nil {
			return "", err
		}
	}
	for name, data := range wheels {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return "", err
		}
	}
	lock, err := lockFor(lockDir)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(repo, pin, "requirements.lock"), []byte(lock), 0o644); err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(lock))
	return hex.EncodeToString(h[:]), nil
}

// addLibsFixtures legt die Wheels und Locks der Bibliotheks-Sonden an.
func addLibsFixtures(repo string) error {
	good := map[string][]byte{probeWheelName: probeWheel()}
	for _, pin := range []string{"libs_readonly", "no_pip"} {
		if _, err := writePinLibs(repo, pin, good, good); err != nil {
			return err
		}
	}
	// libs_tampered: eigenes Lock, Wheel weicht vom Hash im Lock ab.
	tGood := map[string][]byte{tamperWheelName: probeWheelFor("tamper_pkg")}
	tBad := probeWheelFor("tamper_pkg")
	tBad[len(tBad)/2] ^= 0xff
	h, err := writePinLibs(repo, "libs_tampered", map[string][]byte{tamperWheelName: tBad}, tGood)
	if err != nil {
		return err
	}
	tamperedLockHash = h
	// libs_tampered_shared: dasselbe Lock wie libs_readonly und no_pip (deren
	// Verzeichnis steht), aber ein manipuliertes Wheel.
	bad := probeWheel()
	bad[len(bad)/2] ^= 0xff
	if _, err := writePinLibs(repo, "libs_tampered_shared", map[string][]byte{probeWheelName: bad}, good); err != nil {
		return err
	}

	dl, err := os.MkdirTemp("", "p2-1041-wheels")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dl)
	if err := downloadWheels(dl); err != nil {
		return err
	}
	entries, err := os.ReadDir(dl)
	if err != nil {
		return err
	}
	all := map[string][]byte{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".whl") {
			b, err := os.ReadFile(filepath.Join(dl, e.Name()))
			if err != nil {
				return err
			}
			all[e.Name()] = b
		}
	}
	if len(all) == 0 {
		return fmt.Errorf("keine Wheels geladen")
	}
	_, err = writePinLibs(repo, "numpy_ok", all, all)
	return err
}

// downloadWheels lädt numpy und pandas (mit Abhängigkeiten) als Wheels für
// Python 3.12/manylinux x86_64. Nur in der Sonde, nie im Produkt.
func downloadWheels(dest string) error {
	if d := os.Getenv("P2_WHEELS_DIR"); d != "" {
		entries, err := os.ReadDir(d)
		if err != nil {
			return err
		}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(d, e.Name()))
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dest, e.Name()), b, 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	name := nextContainerName()
	defer func() { _, _ = run("docker", "rm", "-f", name) }()
	if out, err := run("docker", "run", "--name", name, "python:3.12-slim", "pip", "download", "-q", "--only-binary=:all:",
		"--implementation", "cp", "--python-version", "3.12",
		"--platform", "manylinux2014_x86_64", "--platform", "manylinux_2_17_x86_64", "--platform", "manylinux_2_27_x86_64", "--platform", "manylinux_2_28_x86_64",
		"-d", "/w", "numpy==2.5.4", "pandas==3.0.6"); err != nil {
		return fmt.Errorf("pip download: %v %s", err, out)
	}
	if out, err := run("docker", "cp", name+":/w/.", dest); err != nil {
		return fmt.Errorf("docker cp: %v %s", err, out)
	}
	return nil
}

// Invariante 4: numpy und pandas aus dem Nur-Lese-Volume, ohne an pids_limit
// oder 512 MiB zu stoßen; die Thread-Deckel greifen.
func TestLibsNumpyPandasUnderLimits(t *testing.T) {
	out := okJSON(t, "numpy_ok", nil)
	if out["groups"] != float64(50) || out["rows_total"] != float64(10000) {
		t.Errorf("Arbeitslast: %v", out)
	}
	if out["threads"] != float64(1) {
		t.Errorf("Threads = %v, want 1 (OPENBLAS_NUM_THREADS=1, OMP_NUM_THREADS=1 aus dem Wächter)", out["threads"])
	}
	if rss, _ := out["max_rss_mib"].(float64); rss <= 0 || rss > 400 {
		t.Errorf("max_rss_mib = %v", out["max_rss_mib"])
	}
	if p, _ := out["libs_on_path"].([]any); len(p) != 1 {
		t.Errorf("libs im Suchpfad: %v", out["libs_on_path"])
	}
	t.Logf("numpy %v, pandas %v, RSS %v MiB, Threads %v", out["numpy"], out["pandas"], out["max_rss_mib"], out["threads"])
}

// Invariante 4: /libs ist nur lesbar (EROFS); Invariante 3: eine .pth-Datei
// im Wheel wird nie ausgeführt.
func TestLibsReadOnlyEROFS(t *testing.T) {
	out := okJSON(t, "libs_readonly", nil)
	for _, op := range []string{"create", "overwrite", "mkdir", "unlink", "rename", "chmod", "symlink"} {
		if out[op] != "EROFS" {
			t.Errorf("%s = %v, want EROFS", op, out[op])
		}
	}
	// Das Öffnen einer vorhandenen 0444-Datei zum Anhängen oder Kürzen scheitert
	// je nach Kernel schon an den Rechten (EACCES), bevor das Dateisystem EROFS
	// meldet (CI-Runner: truncate = EACCES); beides heißt „nicht beschreibbar“.
	// Dass der Mount selbst read-only ist, belegen die anderen Zeilen.
	for _, op := range []string{"append", "truncate"} {
		if out[op] != "EROFS" && out[op] != "EACCES" {
			t.Errorf("%s = %v, want EROFS oder EACCES", op, out[op])
		}
	}
	if out["import"] != "ok" || out["intact"] != true {
		t.Errorf("import/intact: %v / %v", out["import"], out["intact"])
	}
	if out["pth_ran"] != false {
		t.Error(".pth-Datei aus dem Wheel wurde ausgeführt")
	}
	if sp, _ := out["site_in_path"].([]any); len(sp) != 0 {
		t.Errorf("site-packages im Suchpfad: %v", sp)
	}
}

// Invariante 2: ein Wheel, das nicht zum Lock passt, ergibt invalid mit
// libs_hash_mismatch; kein Werkzeug, kein libs/<hash>, kein Rest. libs_tampered
// hat ein eigenes Lock; libs_tampered_shared teilt es mit libs_readonly und
// no_pip, deren Verzeichnis schon steht — auch dort wird das Wheel geprüft, und
// die guten Pins bleiben ready.
func TestLibsTamperedWheelPinInvalid(t *testing.T) {
	// Das Entpacken läuft asynchron; die Pins sind zuerst pending.
	type pinRow struct{ Name, State, Reason string }
	var res *mcp.ListToolsResult
	var meta struct {
		Pins []pinRow `json:"pins"`
	}
	var b []byte
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(500 * time.Millisecond) {
		var err error
		if res, err = listTools(project); err != nil {
			t.Fatal(err)
		}
		b, _ = json.Marshal(res.Meta["januaport.ai/runner"])
		meta.Pins = nil
		if err := json.Unmarshal(b, &meta); err != nil {
			t.Fatal(err)
		}
		pending := false
		for _, p := range meta.Pins {
			pending = pending || (strings.HasPrefix(p.Name, "libs_tampered") && p.State == "pending")
		}
		if !pending || time.Now().After(deadline) {
			break
		}
	}
	state := map[string]pinRow{}
	for _, p := range meta.Pins {
		state[p.Name] = p
	}
	for _, name := range invalidPinNames {
		p, ok := state[name]
		if !ok {
			t.Fatalf("%s nicht in tools/list: %s", name, b)
		}
		if p.State != "invalid" || p.Reason != "libs_hash_mismatch" {
			t.Errorf("%s = %s/%s, want invalid/libs_hash_mismatch", name, p.State, p.Reason)
		}
		for _, tool := range res.Tools {
			if tool.Name == name {
				t.Errorf("Werkzeug für den ungültigen Pin %s", name)
			}
		}
		_, m, err := tryCall(project, name, nil)
		if err != nil {
			t.Fatal(err)
		}
		if m["end"] != "refused" || m["detail"] != "state" {
			t.Errorf("Aufruf %s: %v/%v", name, m["end"], m["detail"])
		}
	}
	for _, name := range []string{"libs_readonly", "no_pip"} {
		if state[name].State != "ready" {
			t.Errorf("%s = %s, want ready (gleiches Lock wie libs_tampered_shared)", name, state[name].State)
		}
	}
	listing := must(t, "docker", "exec", project+"-script-runner-1", "ls", "-A", "/libs")
	for _, n := range strings.Fields(listing) {
		if n == tamperedLockHash || strings.Contains(n, ".tmp") || strings.Contains(n, ".wheel") {
			t.Errorf("libs enthält %s", n)
		}
	}
	t.Logf("libs im vermittler: %s", strings.Join(strings.Fields(listing), " "))
}

// Invariante 1: kein pip in der Sandbox, auch nicht mit Bibliotheken.
func TestLibsNoPip(t *testing.T) {
	out := okJSON(t, "no_pip", nil)
	if out["import_pip"] != "ModuleNotFoundError" {
		t.Errorf("import pip = %v", out["import_pip"])
	}
	if out["spec_pip"] != false || out["spec_ensurepip"] != false {
		t.Errorf("pip/ensurepip auffindbar: %v", out)
	}
	for _, w := range out["which"].([]any) {
		if w != nil {
			t.Errorf("pip-Programm gefunden: %v", w)
		}
	}
	if bins, _ := out["bins"].([]any); len(bins) != 0 {
		t.Errorf("pip-Dateien in den Programmordnern: %v", bins)
	}
	if out["libs_on_path"] != float64(1) {
		t.Errorf("libs im Suchpfad: %v", out["libs_on_path"])
	}
}

// Invariante 4: die Sandbox hängt genau einen Mount mehr als vorher ein,
// /libs nur lesend; der vermittler schreibt ihn; /work und /tmp bleiben
// noexec.
func TestLibsExactlyOneExtraMountReadOnly(t *testing.T) {
	type mount struct {
		Type, Name, Destination string
		RW                      bool
	}
	inspect := func(svc string) []mount {
		out := must(t, "docker", "inspect", "-f", "{{json .Mounts}}", project+"-"+svc+"-1")
		var ms []mount
		if err := json.Unmarshal([]byte(out), &ms); err != nil {
			t.Fatal(err)
		}
		return ms
	}
	sb := inspect("script-runner-sandbox")
	var dests []string
	for _, m := range sb {
		dests = append(dests, m.Destination)
		if m.RW {
			t.Errorf("Sandbox-Mount %s beschreibbar", m.Destination)
		}
		if m.Destination == "/libs" && (m.Type != "volume" || !strings.HasSuffix(m.Name, "_libs")) {
			t.Errorf("/libs ist %s %s, want Volume libs", m.Type, m.Name)
		}
	}
	sort.Strings(dests)
	if strings.Join(dests, ",") != "/libs,/run/script-runner" {
		t.Errorf("Mounts der Sandbox = %v, want genau Socket-Ordner und /libs", dests)
	}
	for _, m := range inspect("script-runner") {
		if m.Destination == "/libs" && !m.RW {
			t.Error("/libs ist im vermittler nicht beschreibbar")
		}
	}
	for _, m := range inspect("script-runner-fetcher") {
		if m.Destination == "/libs" {
			t.Error("/libs im abholer")
		}
	}
	tmpfs := must(t, "docker", "inspect", "-f", "{{json .HostConfig.Tmpfs}}", project+"-script-runner-sandbox-1")
	var tm map[string]string
	if err := json.Unmarshal([]byte(tmpfs), &tm); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/work", "/tmp"} {
		if !strings.Contains(tm[p], "noexec") {
			t.Errorf("%s ohne noexec: %q", p, tm[p])
		}
	}
}

var glibcRe = regexp.MustCompile(`(\d+)\.(\d+)\s*$`)

// P0: die glibc-Obergrenze der Plattform-Tags ist die glibc des Images.
func TestLibsGlibcCeilingMatchesImage(t *testing.T) {
	out := must(t, "docker", "run", "--rm", "--entrypoint", "sh", image, "-c", "ldd --version | head -1")
	m := glibcRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		t.Fatalf("glibc-Fassung nicht lesbar: %q", out)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major != 2 || minor != libs.MaxGlibcMinor {
		t.Fatalf("Image hat glibc %d.%d, libs.MaxGlibcMinor = %d: Messung P0 erneuern", major, minor, libs.MaxGlibcMinor)
	}
}
