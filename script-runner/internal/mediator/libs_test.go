package mediator

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
	"github.com/januaport/januaport-plugins/script-runner/internal/libs"
	"github.com/januaport/januaport-plugins/script-runner/internal/store"
)

// libsTempDir ist ein Ordner, den auch nicht beschreibbare Unterordner (0555)
// nicht am Aufräumen hindern: libs/<hash> ist absichtlich schreibgeschützt.
func libsTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
		_ = os.RemoveAll(dir)
	})
	return dir
}

// testLib ist ein synthetisches Wheel.
type testLib struct {
	dist, version string
	files         map[string]string
	tag           string // Standard: py3-none-any
	raw           []byte // ersetzt den Inhalt (bösartige Wheels)
	tamper        bool   // Inhalt weicht vom Hash im Lock ab
}

func (l testLib) fileName() string {
	tag := l.tag
	if tag == "" {
		tag = "py3-none-any"
	}
	return l.dist + "-" + l.version + "-" + tag + ".whl"
}

func (l testLib) bytes(t *testing.T) []byte {
	t.Helper()
	if l.raw != nil {
		return l.raw
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(l.files))
	for n := range l.files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(l.files[n]))
	}
	w, _ := zw.Create(l.dist + "-" + l.version + ".dist-info/METADATA")
	_, _ = w.Write([]byte("Metadata-Version: 2.1\nName: " + l.dist + "\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// libsPin beschreibt einen Pin-Ordner mit Bibliotheken.
type libsPin struct {
	dir, name    string
	libs         []testLib
	skipLock     bool
	lockOverride string
	extraFiles   map[string]string // relativ zum Pin-Ordner
	manifestTail string            // Standard: requirements: requirements.lock
	noRequires   bool
}

func (p libsPin) lockText(t *testing.T) string {
	if p.lockOverride != "" {
		return p.lockOverride
	}
	var b strings.Builder
	for _, l := range p.libs {
		h := sha256.Sum256(l.withoutTamper().bytes(t))
		b.WriteString(l.dist + "==" + l.version + " \\\n    --hash=sha256:" + hex.EncodeToString(h[:]) + "\n")
	}
	return b.String()
}

func (l testLib) withoutTamper() testLib { l.tamper = false; return l }

func (p libsPin) lockHash(t *testing.T) string {
	h := sha256.Sum256([]byte(p.lockText(t)))
	return hex.EncodeToString(h[:])
}

func (p libsPin) write(t *testing.T, f *fixture) {
	t.Helper()
	tail := p.manifestTail
	if tail == "" && !p.noRequires {
		tail = "requirements: requirements.lock\n"
	}
	f.write(p.dir+"/jnpt-script.yaml", "name: "+p.name+"\ndescription: Skript mit Bibliotheken.\nentry: main.py\ninput_schema:\n  type: object\ntools: []\ntimeout_s: 10\n"+tail)
	f.write(p.dir+"/main.py", "import demo\n")
	if !p.skipLock {
		f.write(p.dir+"/requirements.lock", p.lockText(t))
	}
	for _, l := range p.libs {
		b := l.bytes(t)
		if l.tamper {
			b = append([]byte(nil), b...)
			b[len(b)/2] ^= 0xff
		}
		f.write(p.dir+"/wheels/"+l.fileName(), string(b))
	}
	for name, content := range p.extraFiles {
		f.write(p.dir+"/"+name, content)
	}
}

func (p libsPin) row(f *fixture) pinRow { return pinRow{Name: p.name, Path: p.dir, Commit: f.commit} }

var (
	demoLib   = testLib{dist: "demo", version: "1.0", files: map[string]string{"demo/__init__.py": "A = 1\n"}}
	helperLib = testLib{dist: "helper", version: "2.1", files: map[string]string{"helper/__init__.py": "B = 2\n"}}
)

func libsEnv(t *testing.T, pinsDef []libsPin, opts ...envOpt) (*env, *fixture) {
	t.Helper()
	fx := newFixtureWith(t, func(f *fixture) {
		for _, p := range pinsDef {
			p.write(t, f)
		}
	})
	rows := make([]pinRow, 0, len(pinsDef))
	for _, p := range pinsDef {
		rows = append(rows, p.row(fx))
	}
	return newEnvFx(t, fx, rows, opts...), fx
}

func withLibsLimits(mut func(*libs.Limits)) envOpt {
	return func(c *Config) {
		l := libs.DefaultLimits
		mut(&l)
		c.LibsLimits = &l
	}
}

func withPoll(d time.Duration) envOpt { return func(c *Config) { c.PollInterval = d } }

// pinState liest Zustand und Grund eines Pins aus tools/list.
func (e *env) pinState(name string) (state, reason string) {
	e.t.Helper()
	raw, ok := e.listTools().Meta[contract.MetaRunner]
	if !ok {
		e.t.Fatal("tools/list ohne runner-Meta")
	}
	b, _ := json.Marshal(raw)
	var meta contract.RunnerMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		e.t.Fatal(err)
	}
	for _, p := range meta.Pins {
		if p.Name == name {
			return p.State, p.Reason
		}
	}
	e.t.Fatalf("Pin %s nicht in tools/list", name)
	return "", ""
}

// waitPinSettled wartet, bis der Pin nicht mehr pending ist.
func (e *env) waitPinSettled(name string) (state, reason string) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		state, reason = e.pinState(name)
		if state != contract.StatePending || time.Now().After(deadline) {
			return state, reason
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("Zeitüberschreitung: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Invariante 5/7: ein Pin mit Lock wird erst nach der Vorbereitung ready;
// der job trägt den Lock-Hash, die Wheels reisen nie mit.
func TestLibsPinBecomesReadyAndJobCarriesLockHash(t *testing.T) {
	def := libsPin{dir: "libs-pin", name: "libs_pin", libs: []testLib{demoLib, helperLib}}
	e, _ := libsEnv(t, []libsPin{def})
	var job frame.Job
	done := e.oneRun("i1", func(g *fakeGuard, j frame.Job) { job = j; g.exited(0) })
	e.waitReady("libs_pin")
	res := e.mustCall("libs_pin", map[string]any{})
	<-done
	assertEnd(t, res, contract.EndOK, "")

	hash := def.lockHash(t)
	if job.Libs != hash {
		t.Fatalf("job.Libs = %q, want %q", job.Libs, hash)
	}
	var names []string
	for _, f := range job.Files {
		names = append(names, f.Path)
		if strings.HasPrefix(f.Path, "wheels/") {
			t.Errorf("Wheel im job: %s", f.Path)
		}
	}
	if !contains(names, "main.py") || !contains(names, "requirements.lock") || !contains(names, "jnpt-script.yaml") {
		t.Fatalf("job-Dateien = %v", names)
	}
	b, err := os.ReadFile(filepath.Join(e.libs, hash, "demo", "__init__.py"))
	if err != nil || string(b) != "A = 1\n" {
		t.Fatalf("libs/<hash>/demo/__init__.py = %q, %v", b, err)
	}
	if got := dirNames(t, e.libs); len(got) != 1 || got[0] != hash {
		t.Fatalf("libs = %v", got)
	}
	if strings.Contains(e.logs.String(), "demo") || strings.Contains(e.logs.String(), hash) {
		t.Errorf("Log nennt Paketnamen oder Hash: %s", e.logs.String())
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Invariante 7 (Altbestand): ein Manifest ohne `requirements` verhält sich
// wie bisher. Ein Ordner `wheels/` gehört dort zum gepinnten Baum.
func TestLibsLegacyPinWithWheelsDirectoryBehavesAsBefore(t *testing.T) {
	def := libsPin{dir: "legacy", name: "legacy", noRequires: true, skipLock: true, extraFiles: map[string]string{"wheels/notes.txt": "keine Bibliothek\n"}}
	e, _ := libsEnv(t, []libsPin{def})
	var job frame.Job
	done := e.oneRun("i1", func(g *fakeGuard, j frame.Job) { job = j; g.exited(0) })
	e.waitReady("legacy")
	assertEnd(t, e.mustCall("legacy", map[string]any{}), contract.EndOK, "")
	<-done
	if job.Libs != "" {
		t.Fatalf("job.Libs = %q ohne requirements", job.Libs)
	}
	var names []string
	for _, f := range job.Files {
		names = append(names, f.Path)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "jnpt-script.yaml,main.py,wheels/notes.txt" {
		t.Fatalf("job-Dateien = %s", got)
	}
	raw, _ := json.Marshal(job)
	if strings.Contains(string(raw), `"libs"`) {
		t.Fatalf("job-JSON trägt libs: %s", raw)
	}
	if got := dirNames(t, e.libs); len(got) != 0 {
		t.Fatalf("libs = %v ohne requirements", got)
	}
}

// Invariante 2: jede Abweichung ist fail-closed — Pin invalid mit dem Grund,
// kein Werkzeug, kein libs/<hash>, kein Rest.
func TestLibsReasonsFailClosed(t *testing.T) {
	zipSlip := func() []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("../../evil.py")
		_, _ = w.Write([]byte("x"))
		_ = zw.Close()
		return buf.Bytes()
	}()
	symlink := func() []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		h := &zip.FileHeader{Name: "demo/link", Method: zip.Store}
		h.SetMode(os.ModeSymlink | 0o777)
		w, _ := zw.CreateHeader(h)
		_, _ = w.Write([]byte("/etc/passwd"))
		_ = zw.Close()
		return buf.Bytes()
	}()
	tests := []struct {
		name   string
		def    libsPin
		opts   []envOpt
		reason string
	}{
		{"manipuliertes Wheel", libsPin{libs: []testLib{{dist: "demo", version: "1.0", files: demoLib.files, tamper: true}}}, nil, contract.ReasonLibsHashMismatch},
		{"Lock-Eintrag ohne Wheel", libsPin{libs: []testLib{demoLib}, lockOverride: "demo==1.0 --hash=sha256:" + strings.Repeat("a", 64) + "\nother==1.0 --hash=sha256:" + strings.Repeat("b", 64) + "\n"}, nil, contract.ReasonLibsInvalid},
		{"Wheel außerhalb des Locks", libsPin{libs: []testLib{demoLib}, extraFiles: map[string]string{"wheels/stray-1.0-py3-none-any.whl": "x"}}, nil, contract.ReasonLibsInvalid},
		{"sdist", libsPin{libs: []testLib{demoLib}, extraFiles: map[string]string{"wheels/demo-1.0.tar.gz": "x"}}, nil, contract.ReasonLibsInvalid},
		{"Unterordner in wheels/", libsPin{libs: []testLib{demoLib}, extraFiles: map[string]string{"wheels/sub/x-1.0-py3-none-any.whl": "x"}}, nil, contract.ReasonLibsInvalid},
		{"falsche Plattform (arm64)", libsPin{libs: []testLib{{dist: "demo", version: "1.0", files: demoLib.files, tag: "cp312-cp312-manylinux_2_28_aarch64"}}}, nil, contract.ReasonLibsPlatform},
		{"falsche Python-Fassung", libsPin{libs: []testLib{{dist: "demo", version: "1.0", files: demoLib.files, tag: "cp311-cp311-manylinux_2_28_x86_64"}}}, nil, contract.ReasonLibsPlatform},
		{"glibc über der Obergrenze", libsPin{libs: []testLib{{dist: "demo", version: "1.0", files: demoLib.files, tag: "cp312-cp312-manylinux_2_99_x86_64"}}}, nil, contract.ReasonLibsPlatform},
		{"Zip-Slip", libsPin{libs: []testLib{{dist: "demo", version: "1.0", raw: zipSlip}}}, nil, contract.ReasonLibsInvalid},
		{"Symlink im Wheel", libsPin{libs: []testLib{{dist: "demo", version: "1.0", raw: symlink}}}, nil, contract.ReasonLibsInvalid},
		{"requirements.lock fehlt", libsPin{libs: []testLib{demoLib}, skipLock: true}, nil, contract.ReasonLibsInvalid},
		{"Lock nicht lesbar", libsPin{libs: []testLib{demoLib}, lockOverride: "demo>=1.0\n"}, nil, contract.ReasonLibsInvalid},
		{"Lock mit index-url", libsPin{libs: []testLib{demoLib}, lockOverride: "--index-url https://example.com/simple\ndemo==1.0 --hash=sha256:" + strings.Repeat("a", 64) + "\n"}, nil, contract.ReasonLibsInvalid},
		{"über der Grenze je Lock", libsPin{libs: []testLib{demoLib}}, []envOpt{withLibsLimits(func(l *libs.Limits) { l.MaxLockBytes = 5 })}, contract.ReasonLibsTooLarge},
		{"über der Grenze je Datei", libsPin{libs: []testLib{demoLib}}, []envOpt{withLibsLimits(func(l *libs.Limits) { l.MaxFileBytes = 3 })}, contract.ReasonLibsTooLarge},
		{"über der Grenze für Dateien", libsPin{libs: []testLib{demoLib}}, []envOpt{withLibsLimits(func(l *libs.Limits) { l.MaxLockFiles = 1 })}, contract.ReasonLibsTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := tt.def
			def.dir, def.name = "libs-pin", "libs_pin"
			e, _ := libsEnv(t, []libsPin{def}, tt.opts...)
			g := dialGuard(t, e.sock, "i1", 2)
			defer func() { _ = g.conn.Close() }()
			state, reason := e.waitPinSettled("libs_pin")
			if state != contract.StateInvalid || reason != tt.reason {
				t.Fatalf("Zustand %s/%s, want invalid/%s", state, reason, tt.reason)
			}
			if tools := e.listTools().Tools; len(tools) != 0 {
				t.Fatalf("Werkzeuge trotz ungültigem Pin: %d", len(tools))
			}
			assertEnd(t, e.mustCall("libs_pin", map[string]any{}), contract.EndRefused, contract.DetailState)
			// Der Fehler ist endgültig: nach weiteren Abgleichen bleibt es dabei,
			// und es liegt nichts in libs.
			time.Sleep(100 * time.Millisecond)
			if state, reason = e.pinState("libs_pin"); state != contract.StateInvalid || reason != tt.reason {
				t.Fatalf("später: %s/%s", state, reason)
			}
			if got := dirNames(t, e.libs); len(got) != 0 {
				t.Fatalf("libs = %v, want leer (kein libs/<hash>, kein .tmp)", got)
			}
		})
	}
}

func TestLibsManifestRequirementsInvalidValue(t *testing.T) {
	def := libsPin{dir: "libs-pin", name: "libs_pin", libs: []testLib{demoLib}, manifestTail: "requirements: wheels/other.lock\n"}
	e, _ := libsEnv(t, []libsPin{def})
	state, reason := e.waitPinSettled("libs_pin")
	if state != contract.StateInvalid || reason != contract.ReasonManifestInvalid {
		t.Fatalf("Zustand %s/%s", state, reason)
	}
}

func TestLibsWithoutLibsDirectoryFailsClosed(t *testing.T) {
	def := libsPin{dir: "libs-pin", name: "libs_pin", libs: []testLib{demoLib}}
	e, _ := libsEnv(t, []libsPin{def}, func(c *Config) { c.LibsDir = "" })
	state, reason := e.waitPinSettled("libs_pin")
	if state != contract.StateInvalid || reason != contract.ReasonLibsInvalid {
		t.Fatalf("Zustand %s/%s", state, reason)
	}
}

// Invariante 5: ohne fertiges libs ergibt der Lauf refused/state.
func TestLibsRunWithoutReadyLibsRefusedState(t *testing.T) {
	def := libsPin{dir: "libs-pin", name: "libs_pin", libs: []testLib{demoLib}}
	// Der Abgleich läuft von Hand, damit nichts zwischen dem Löschen und dem
	// Aufruf neu vorbereitet.
	e, _ := libsEnv(t, []libsPin{def}, withPoll(time.Hour))
	g := dialGuard(t, e.sock, "i1", 2)
	defer func() { _ = g.conn.Close() }()
	waitFor(t, "Pin ready", func() bool {
		e.m.refresh(context.Background())
		s, _ := e.pinState("libs_pin")
		return s == contract.StateReady
	})
	hash := def.lockHash(t)
	removeTree(t, filepath.Join(e.libs, hash))
	res := e.mustCall("libs_pin", map[string]any{})
	assertEnd(t, res, contract.EndRefused, contract.DetailState)
	if _, err := os.Stat(filepath.Join(e.libs, hash)); err == nil {
		t.Fatal("der Lauf hat libs/<hash> stillschweigend neu angelegt")
	}
}

func removeTree(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
}

// Invariante 5: derselbe Lock bei zwei Pins wird einmal vorbereitet.
func TestLibsSameLockTwoPinsPreparedOnce(t *testing.T) {
	a := libsPin{dir: "pin-a", name: "pin_a", libs: []testLib{demoLib, helperLib}}
	b := libsPin{dir: "pin-b", name: "pin_b", libs: []testLib{demoLib, helperLib}}
	if a.lockHash(t) != b.lockHash(t) {
		t.Fatal("Testfehler: Locks verschieden")
	}
	e, _ := libsEnv(t, []libsPin{a, b})
	g := dialGuard(t, e.sock, "i1", 2)
	defer func() { _ = g.conn.Close() }()
	e.waitReady("pin_a")
	e.waitReady("pin_b")
	if n := e.m.libs.PreparedCount(); n != 1 {
		t.Fatalf("PreparedCount = %d, want 1", n)
	}
	if got := dirNames(t, e.libs); len(got) != 1 || got[0] != a.lockHash(t) {
		t.Fatalf("libs = %v", got)
	}
}

// Invariante 5: ein neuer Lock ergibt ein neues Verzeichnis; aufgeräumt wird
// nur, was nicht mehr referenziert ist.
func TestLibsNewLockNewDirectoryAndPruneUnreferenced(t *testing.T) {
	a := libsPin{dir: "pin-a", name: "pin_a", libs: []testLib{demoLib}}
	b := libsPin{dir: "pin-b", name: "pin_b", libs: []testLib{helperLib}}
	e, fx := libsEnv(t, []libsPin{a, b})
	g := dialGuard(t, e.sock, "i1", 2)
	defer func() { _ = g.conn.Close() }()
	e.waitReady("pin_a")
	e.waitReady("pin_b")
	want := []string{a.lockHash(t), b.lockHash(t)}
	sort.Strings(want)
	if got := dirNames(t, e.libs); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("libs = %v, want %v", got, want)
	}
	// pin_b wird entfernt: sein Lock ist nicht mehr referenziert.
	e.writePins([]pinRow{a.row(fx)})
	waitFor(t, "Lock von pin_b aufgeräumt", func() bool {
		got := dirNames(t, e.libs)
		return len(got) == 1 && got[0] == a.lockHash(t)
	})
	time.Sleep(100 * time.Millisecond)
	if got := dirNames(t, e.libs); len(got) != 1 || got[0] != a.lockHash(t) {
		t.Fatalf("referenzierter Lock verschwunden: %v", got)
	}
	if s, _ := e.pinState("pin_a"); s != contract.StateReady {
		t.Fatalf("pin_a = %s", s)
	}
}

// Ein unlesbares pins.json räumt nichts weg (keine Pins heißt nicht
// „nichts referenziert“).
func TestLibsUnreadablePinsFileKeepsLibs(t *testing.T) {
	a := libsPin{dir: "pin-a", name: "pin_a", libs: []testLib{demoLib}}
	e, _ := libsEnv(t, []libsPin{a})
	g := dialGuard(t, e.sock, "i1", 2)
	defer func() { _ = g.conn.Close() }()
	e.waitReady("pin_a")
	if err := os.WriteFile(filepath.Join(e.keys, "pins.json"), []byte("{kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := dirNames(t, e.libs); len(got) != 1 {
		t.Fatalf("libs = %v", got)
	}
}

// Ein URL-Wechsel leert libs.
func TestLibsRepositoryURLChangeClearsLibs(t *testing.T) {
	a := libsPin{dir: "pin-a", name: "pin_a", libs: []testLib{demoLib}}
	e, fx := libsEnv(t, []libsPin{a})
	g := dialGuard(t, e.sock, "i1", 2)
	defer func() { _ = g.conn.Close() }()
	e.waitReady("pin_a")
	if got := dirNames(t, e.libs); len(got) != 1 {
		t.Fatalf("libs = %v", got)
	}
	b, _ := json.Marshal(map[string]any{"version": 1, "repo_url": "ssh://git@git.example.com/org/other.git", "pins": []pinRow{a.row(fx)}})
	if err := os.WriteFile(filepath.Join(e.keys, "pins.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "libs geleert", func() bool { return len(dirNames(t, e.libs)) == 0 })
	// Die Freigabe des abholers gilt für die alte URL: der Pin wartet.
	time.Sleep(100 * time.Millisecond)
	if s, _ := e.pinState("pin_a"); s != contract.StatePending {
		t.Fatalf("pin_a = %s, want pending", s)
	}
	if got := dirNames(t, e.libs); len(got) != 0 {
		t.Fatalf("libs = %v nach dem URL-Wechsel", got)
	}
}

// Der erste Abgleich nach dem Start ist kein URL-Wechsel: libs überlebt einen
// Neustart (nach der Prüfung gegen das Manifest, B3).
func TestLibsSurvivesRestartAndIsVerified(t *testing.T) {
	a := libsPin{dir: "pin-a", name: "pin_a", libs: []testLib{demoLib}}
	e, fx := libsEnv(t, []libsPin{a})
	g := dialGuard(t, e.sock, "i1", 2)
	e.waitReady("pin_a")
	_ = g.conn.Close()
	hash := a.lockHash(t)

	// Neustart mit demselben libs-Ordner: ein Rest aus einem Absturz (.tmp)
	// und ein verändertes Verzeichnis werden beim Start verworfen.
	if err := os.MkdirAll(filepath.Join(e.libs, hash+".tmp", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	e2 := newEnvFx(t, fx, []pinRow{a.row(fx)}, func(c *Config) { c.LibsDir = e.libs; c.StoreDir = fx.store })
	_ = e2
	if got := dirNames(t, e.libs); len(got) != 1 || got[0] != hash {
		t.Fatalf("libs nach dem Neustart = %v", got)
	}
	if e2.m.libs.PreparedCount() != 0 {
		t.Fatalf("PreparedCount = %d nach dem Neustart: das geprüfte Verzeichnis wurde neu gebaut", e2.m.libs.PreparedCount())
	}
}

// Invariante 2 (B3): ein beim Start veränderter Lock-Ordner wird verworfen
// und neu vorbereitet; der Pin bleibt bis dahin pending.
func TestLibsTamperedDirectoryIsDiscardedAtStart(t *testing.T) {
	a := libsPin{dir: "pin-a", name: "pin_a", libs: []testLib{demoLib}}
	fx := newFixtureWith(t, func(f *fixture) { a.write(t, f) })
	libsDir := libsTempDir(t)
	hash := a.lockHash(t)
	evil := filepath.Join(libsDir, hash, "demo")
	if err := os.MkdirAll(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evil, "__init__.py"), []byte("import os; os.system('x')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEnvFx(t, fx, []pinRow{a.row(fx)}, func(c *Config) { c.LibsDir = libsDir })
	g := dialGuard(t, e.sock, "i1", 2)
	defer func() { _ = g.conn.Close() }()
	e.waitReady("pin_a")
	b, err := os.ReadFile(filepath.Join(libsDir, hash, "demo", "__init__.py"))
	if err != nil || string(b) != "A = 1\n" {
		t.Fatalf("__init__.py = %q, %v (der veränderte Inhalt hat überlebt)", b, err)
	}
	if e.m.libs.PreparedCount() != 1 {
		t.Fatalf("PreparedCount = %d, want 1 (neu vorbereitet)", e.m.libs.PreparedCount())
	}
}

// Invariante 1: Der Läufer lädt nichts aus dem Netz. Weder vermittler noch
// abholer (noch eine andere Stelle ohne Weg B) enthalten einen HTTP-Client
// für Downloads, kein pip, keine Index-Adresse.
func TestNoDownloadClientInSource(t *testing.T) {
	root := "../.."
	httpClientAllowed := map[string]bool{"internal/mediator/run.go": true, "internal/mediator/schedules.go": true}
	noNetPackages := []string{"libs", "store", "fetcher", "guard", "manifest", "pins", "gitref", "frame", "schedules", "ticker"}
	forbiddenImportsEverywhere := map[string]bool{"plugin": true}
	clientNames := map[string]bool{"Get": true, "Head": true, "Post": true, "PostForm": true, "DefaultClient": true,
		"NewRequest": true, "NewRequestWithContext": true, "Client": true, "Transport": true, "DefaultTransport": true}
	forbiddenLiterals := []string{"pypi", "pythonhosted", "pip install", "pip download", "pip3", "get-pip", "ensurepip", "index-url", "easy_install"}

	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "probe" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 20 {
		t.Fatalf("nur %d Quelldateien gefunden: Testfehler", len(files))
	}
	for _, p := range files {
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		pkg := ""
		if strings.HasPrefix(rel, "internal/") {
			pkg = strings.Split(rel, "/")[1]
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if forbiddenImportsEverywhere[path] {
				t.Errorf("%s importiert %s", rel, path)
			}
			for _, np := range noNetPackages {
				if pkg == np && path == "net/http" {
					t.Errorf("%s importiert %s", rel, path)
				}
			}
			if pkg == "libs" && (path == "net" || path == "os/exec" || strings.HasPrefix(path, "net/")) {
				t.Errorf("%s (Entpacken) importiert %s", rel, path)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "http" && clientNames[x.Sel.Name] && !httpClientAllowed[rel] {
					t.Errorf("%s: http.%s außerhalb von Weg B und Taktgeber", rel, x.Sel.Name)
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					lit := strings.ToLower(x.Value)
					for _, bad := range forbiddenLiterals {
						if strings.Contains(lit, bad) {
							t.Errorf("%s: Zeichenkette %s", rel, x.Value)
						}
					}
				}
			}
			return true
		})
	}
}

// Altbestand: ohne `requirements` gelten die alten Grenzen auch für einen
// Ordner wheels/ (4 MiB für den ganzen Pin-Ordner) — er wird weder übersprungen
// noch anders bewertet.
func TestLibsLegacyPinWithOversizedWheelsDirectoryStaysInvalid(t *testing.T) {
	def := libsPin{dir: "legacy", name: "legacy", noRequires: true, skipLock: true,
		extraFiles: map[string]string{"wheels/big.whl": strings.Repeat("x", 5<<20)}}
	e, _ := libsEnv(t, []libsPin{def})
	state, reason := e.waitPinSettled("legacy")
	if state != contract.StateInvalid || reason != contract.ReasonManifestInvalid {
		t.Fatalf("Zustand %s/%s, want invalid/manifest_invalid wie bisher", state, reason)
	}
}

// Mit `requirements` gilt die Grenze nicht für wheels/ (eigene Grenzen).
func TestLibsPinWithBigWheelsDirectoryIsNotLimitedByTreeCap(t *testing.T) {
	// Schwer komprimierbar (sonst griffe das Verhältnis zu Recht): das Wheel selbst
	// ist größer als die 4 MiB des Code-Baums.
	rnd := make([]byte, 5<<20)
	_, _ = rand.Read(rnd)
	big := testLib{dist: "big", version: "1.0", files: map[string]string{"big/__init__.py": "X = 1\n", "big/data.bin": string(rnd)}}
	def := libsPin{dir: "libs-pin", name: "libs_pin", libs: []testLib{big}}
	e, _ := libsEnv(t, []libsPin{def})
	g := dialGuard(t, e.sock, "i1", 2)
	defer func() { _ = g.conn.Close() }()
	e.waitReady("libs_pin")
	if n := len(big.bytes(t)); n <= store.MaxTreeBytes {
		t.Fatalf("Testfehler: Wheel nur %d Byte, nicht über der Grenze des Code-Baums", n)
	}
}

// Aufräumen nur bei vollständiger Lage: Ein Pin, dessen Manifest noch nicht
// gelesen ist (hier: Commit nicht im Store), könnte sein Lock brauchen.
func TestLibsPruneWaitsForPendingPins(t *testing.T) {
	a := libsPin{dir: "pin-a", name: "pin_a", libs: []testLib{demoLib}}
	b := libsPin{dir: "pin-b", name: "pin_b", libs: []testLib{helperLib}}
	e, fx := libsEnv(t, []libsPin{a, b})
	g := dialGuard(t, e.sock, "i1", 2)
	defer func() { _ = g.conn.Close() }()
	e.waitReady("pin_a")
	e.waitReady("pin_b")
	e.writePins([]pinRow{a.row(fx), {Name: "pin_b", Path: "pin-b", Commit: strings.Repeat("1", 40)}})
	time.Sleep(300 * time.Millisecond) // viele Abgleiche
	if state, _ := e.pinState("pin_b"); state != contract.StatePending {
		t.Fatalf("pin_b = %s, want pending", state)
	}
	want := []string{a.lockHash(t), b.lockHash(t)}
	sort.Strings(want)
	if got := dirNames(t, e.libs); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("libs = %v, want %v (nichts aufräumen, solange ein Pin unbekannt ist)", got, want)
	}
}

// Verschwindet libs/<hash> unter einem gecachten ready, wird neu vorbereitet
// (kein stilles Weiterlaufen mit einem Verzeichnis, das es nicht gibt).
func TestLibsMissingDirectoryIsRebuilt(t *testing.T) {
	a := libsPin{dir: "pin-a", name: "pin_a", libs: []testLib{demoLib}}
	e, _ := libsEnv(t, []libsPin{a})
	g := dialGuard(t, e.sock, "i1", 2)
	defer func() { _ = g.conn.Close() }()
	e.waitReady("pin_a")
	hash := a.lockHash(t)
	removeTree(t, filepath.Join(e.libs, hash))
	waitFor(t, "libs/<hash> neu vorbereitet", func() bool {
		_, err := os.Stat(filepath.Join(e.libs, hash, "demo", "__init__.py"))
		return err == nil && e.m.libs.PreparedCount() == 2
	})
	e.waitReady("pin_a")
}

// Invariante 2 für Pins mit gleichem Lock: Das manipulierte Wheel des einen
// Pins wird auch dann erkannt, wenn der Lock schon vorbereitet ist — in jeder
// Reihenfolge, und der gute Pin bleibt ready.
func TestLibsTamperedWheelWithSharedLockIsInvalidInAnyOrder(t *testing.T) {
	good := libsPin{dir: "pin-good", name: "pin_good", libs: []testLib{demoLib}}
	evilLib := demoLib
	evilLib.tamper = true
	evil := libsPin{dir: "pin-evil", name: "pin_evil", libs: []testLib{evilLib}}
	if good.lockHash(t) != evil.lockHash(t) {
		t.Fatal("Testfehler: die Locks sind verschieden")
	}
	for name, order := range map[string][]libsPin{"gut zuerst": {good, evil}, "manipuliert zuerst": {evil, good}} {
		t.Run(name, func(t *testing.T) {
			e, _ := libsEnv(t, order)
			g := dialGuard(t, e.sock, "i1", 2)
			defer func() { _ = g.conn.Close() }()
			e.waitReady("pin_good")
			state, reason := e.waitPinSettled("pin_evil")
			if state != contract.StateInvalid || reason != contract.ReasonLibsHashMismatch {
				t.Fatalf("pin_evil = %s/%s, want invalid/libs_hash_mismatch", state, reason)
			}
			assertEnd(t, e.mustCall("pin_evil", map[string]any{}), contract.EndRefused, contract.DetailState)
			if s, _ := e.pinState("pin_good"); s != contract.StateReady {
				t.Fatalf("pin_good = %s", s)
			}
			if got := dirNames(t, e.libs); len(got) != 1 || got[0] != good.lockHash(t) {
				t.Fatalf("libs = %v", got)
			}
		})
	}
}
