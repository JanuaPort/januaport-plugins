package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
)

// bed ist ein vollständiger Windows-Laufplatz mit synthetischem Paket und
// Fake-Prozess: Execute erreicht Spawn, ohne Python oder Windows zu brauchen.
type bed struct {
	t        *testing.T
	st       *State
	engine   *Engine
	env      Environment
	result   Result
	envDir   string
	session  Session
	launches []Launch
	onSpawn  func(Launch)
	exit     func(w *io.PipeWriter) // nil: sofort exited/0
	spawnErr error
	atClose  []bool // existierte das Arbeitsverzeichnis beim Schließen des Prozesses?
	mu       sync.Mutex
}

type nopWC struct{}

func (nopWC) Write(b []byte) (int, error) { return len(b), nil }
func (nopWC) Close() error                { return nil }

type fakeProc struct {
	r     *io.PipeReader
	w     *io.PipeWriter
	close func()
	once  sync.Once
}

func (p *fakeProc) Input() io.WriteCloser { return nopWC{} }
func (p *fakeProc) Output() io.ReadCloser { return p.r }
func (p *fakeProc) Wait() error           { return nil }
func (p *fakeProc) Kill() error           { p.w.Close(); return nil }
func (p *fakeProc) Close() error {
	p.once.Do(func() {
		if p.close != nil {
			p.close()
		}
		p.w.Close()
	})
	return nil
}

func newBed(t *testing.T) *bed {
	t.Helper()
	st, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	manifest := "name: demo\ndescription: test\nentry: main.py\ninput_schema: {type: object}\ntools: []\ntimeout_s: 10\npython: python.zip\n"
	commit, raw := testfixture.Package(map[string][]byte{"main.py": []byte("pass"), "jnpt-desktop.yaml": []byte(manifest)}, "")
	pin := Pin{Pin: "demo", Commit: commit, Path: "demo", Algo: "sha256", DeclaredTools: []string{}, Schedules: []Schedule{}}
	dir := envKey(pin)
	envDir := filepath.Join(st.Root, "envs", dir)
	if err = os.MkdirAll(filepath.Join(envDir, "runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(envDir, "runtime", "python.exe"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	seal, err := Snapshot(envDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(st.Root, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(st.Root, "packages", dir), raw, 0600); err != nil {
		t.Fatal(err)
	}
	b := &bed{t: t, st: st, envDir: envDir, session: Session{Active: true, Account: "own"}}
	b.env = Environment{Origin: pin, Dir: dir, Files: seal, Entry: "main.py", Timeout: 10, Tools: []string{}}
	b.result = Result{RunKey: "demo/daily/2026-10-23T10:00:00Z", ManifestSeq: 1, Commit: commit}
	cfg := Config{ExpectedAccount: "own"}
	cfg.StartPolicy.Mode = "always"
	b.engine = &Engine{State: st, Config: cfg, Session: func() (Session, error) { return b.session, nil }, Spawn: b.spawn,
		ChildEnv: []string{"SystemRoot=C:\\Windows", "OMP_NUM_THREADS=1"}}
	return b
}

func (b *bed) spawn(_ context.Context, l Launch) (Process, error) {
	b.mu.Lock()
	b.launches = append(b.launches, l)
	b.mu.Unlock()
	if b.spawnErr != nil {
		return nil, b.spawnErr
	}
	if b.onSpawn != nil {
		b.onSpawn(l)
	}
	r, w := io.Pipe()
	p := &fakeProc{r: r, w: w, close: func() {
		_, err := os.Stat(l.Dir)
		b.mu.Lock()
		b.atClose = append(b.atClose, err == nil)
		b.mu.Unlock()
	}}
	go func() {
		if b.exit != nil {
			b.exit(w)
			return
		}
		WriteFrame(w, map[string]any{"type": "exited", "code": 0})
	}()
	return p, nil
}

func (b *bed) execute() Result {
	b.t.Helper()
	return b.engine.Execute(context.Background(), b.result, b.env, json.RawMessage(`{}`), "", 10, 0)
}
func (b *bed) base() string   { return filepath.Join(b.st.Root, "tmp") }
func (b *bed) runDir() string { return filepath.Join(b.base(), RunID(b.result.Key())) }

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("Symlink nicht anlegbar (Windows ohne Berechtigung); Junction-Kanarienvogel läuft in internal/platform:", err)
	}
}

