package libs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

// fakeSource ist die Senke: Wheels aus dem Speicher, zählt die Abrufe.
type fakeSource struct {
	mu      sync.Mutex
	files   map[string][]byte
	fetched []string
	gate    chan struct{} // gesetzt: Fetch wartet, bis er geschlossen wird
	err     error
}

func (s *fakeSource) Fetch(ctx context.Context, file string, dst io.Writer, max int64) error {
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	s.fetched = append(s.fetched, file)
	err, b := s.err, s.files[file]
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if int64(len(b)) > max {
		return &Error{Reason: contract.ReasonLibsTooLarge, Msg: "Wheel zu groß"}
	}
	_, werr := dst.Write(b)
	return werr
}

func (s *fakeSource) fetchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.fetched)
}

type libFixture struct {
	dir    string
	m      *Manager
	src    *fakeSource
	plan   Plan
	wheels map[string][]byte
}

func newManager(t *testing.T, dir string, mut func(*Limits)) *Manager {
	t.Helper()
	lim := DefaultLimits
	if mut != nil {
		mut(&lim)
	}
	m, err := Open(dir, tempDir(t), lim, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	m.retryAfter = 0
	t.Cleanup(m.Close)
	return m
}

// newLibFixture legt zwei Wheels (demo, helper) an und plant einen Lock dafür.
func newLibFixture(t *testing.T, mut func(*Limits)) *libFixture {
	t.Helper()
	dir := tempDir(t)
	demo := wheelBytes(t, "demo", "1.0", map[string]string{"demo/__init__.py": "A = 1\n"})
	helper := wheelBytes(t, "helper", "2.1", map[string]string{"helper/__init__.py": "B = 2\n"})
	f := &libFixture{dir: dir, m: newManager(t, dir, mut), wheels: map[string][]byte{
		"demo-1.0-py3-none-any.whl":   demo,
		"helper-2.1-py3-none-any.whl": helper,
	}}
	f.src = &fakeSource{files: f.wheels}
	f.plan = f.planFor("demo", "1.0", "helper", "2.1")
	return f
}

func (f *libFixture) planFor(args ...string) Plan {
	var lock string
	var names []string
	for i := 0; i < len(args); i += 2 {
		file := args[i] + "-" + args[i+1] + "-py3-none-any.whl"
		lock += lockLine(args[i], args[i+1], f.wheels[file])
		names = append(names, file)
	}
	p, err := NewPlan([]byte(lock), names)
	if err != nil {
		panic(err)
	}
	return p
}

func waitState(t *testing.T, m *Manager, hash string, want Status) State {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := m.State(hash)
		if s.Status == want {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("Zustand %v, want %v (Grund %q)", s.Status, want, s.Reason)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func listDir(t *testing.T, dir string) []string {
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

// Ensure ist asynchron: solange der Abruf läuft, bleibt der Zustand
// „vorbereitend“ (der Pin bleibt pending), danach „bereit“.
func TestEnsureIsAsyncThenReady(t *testing.T) {
	f := newLibFixture(t, nil)
	f.src.gate = make(chan struct{})
	if s := f.m.State(f.plan.Lock.Hash); s.Status != StatusNone {
		t.Fatalf("Zustand vorab = %v", s.Status)
	}
	done := make(chan State)
	go func() { done <- f.m.Ensure(f.plan, f.src) }()
	select {
	case s := <-done:
		if s.Status != StatusPreparing {
			t.Fatalf("Ensure lieferte %v, want vorbereitend", s.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ensure blockiert, solange das Entpacken läuft")
	}
	if s := f.m.State(f.plan.Lock.Hash); s.Status != StatusPreparing {
		t.Fatalf("Zustand = %v", s.Status)
	}
	if _, err := os.Stat(filepath.Join(f.dir, f.plan.Lock.Hash)); err == nil {
		t.Fatal("libs/<hash> sichtbar, bevor die Vorbereitung fertig ist")
	}
	close(f.src.gate)
	waitState(t, f.m, f.plan.Lock.Hash, StatusReady)
	b, err := os.ReadFile(filepath.Join(f.m.Path(f.plan.Lock.Hash), "demo", "__init__.py"))
	if err != nil || string(b) != "A = 1\n" {
		t.Fatalf("demo/__init__.py = %q, %v", b, err)
	}
	if got := listDir(t, f.dir); len(got) != 1 || got[0] != f.plan.Lock.Hash {
		t.Fatalf("Inhalt von libs = %v, want nur den Lock-Hash (kein .tmp, kein Rest)", got)
	}
	assertNormalizedModes(t, f.m.Path(f.plan.Lock.Hash))
	if err := verifyDir(f.m.Path(f.plan.Lock.Hash), f.plan.Lock.Hash); err != nil {
		t.Fatalf("fertiges Verzeichnis besteht die Prüfung nicht: %v", err)
	}
}

// Invariante 5: derselbe Lock bei zwei Pins wird einmal vorbereitet.
func TestEnsureSameLockOnce(t *testing.T) {
	f := newLibFixture(t, nil)
	f.src.gate = make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); f.m.Ensure(f.plan, f.src) }()
	}
	wg.Wait()
	close(f.src.gate)
	waitState(t, f.m, f.plan.Lock.Hash, StatusReady)
	f.m.Ensure(f.plan, f.src)
	f.m.Ensure(f.plan, f.src)
	if n := f.m.PreparedCount(); n != 1 {
		t.Fatalf("PreparedCount = %d, want 1", n)
	}
	if n := f.src.fetchCount(); n != 2 {
		t.Fatalf("%d Abrufe, want 2 (ein Wheel je Eintrag, einmal)", n)
	}
}

// Invariante 5: ein neuer Lock ergibt ein neues Verzeichnis.
func TestNewLockNewDirectory(t *testing.T) {
	f := newLibFixture(t, nil)
	other := f.planFor("demo", "1.0")
	if other.Lock.Hash == f.plan.Lock.Hash {
		t.Fatal("Testfehler: gleicher Hash")
	}
	f.m.Ensure(f.plan, f.src)
	f.m.Ensure(other, f.src)
	waitState(t, f.m, f.plan.Lock.Hash, StatusReady)
	waitState(t, f.m, other.Lock.Hash, StatusReady)
	got := listDir(t, f.dir)
	want := []string{f.plan.Lock.Hash, other.Lock.Hash}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("libs = %v, want %v", got, want)
	}
	if f.m.PreparedCount() != 2 {
		t.Fatalf("PreparedCount = %d", f.m.PreparedCount())
	}
}

// Invariante 2: eine manipulierte Wheel-Datei ergibt libs_hash_mismatch; es
// entsteht kein libs/<hash> und nichts bleibt liegen.
func TestEnsureTamperedWheelFailsClosed(t *testing.T) {
	f := newLibFixture(t, nil)
	evil := append([]byte(nil), f.wheels["demo-1.0-py3-none-any.whl"]...)
	evil[len(evil)/2] ^= 0xff
	f.src.files["demo-1.0-py3-none-any.whl"] = evil
	f.m.Ensure(f.plan, f.src)
	s := waitState(t, f.m, f.plan.Lock.Hash, StatusFailed)
	if s.Reason != contract.ReasonLibsHashMismatch || s.Retry {
		t.Fatalf("Zustand = %+v", s)
	}
	if got := listDir(t, f.dir); len(got) != 0 {
		t.Fatalf("libs enthält nach dem Fehlschlag %v", got)
	}
	// Dauerhaft: eine Wiederholung ändert nichts und ruft nichts erneut ab.
	n := f.src.fetchCount()
	if s := f.m.Ensure(f.plan, f.src); s.Status != StatusFailed || s.Reason != contract.ReasonLibsHashMismatch {
		t.Fatalf("zweiter Ensure = %+v", s)
	}
	time.Sleep(50 * time.Millisecond)
	if f.src.fetchCount() != n {
		t.Fatal("dauerhafter Fehler wurde erneut versucht")
	}
}

func TestEnsureFailureReasons(t *testing.T) {
	zipSlip := zipOf(t, zent{name: "../evil.py", data: []byte("x")})
	tests := []struct {
		name  string
		wheel []byte
		mut   func(*Limits)
		want  string
	}{
		{"Zip-Slip", zipSlip, nil, contract.ReasonLibsInvalid},
		{"kein Zip", []byte("kein zip"), nil, contract.ReasonLibsInvalid},
		{"zu groß", wheelBytes(t, "demo", "1.0", map[string]string{"demo/big.bin": strings.Repeat("x", 5000)}),
			func(l *Limits) { l.MaxLockBytes = 1000 }, contract.ReasonLibsTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tempDir(t)
			m := newManager(t, dir, tt.mut)
			src := &fakeSource{files: map[string][]byte{"demo-1.0-py3-none-any.whl": tt.wheel}}
			p, err := NewPlan([]byte(lockLine("demo", "1.0", tt.wheel)), []string{"demo-1.0-py3-none-any.whl"})
			if err != nil {
				t.Fatal(err)
			}
			m.Ensure(p, src)
			s := waitState(t, m, p.Lock.Hash, StatusFailed)
			if s.Reason != tt.want || s.Retry {
				t.Fatalf("Zustand = %+v, want Grund %s", s, tt.want)
			}
			if got := listDir(t, dir); len(got) != 0 {
				t.Fatalf("Rückstand: %v", got)
			}
		})
	}
}

// Ein Fehler der Senke mit Grund (Hash-Fehler im Git-Objekt, Grenzen) wird
// durchgereicht, ein Lesefehler ohne Grund gilt als vorübergehend und wird
// nach der Wartezeit erneut versucht.
func TestEnsureSourceErrors(t *testing.T) {
	t.Run("Grund der Senke", func(t *testing.T) {
		f := newLibFixture(t, nil)
		f.src.err = &Error{Reason: contract.ReasonLibsTooLarge, Msg: "Wheel zu groß"}
		f.m.Ensure(f.plan, f.src)
		s := waitState(t, f.m, f.plan.Lock.Hash, StatusFailed)
		if s.Reason != contract.ReasonLibsTooLarge || s.Retry {
			t.Fatalf("Zustand = %+v", s)
		}
	})
	t.Run("vorübergehend", func(t *testing.T) {
		f := newLibFixture(t, nil)
		f.src.err = errors.New("E/A-Fehler")
		f.m.Ensure(f.plan, f.src)
		s := waitState(t, f.m, f.plan.Lock.Hash, StatusFailed)
		if !s.Retry {
			t.Fatalf("Zustand = %+v, want vorübergehend", s)
		}
		if got := listDir(t, f.dir); len(got) != 0 {
			t.Fatalf("Rückstand: %v", got)
		}
		f.src.mu.Lock()
		f.src.err = nil
		f.src.mu.Unlock()
		f.m.Ensure(f.plan, f.src)
		waitState(t, f.m, f.plan.Lock.Hash, StatusReady)
	})
	t.Run("Wartezeit vor dem nächsten Versuch", func(t *testing.T) {
		f := newLibFixture(t, nil)
		f.m.retryAfter = time.Hour
		f.src.err = errors.New("E/A-Fehler")
		f.m.Ensure(f.plan, f.src)
		waitState(t, f.m, f.plan.Lock.Hash, StatusFailed)
		n := f.src.fetchCount()
		f.m.Ensure(f.plan, f.src)
		time.Sleep(50 * time.Millisecond)
		if f.src.fetchCount() != n {
			t.Fatal("neuer Versuch vor Ablauf der Wartezeit")
		}
	})
}

// Grenze „gesamt“: bereits fertige Locks zählen mit.
func TestTotalLimitCountsOtherLocks(t *testing.T) {
	// Jeder Lock für sich (rund 111 und 113 Byte installiert) passt unter
	// 150 Byte, beide zusammen nicht.
	f := newLibFixture(t, func(l *Limits) { l.MaxTotalBytes = 150 })
	first, second := f.planFor("demo", "1.0"), f.planFor("helper", "2.1")
	f.m.Ensure(first, f.src)
	waitState(t, f.m, first.Lock.Hash, StatusReady)
	f.m.Ensure(second, f.src)
	s := waitState(t, f.m, second.Lock.Hash, StatusFailed)
	if s.Reason != contract.ReasonLibsTooLarge {
		t.Fatalf("Zustand = %+v", s)
	}
	if f.m.State(first.Lock.Hash).Status != StatusReady {
		t.Fatal("der erste Lock ist durch den Fehlschlag des zweiten verloren gegangen")
	}
}

// Invariante 2: ein abgebrochenes Entpacken wird nie sichtbar; .tmp und Reste
// werden beim Start entfernt, ein abweichendes Verzeichnis verworfen (B3).
func TestOpenCleansTmpStraysAndTampered(t *testing.T) {
	f := newLibFixture(t, nil)
	good := f.planFor("demo", "1.0")
	f.m.Ensure(good, f.src)
	waitState(t, f.m, good.Lock.Hash, StatusReady)
	tampered := f.planFor("helper", "2.1")
	f.m.Ensure(tampered, f.src)
	waitState(t, f.m, tampered.Lock.Hash, StatusReady)
	f.m.Close()

	// Zustand nach einem Absturz: halbes .tmp (mit nicht beschreibbaren
	// Ordnern), Wheel-Zwischendatei, Fremdes, und ein verändertes Verzeichnis.
	half := filepath.Join(f.dir, strings.Repeat("c", 64)+".tmp", "pkg", "sub")
	if err := os.MkdirAll(half, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(half, "a.py"), []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(half), filepath.Join(half, ".."), filepath.Join(half, "..", "..")} {
		if err := os.Chmod(p, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{strings.Repeat("d", 64) + ".wheel", "stray.txt", ".hidden", strings.Repeat("E", 64)} {
		if err := os.WriteFile(filepath.Join(f.dir, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(f.dir, "not-a-hash", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(tampered.Lock.Hash, "helper")
	if err := os.Chmod(filepath.Join(f.dir, evil), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, evil, "evil.pth"), []byte("import os"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(f.dir, evil), 0o555); err != nil {
		t.Fatal(err)
	}

	m := newManager(t, f.dir, nil)
	if got := listDir(t, f.dir); len(got) != 1 || got[0] != good.Lock.Hash {
		t.Fatalf("libs nach dem Start = %v, want nur %s", got, good.Lock.Hash)
	}
	if m.State(good.Lock.Hash).Status != StatusReady {
		t.Fatal("das intakte Verzeichnis ist nicht bereit")
	}
	if m.State(tampered.Lock.Hash).Status != StatusNone {
		t.Fatal("das veränderte Verzeichnis gilt als bereit")
	}
	// Ohne neue Vorbereitung ist ein verworfener Lock neu anlegbar.
	m.Ensure(tampered, f.src)
	waitState(t, m, tampered.Lock.Hash, StatusReady)
	if err := verifyDir(m.Path(tampered.Lock.Hash), tampered.Lock.Hash); err != nil {
		t.Fatal(err)
	}
}

func TestStateReadyRequiresDirectory(t *testing.T) {
	f := newLibFixture(t, nil)
	f.m.Ensure(f.plan, f.src)
	waitState(t, f.m, f.plan.Lock.Hash, StatusReady)
	if err := removeAll(f.m.Path(f.plan.Lock.Hash)); err != nil {
		t.Fatal(err)
	}
	if s := f.m.State(f.plan.Lock.Hash); s.Status == StatusReady {
		t.Fatal("bereit trotz fehlendem Verzeichnis")
	}
	// ... und wird bei Bedarf neu vorbereitet.
	f.m.Ensure(f.plan, f.src)
	waitState(t, f.m, f.plan.Lock.Hash, StatusReady)
}

// Aufräumen: nur, was nicht mehr referenziert ist.
func TestPruneRemovesOnlyUnreferenced(t *testing.T) {
	f := newLibFixture(t, nil)
	a, b := f.planFor("demo", "1.0"), f.planFor("helper", "2.1")
	for _, p := range []Plan{a, b} {
		f.m.Ensure(p, f.src)
		waitState(t, f.m, p.Lock.Hash, StatusReady)
	}
	f.m.Prune(map[string]bool{a.Lock.Hash: true})
	if got := listDir(t, f.dir); len(got) != 1 || got[0] != a.Lock.Hash {
		t.Fatalf("libs = %v, want nur %s", got, a.Lock.Hash)
	}
	if f.m.State(b.Lock.Hash).Status != StatusNone {
		t.Fatal("entfernter Lock gilt weiter als bereit")
	}
	f.m.Prune(map[string]bool{a.Lock.Hash: true})
	if f.m.State(a.Lock.Hash).Status != StatusReady {
		t.Fatal("referenzierter Lock wurde entfernt")
	}
}

func TestPruneKeepsPreparingLock(t *testing.T) {
	f := newLibFixture(t, nil)
	f.src.gate = make(chan struct{})
	f.m.Ensure(f.plan, f.src)
	f.m.Prune(map[string]bool{}) // nicht mehr referenziert, aber in Arbeit
	close(f.src.gate)
	s := waitState(t, f.m, f.plan.Lock.Hash, StatusReady)
	_ = s
	f.m.Prune(map[string]bool{})
	if got := listDir(t, f.dir); len(got) != 0 {
		t.Fatalf("libs = %v, want leer", got)
	}
}

// Ein URL-Wechsel leert libs.
func TestResetClearsEverything(t *testing.T) {
	f := newLibFixture(t, nil)
	f.m.Ensure(f.plan, f.src)
	waitState(t, f.m, f.plan.Lock.Hash, StatusReady)
	f.m.Reset()
	if got := listDir(t, f.dir); len(got) != 0 {
		t.Fatalf("libs = %v, want leer", got)
	}
	if f.m.State(f.plan.Lock.Hash).Status != StatusNone {
		t.Fatal("Zustand überlebt Reset")
	}
}

func TestCloseCancelsRunningPreparation(t *testing.T) {
	f := newLibFixture(t, nil)
	f.src.gate = make(chan struct{}) // wird nie geschlossen
	f.m.Ensure(f.plan, f.src)
	done := make(chan struct{})
	go func() { f.m.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close wartet auf einen blockierten Abruf")
	}
	if got := listDir(t, f.dir); len(got) != 0 {
		t.Fatalf("Rückstand nach Close: %v", got)
	}
}

// Ein Wheel-Abruf darf nur in die Zwischendatei schreiben: ein Source, der
// mehr liefert als erlaubt, wird abgewiesen, ohne dass es im Speicher landet.
func TestWheelFileLimitIsEnforcedOnTheSink(t *testing.T) {
	f := newLibFixture(t, func(l *Limits) { l.MaxWheelFileBytes = 10 })
	greedy := &greedySource{}
	f.m.Ensure(f.plan, greedy)
	s := waitState(t, f.m, f.plan.Lock.Hash, StatusFailed)
	if s.Reason != contract.ReasonLibsTooLarge {
		t.Fatalf("Zustand = %+v", s)
	}
}

// greedySource ignoriert max und schreibt zu viel.
type greedySource struct{}

func (greedySource) Fetch(_ context.Context, _ string, dst io.Writer, _ int64) error {
	_, err := io.Copy(dst, bytes.NewReader(bytes.Repeat([]byte("x"), 1000)))
	return err
}

// Ein URL-Wechsel bricht eine laufende Vorbereitung ab: Reset kehrt zurück,
// ohne dass der blockierte Abruf endet, und es bleibt nichts liegen.
func TestResetCancelsRunningPreparation(t *testing.T) {
	f := newLibFixture(t, nil)
	f.src.gate = make(chan struct{}) // wird nie geschlossen
	f.m.Ensure(f.plan, f.src)
	done := make(chan struct{})
	go func() { f.m.Reset(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Reset wartet auf einen blockierten Abruf")
	}
	if got := listDir(t, f.dir); len(got) != 0 {
		t.Fatalf("Rückstand nach Reset: %v", got)
	}
	if f.m.State(f.plan.Lock.Hash).Status != StatusNone {
		t.Fatal("Zustand überlebt Reset")
	}
}

// idSource macht aus der Test-Senke eine mit Inhalts-Identität (bei Git: die
// Blob-ID). Zwei Pins mit demselben Lock, aber verschiedenen Wheel-Dateien
// haben verschiedene Identitäten.
type idSource struct {
	*fakeSource
	id string
}

func (s idSource) Identity(file string) string { return s.id + "/" + file }

// ensureEventually ruft Ensure, bis der Zustand nicht mehr „vorbereitend“ ist.
func ensureEventually(t *testing.T, m *Manager, plan Plan, src Source) State {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := m.Ensure(plan, src)
		if st.Status != StatusPreparing && st.Status != StatusNone {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("Zustand bleibt %v", st.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Invariante 2 für Pins, die sich ein Lock teilen: Ist libs/<hash> schon da,
// wird die Wheel-Datei des zweiten Pins trotzdem gegen das Lock geprüft. Ein
// manipuliertes Wheel ergibt libs_hash_mismatch für diesen Pin, das fertige
// Verzeichnis und der gute Pin bleiben unberührt.
func TestEnsureChecksWheelsOfPinsSharingAPreparedLock(t *testing.T) {
	f := newLibFixture(t, nil)
	good := idSource{fakeSource: f.src, id: "gut"}
	evilFiles := map[string][]byte{}
	for k, v := range f.wheels {
		evilFiles[k] = append([]byte(nil), v...)
	}
	evilFiles["demo-1.0-py3-none-any.whl"][10] ^= 0xff
	evil := idSource{fakeSource: &fakeSource{files: evilFiles}, id: "boese"}

	if st := ensureEventually(t, f.m, f.plan, good); st.Status != StatusReady {
		t.Fatalf("guter Pin: %+v", st)
	}
	st := ensureEventually(t, f.m, f.plan, evil)
	if st.Status != StatusFailed || st.Reason != contract.ReasonLibsHashMismatch || st.Retry {
		t.Fatalf("manipulierter Pin: %+v, want Failed/libs_hash_mismatch", st)
	}
	if st := f.m.Ensure(f.plan, good); st.Status != StatusReady {
		t.Fatalf("guter Pin nach dem Fehlschlag des anderen: %+v", st)
	}
	if err := verifyDir(f.m.Path(f.plan.Lock.Hash), f.plan.Lock.Hash); err != nil {
		t.Fatalf("fertiges Verzeichnis berührt: %v", err)
	}
	if f.m.PreparedCount() != 1 {
		t.Fatalf("PreparedCount = %d", f.m.PreparedCount())
	}
	// Dauerhaft und ohne erneutes Lesen.
	n := evil.fetchCount()
	if st := f.m.Ensure(f.plan, evil); st.Status != StatusFailed {
		t.Fatalf("zweiter Versuch: %+v", st)
	}
	time.Sleep(30 * time.Millisecond)
	if evil.fetchCount() != n {
		t.Fatal("bekannter Fehler wurde erneut gelesen")
	}
}

// Umgekehrte Reihenfolge: Das manipulierte Wheel zuerst darf den Lock für den
// guten Pin nicht vergiften.
func TestEnsureTamperedFirstDoesNotPoisonLockForGoodPin(t *testing.T) {
	f := newLibFixture(t, nil)
	good := idSource{fakeSource: f.src, id: "gut"}
	evilFiles := map[string][]byte{}
	for k, v := range f.wheels {
		evilFiles[k] = append([]byte(nil), v...)
	}
	evilFiles["helper-2.1-py3-none-any.whl"][10] ^= 0xff
	evil := idSource{fakeSource: &fakeSource{files: evilFiles}, id: "boese"}

	if st := ensureEventually(t, f.m, f.plan, evil); st.Status != StatusFailed || st.Reason != contract.ReasonLibsHashMismatch {
		t.Fatalf("manipulierter Pin: %+v", st)
	}
	if got := listDir(t, f.dir); len(got) != 0 {
		t.Fatalf("libs = %v nach dem Fehlschlag", got)
	}
	if st := ensureEventually(t, f.m, f.plan, good); st.Status != StatusReady {
		t.Fatalf("guter Pin: %+v", st)
	}
	if st := f.m.Ensure(f.plan, evil); st.Status != StatusFailed || st.Reason != contract.ReasonLibsHashMismatch {
		t.Fatalf("manipulierter Pin danach: %+v", st)
	}
}

// Fail-closed beim Entfernen: Ein Lauf hält eine Leihe auf sein libs/<hash>.
// Aufräumen überspringt das Verzeichnis, ein Reset wartet auf das Ende des
// Laufs, und wer sich währenddessen oder danach leihen will, bekommt nichts —
// nie ein halb entferntes Verzeichnis.
func TestAcquireHoldsDirectoryAgainstPruneAndReset(t *testing.T) {
	f := newLibFixture(t, nil)
	hash := f.plan.Lock.Hash
	f.m.Ensure(f.plan, f.src)
	waitState(t, f.m, hash, StatusReady)
	release, ok := f.m.Acquire(hash)
	if !ok {
		t.Fatal("keine Leihe auf ein fertiges Verzeichnis")
	}
	f.m.Prune(map[string]bool{})
	if err := verifyDir(f.m.Path(hash), hash); err != nil {
		t.Fatalf("Aufräumen hat das verliehene Verzeichnis angefasst: %v", err)
	}
	resetDone := make(chan struct{})
	go func() { f.m.Reset(); close(resetDone) }()
	// Reset hat begonnen, sobald niemand mehr leihen kann.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if r, ok := f.m.Acquire(hash); !ok {
			break
		} else {
			r()
		}
		if time.Now().After(deadline) {
			t.Fatal("Reset hat nie begonnen")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-resetDone:
		t.Fatal("Reset hat nicht auf den laufenden Lauf gewartet")
	default:
	}
	if err := verifyDir(f.m.Path(hash), hash); err != nil {
		t.Fatalf("Reset hat das verliehene Verzeichnis angefasst: %v", err)
	}
	release()
	release() // doppeltes Freigeben schadet nicht
	select {
	case <-resetDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Reset endet nicht nach dem Ende des Laufs")
	}
	if got := listDir(t, f.dir); len(got) != 0 {
		t.Fatalf("libs = %v nach dem Reset", got)
	}
	if _, ok := f.m.Acquire(hash); ok {
		t.Fatal("Leihe auf ein entferntes Verzeichnis")
	}
}

func TestAcquireNeedsReadyDirectory(t *testing.T) {
	f := newLibFixture(t, nil)
	hash := f.plan.Lock.Hash
	if _, ok := f.m.Acquire(hash); ok {
		t.Fatal("Leihe ohne Verzeichnis")
	}
	f.m.Ensure(f.plan, f.src)
	waitState(t, f.m, hash, StatusReady)
	release, ok := f.m.Acquire(hash)
	if !ok {
		t.Fatal("keine Leihe")
	}
	release()
	f.m.Prune(map[string]bool{})
	if _, ok := f.m.Acquire(hash); ok {
		t.Fatal("Leihe nach dem Aufräumen")
	}
	// Von außen verschwunden: ebenfalls keine Leihe.
	f.m.Ensure(f.plan, f.src)
	waitState(t, f.m, hash, StatusReady)
	if err := removeAll(f.m.Path(hash)); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.m.Acquire(hash); ok {
		t.Fatal("Leihe auf ein von außen entferntes Verzeichnis")
	}
}