func canaryDir(t *testing.T) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "kanarienvogel.txt")
	if err := os.WriteFile(file, []byte("lebt"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

func TestLaufBekommtPrivatesTempAlsArbeitsverzeichnis(t *testing.T) {
	b := newBed(t)
	outside, canary := canaryDir(t)
	var during struct {
		empty, absolute bool
	}
	b.onSpawn = func(l Launch) {
		entries, err := os.ReadDir(l.Dir)
		during.empty = err == nil && len(entries) == 0
		during.absolute = filepath.IsAbs(l.Args[2])
		// Das Kind schreibt hinein: verschachtelt, schreibgeschützt, und ein Verweis nach außen.
		os.MkdirAll(filepath.Join(l.Dir, "a", "b"), 0700)
		os.WriteFile(filepath.Join(l.Dir, "a", "b", "datei.txt"), []byte("x"), 0600)
		ro := filepath.Join(l.Dir, "nur-lesen.txt")
		os.WriteFile(ro, []byte("x"), 0600)
		os.Chmod(ro, 0400)
		os.Symlink(outside, filepath.Join(l.Dir, "verweis"))
	}
	got := b.execute()
	if got.End != "ok" || len(b.launches) != 1 {
		t.Fatalf("positive Kontrolle: %+v launches=%d", got, len(b.launches))
	}
	l := b.launches[0]
	if l.Dir != b.runDir() {
		t.Fatalf("Arbeitsverzeichnis %q, erwartet %q", l.Dir, b.runDir())
	}
	if strings.HasPrefix(l.Dir, b.envDir) {
		t.Fatal("Arbeitsverzeichnis liegt in der geprüften Umgebung")
	}
	if !during.empty || !during.absolute {
		t.Fatalf("Verzeichnis nicht leer oder Bootstrap-Pfad relativ: %+v", during)
	}
	if l.Args[0] != "-I" || l.Args[1] != "-B" || l.Args[2] != filepath.Join(b.envDir, "runtime", "bootstrap.py") {
		t.Fatalf("Argumente %q", l.Args)
	}
	if l.Exe != filepath.Join(b.envDir, "runtime", "python.exe") {
		t.Fatalf("Exe %q", l.Exe)
	}
	temp, tmp := 0, 0
	for _, e := range l.Env {
		switch e {
		case "TEMP=" + l.Dir:
			temp++
		case "TMP=" + l.Dir:
			tmp++
		}
	}
	if temp != 1 || tmp != 1 {
		t.Fatalf("TEMP/TMP nicht genau einmal auf das Laufverzeichnis gesetzt: %q", l.Env)
	}
	if len(b.atClose) != 1 || !b.atClose[0] {
		t.Fatalf("Verzeichnis wurde vor dem Beenden des Prozessbaums gelöscht: %v", b.atClose)
	}
	if exists(l.Dir) {
		t.Fatal("Temp-Verzeichnis überlebt den Lauf")
	}
	if !exists(canary) {
		t.Fatal("Aufräumen ist dem Verweis nach außen gefolgt")
	}
	if err := VerifyEnvironment(b.envDir, b.env.Files); err != nil {
		t.Fatal("Umgebung verändert", err)
	}
	if j := b.st.Journal(); j.Marker == nil || j.Marker.Phase != "started" {
		t.Fatal("Laufmarker fehlt", j.Marker)
	}
}

func TestLaufTempWirdBeiJedemEndeAufgeraeumt(t *testing.T) {
	for name, setup := range map[string]func(b *bed) context.Context{
		"Spawn-Fehler": func(b *bed) context.Context { b.spawnErr = ErrProtocol; return context.Background() },
		"Exit-Code 1": func(b *bed) context.Context {
			b.exit = func(w *io.PipeWriter) { WriteFrame(w, map[string]any{"type": "exited", "code": 1}) }
			return context.Background()
		},
		"Abbruch": func(b *bed) context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			b.exit = func(w *io.PipeWriter) { time.Sleep(50 * time.Millisecond); cancel() }
			return ctx
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := newBed(t)
			b.onSpawn = func(l Launch) { os.WriteFile(filepath.Join(l.Dir, "rest.txt"), []byte("x"), 0600) }
			ctx := setup(b)
			b.engine.Execute(ctx, b.result, b.env, json.RawMessage(`{}`), "", 10, 0)
			if exists(b.runDir()) {
				t.Fatal("Temp-Verzeichnis überlebt")
			}
		})
	}
}

func TestLaufTempExistiertSchonWirdAbgelehnt(t *testing.T) { // L4
	b := newBed(t)
	if err := os.MkdirAll(b.runDir(), 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(b.runDir(), "fremd.txt")
	os.WriteFile(foreign, []byte("fremd"), 0600)
	got := b.execute()
	if got.End != "internal" || len(b.launches) != 0 {
		t.Fatalf("fremdes Verzeichnis übernommen: %+v launches=%d", got, len(b.launches))
	}
	if !exists(foreign) {
		t.Fatal("fremdes Verzeichnis angefasst")
	}
	if b.st.Journal().Marker != nil {
		t.Fatal("Laufmarker trotz Ablehnung vor dem Start")
	}
}

func TestLaufTempAnlegenSchlaegtFehlKeinStart(t *testing.T) {
	b := newBed(t)
	b.engine.Temps = &RunTemps{Base: b.base(), Mkdir: func(string, bool) error { return errors.New("ACL") }}
	got := b.execute()
	if got.End != "internal" || len(b.launches) != 0 || b.st.Journal().Marker != nil {
		t.Fatalf("%+v launches=%d", got, len(b.launches))
	}
}

func TestKindUmgebungOhneProfil(t *testing.T) { // L6
	b := newBed(t)
	profil := `C:\Users\beispiel\AppData\Local`
	b.engine.ChildEnv = []string{
		"SystemRoot=C:\\Windows", "OMP_NUM_THREADS=1",
		"LOCALAPPDATA=" + profil, "UserProfile=C:\\Users\\beispiel", "appdata=" + profil,
		"HOME=C:\\Users\\beispiel", "HOMEPATH=\\Users\\beispiel", "HOMEDRIVE=C:",
		"TEMP=" + profil + "\\Temp", "tmp=" + profil + "\\Temp", "TMPDIR=" + profil,
	}
	b.execute()
	if len(b.launches) != 1 {
		t.Fatal("kein Start")
	}
	l := b.launches[0]
	keep := 0
	for _, kv := range l.Env {
		key, value, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(key) {
		case "LOCALAPPDATA", "USERPROFILE", "APPDATA", "HOME", "HOMEPATH", "HOMEDRIVE", "TMPDIR":
			t.Fatalf("Profilvariable im Kind: %s", kv)
		case "TEMP", "TMP":
			if value != l.Dir {
				t.Fatalf("%s zeigt nicht auf das Laufverzeichnis: %s", key, kv)
			}
		case "SYSTEMROOT", "OMP_NUM_THREADS":
			keep++
		}
		if strings.Contains(value, "beispiel") {
			t.Fatalf("Wert zeigt ins Profil: %s", kv)
		}
		if strings.HasPrefix(value, b.st.Root) && value != l.Dir {
			t.Fatalf("Wert zeigt in den Zustand des Läufers: %s", kv)
		}
	}
	if keep != 2 {
		t.Fatalf("harmlose Variablen verloren: %q", l.Env)
	}
}

func TestTempLiegtAusserhalbVonJournalUmgebungUndGeheimnissen(t *testing.T) {
	b := newBed(t)
	b.execute()
	base := b.base()
	for _, other := range []string{"envs", "packages", "journal", "manifest", "credential"} {
		p := filepath.Join(b.st.Root, other)
		if p == base || strings.HasPrefix(base, p+string(filepath.Separator)) || strings.HasPrefix(p, base+string(filepath.Separator)) {
			t.Fatalf("tmp und %s verschachtelt", other)
		}
	}
}

func TestRunIDFormat(t *testing.T) { // L3
	good := RunID("demo/daily/2026-10-23T10:00:00Z")
	if !ValidRunID(good) || len(good) != 64 || RunID("demo/daily/2026-10-23T10:00:01Z") == good {
		t.Fatalf("abgeleitete ID ungültig oder nicht eindeutig: %q", good)
	}
	bad := []string{
		"", "..", ".", "../x", `..\x`, "a/b", `a\b`, "C:", `C:\x`, `\\server\share`, "/abs",
		strings.ToUpper(good), good[:63], good + "0", good[:63] + "g", good[:63] + " ", good[:63] + "\x00",
		strings.Repeat("a", 32) + "/" + strings.Repeat("b", 31), strings.Repeat("a", 62) + "..",
		"11111111-1111-4111-8111-111111111111",
	}
	for _, id := range bad {
		if ValidRunID(id) {
			t.Errorf("ID akzeptiert: %q", id)
		}
	}
}

func TestRunTempsCreateVerlaesstDieBasisNie(t *testing.T) { // L3 am Anlegen
	root := t.TempDir()
	temps := RunTemps{Base: filepath.Join(root, "tmp")}
	for _, id := range []string{"..", `..\..\fremd`, "../fremd", "C:", "", strings.Repeat("A", 64)} {
		if dir, err := temps.Create(id); err == nil {
			t.Fatalf("ID %q angenommen: %s", id, dir)
		}
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() != "tmp" {
			t.Fatalf("außerhalb der Basis angelegt: %s", e.Name())
		}
	}
	if sub, _ := os.ReadDir(filepath.Join(root, "tmp")); len(sub) != 0 {
		t.Fatal("trotz Ablehnung angelegt")
	}
}

func TestRunTempsAnlegenNeuUndExklusiv(t *testing.T) { // L4
	root := t.TempDir()
	type call struct {
		path      string
		exclusive bool
	}
	var calls []call
	temps := RunTemps{Base: filepath.Join(root, "tmp"), Mkdir: func(p string, exclusive bool) error {
		calls = append(calls, call{p, exclusive})
		if exclusive {
			return os.Mkdir(p, 0700)
		}
		return os.MkdirAll(p, 0700)
	}}
	id := RunID("a")
	dir, err := temps.Create(id)
	if err != nil || dir != filepath.Join(root, "tmp", id) {
		t.Fatal(dir, err)
	}
	if len(calls) != 2 || calls[0] != (call{temps.Base, false}) || calls[1] != (call{dir, true}) {
		t.Fatalf("Anlegen nicht exklusiv für das Laufverzeichnis: %+v", calls)
	}
	// Standard-Mkdir: ein vorhandenes Verzeichnis wird nie übernommen.
	fresh := RunTemps{Base: filepath.Join(root, "tmp2")}
	foreign := filepath.Join(fresh.Base, id)
	os.MkdirAll(foreign, 0700)
	os.WriteFile(filepath.Join(foreign, "fremd.txt"), []byte("x"), 0600)
	if _, err = fresh.Create(id); err == nil {
		t.Fatal("vorhandenes Verzeichnis übernommen")
	}
	if !exists(filepath.Join(foreign, "fremd.txt")) {
		t.Fatal("fremdes Verzeichnis verändert")
	}
	// Auch ein vorhandener Verweis gleichen Namens wird abgelehnt.
	outside, canary := canaryDir(t)
	link := filepath.Join(fresh.Base, RunID("b"))
	symlinkOrSkip(t, outside, link)
	if _, err = fresh.Create(RunID("b")); err == nil {
		t.Fatal("Verweis übernommen")
	}
	if !exists(canary) {
		t.Fatal("Kanarienvogel verloren")
	}
}

func TestRunTempsBasisAlsVerweisWirdAbgelehnt(t *testing.T) {
	root := t.TempDir()
	outside, _ := canaryDir(t)
	symlinkOrSkip(t, outside, filepath.Join(root, "tmp"))
	if _, err := (RunTemps{Base: filepath.Join(root, "tmp")}).Create(RunID("a")); err == nil {
		t.Fatal("Basis hinter Verweis akzeptiert")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Fatal("hinter dem Verweis angelegt")
	}
}

func TestRunTempsRemoveFolgtKeinemVerweis(t *testing.T) { // L5
	root := t.TempDir()
	temps := RunTemps{Base: filepath.Join(root, "tmp")}
	dir, err := temps.Create(RunID("a"))
	if err != nil {
		t.Fatal(err)
	}
	outside, canary := canaryDir(t)
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0700)
	symlinkOrSkip(t, outside, filepath.Join(sub, "verweis"))
	os.WriteFile(filepath.Join(dir, "nur-lesen.txt"), []byte("x"), 0600)
	os.Chmod(filepath.Join(dir, "nur-lesen.txt"), 0400)
	if err = temps.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if exists(dir) {
		t.Fatal("Verzeichnis nicht entfernt")
	}
	if !exists(canary) {
		t.Fatal("Aufräumen ist dem Verweis gefolgt")
	}
}

func TestRunTempsRemoveNurEigeneLaufordner(t *testing.T) {
	root := t.TempDir()
	temps := RunTemps{Base: filepath.Join(root, "tmp")}
	dir, _ := temps.Create(RunID("a"))
	_, canary := canaryDir(t)
	for _, p := range []string{temps.Base, root, filepath.Dir(canary), filepath.Join(temps.Base, "..", "fremd"), filepath.Join(temps.Base, "kein-hex"), filepath.Join(dir, "sub")} {
		if err := temps.Remove(p); err == nil {
			t.Errorf("fremder Pfad akzeptiert: %s", p)
		}
	}
	if !exists(temps.Base) || !exists(root) || !exists(canary) || !exists(dir) {
		t.Fatal("außerhalb eines Laufordners gelöscht")
	}
	if err := temps.Remove(filepath.Join(temps.Base, RunID("nie-da"))); err != nil {
		t.Fatal("fehlender Laufordner ist kein Fehler", err)
	}
}

func TestRunTempsSweepRaeumtRestePlusVerweiseOhneEinzusteigen(t *testing.T) { // L5 Besen
	root := t.TempDir()
	temps := RunTemps{Base: filepath.Join(root, "tmp")}
	if err := temps.Sweep(); err != nil {
		t.Fatal("fehlende Basis ist kein Fehler", err)
	}
	a, _ := temps.Create(RunID("a"))
	os.MkdirAll(filepath.Join(a, "x", "y"), 0700)
	os.WriteFile(filepath.Join(a, "x", "y", "rest.txt"), []byte("x"), 0600)
	b, _ := temps.Create(RunID("b"))
	outside, canary := canaryDir(t)
	symlinkOrSkip(t, outside, filepath.Join(b, "verweis"))
	symlinkOrSkip(t, outside, filepath.Join(temps.Base, "direkt-in-tmp"))
	os.WriteFile(filepath.Join(temps.Base, "lose-datei"), []byte("x"), 0600)
	if err := temps.Sweep(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(temps.Base); len(entries) != 0 {
		t.Fatalf("Reste: %d", len(entries))
	}
	if !exists(temps.Base) {
		t.Fatal("Basis selbst entfernt")
	}
	if !exists(canary) {
		t.Fatal("Besen ist einem Verweis gefolgt")
	}
}

func TestRunRaeumtRestordnerBeimStartAuf(t *testing.T) {
	b := newBed(t)
	rest := filepath.Join(b.base(), RunID("alter-lauf"))
	os.MkdirAll(filepath.Join(rest, "x"), 0700)
	os.WriteFile(filepath.Join(rest, "x", "rest.txt"), []byte("x"), 0600)
	api, err := NewAPI(Trust{URL: "http://127.0.0.1:1"}, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	b.engine.API = api
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = b.engine.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if exists(rest) {
		t.Fatal("Rest vom letzten Lauf bleibt liegen")
	}
}

func TestRevokeFolgtKeinemVerweisInTmp(t *testing.T) { // SEC R1 c-6519227223
	st, _ := OpenState(t.TempDir(), testCipher{})
	outside, canary := canaryDir(t)
	run := filepath.Join(st.Root, "tmp", RunID("a"))
	os.MkdirAll(run, 0700)
	symlinkOrSkip(t, outside, filepath.Join(run, "verweis"))
	if err := st.Revoke(); err != nil {
		t.Fatal(err)
	}
	if exists(st.Root) || !exists(canary) {
		t.Fatalf("Wurzel gelöscht=%t, Kanarienvogel lebt=%t", !exists(st.Root), exists(canary))
	}
}
