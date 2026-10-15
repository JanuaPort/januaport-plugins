package libs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

// Source liefert die Bytes eines Wheels. Die Implementierung rechnet ihre
// eigene Integrität nach (die Senke den Git-Hash, M5) und meldet einen Fehler,
// wenn das nicht aufgeht; was dann schon in dst steht, wird verworfen. max
// begrenzt die Größe, die sie liefern darf.
type Source interface {
	Fetch(ctx context.Context, file string, dst io.Writer, max int64) error
}

// Identifier ist die optionale Erweiterung einer Source: Identity nennt eine
// Kennung des Inhalts einer Datei, die sich mit dem Inhalt ändert (bei Git die
// Blob-ID). Mit ihr merkt sich der Manager die SHA-256 jedes gelesenen Wheels
// und prüft auch die Wheels eines Pins, dessen Lock schon vorbereitet ist.
type Identifier interface {
	Identity(file string) string
}

// Status eines Locks im Manager.
type Status int

const (
	StatusNone      Status = iota // unbekannt oder verworfen
	StatusPreparing               // wird gerade geholt, entpackt oder geprüft
	StatusReady                   // libs/<hash> ist fertig und geprüft
	StatusFailed                  // Vorbereitung gescheitert
)

// State ist der Zustand eines Locks. Bei StatusFailed nennt Reason den Grund
// des Pins; Retry heißt: vorübergehender Fehler (Lesefehler), der nach einer
// Wartezeit erneut versucht wird. Ohne Retry ist der Fehler endgültig.
type State struct {
	Status Status
	Reason string
	Retry  bool
}

var hashDirRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// maxKnownSums begrenzt den Merkzettel der gelesenen Wheels.
const maxKnownSums = 8192

// Manager hält das Verzeichnis libs/: pro Lock-Hash ein fertiges,
// schreibgeschütztes Verzeichnis. Nur der vermittler schreibt dort; die
// Sandbox sieht es nur lesend. Ein Entpacken läuft asynchron und höchstens
// eines zugleich; es entsteht im privaten Staging als <hash>.tmp, wird mit fsync gesichert und
// dann atomar umbenannt, nie halb sichtbar.
//
// Fertige Verzeichnisse gehören zum Lock-Hash, Vorbereitungen und Fehler zum
// Plan (Lock plus die Identitäten der Wheel-Dateien eines Pins): Ein Pin mit
// manipuliertem Wheel scheitert, ohne dass ein anderer Pin mit demselben Lock
// und guten Wheels Schaden nimmt.
type Manager struct {
	dir        string
	staging    string // outside the sandbox mount, same filesystem as dir (#1096)
	lim        Limits
	log        *slog.Logger
	retryAfter time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	sem    chan struct{}

	mu       sync.Mutex
	dirs     map[string]*dirEntry // fertige Verzeichnisse
	jobs     map[string]*job      // Vorbereitungen und Fehler je Plan
	sums     map[string]string    // Identität → SHA-256 gelesener Wheels
	prepared int
	leases   map[string]int // laufende Läufe je Lock-Hash
	draining bool           // Reset läuft: keine neuen Leihen
	leaseCh  *sync.Cond
}

type dirEntry struct {
	bytes int64 // entpackte Bytes
}

type job struct {
	hash     string
	state    State
	failedAt time.Time
	cancel   context.CancelFunc
}

// Open übernimmt dir (muss existieren) und privates staging (gleicher Mount,
// außerhalb der Sandbox-Sicht). Beim Start werden Staging-Reste entfernt: alles,
// was kein fertiges <hash>-Verzeichnis ist (.tmp, Reste, Fremdes), wird
// entfernt, und jedes Verzeichnis wird einmal gegen sein Manifest geprüft
// (SEC B3); Abweichungen werden verworfen.
func Open(dir, staging string, lim Limits, log *slog.Logger) (*Manager, error) {
	if dir == "" || staging == "" {
		return nil, errors.New("libs: Bibliotheken und Staging erforderlich")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("libs: kein Ordner")
	}
	// Staging must be outside the visible library tree. Deployment places both
	// under one volume, so publication is a same-filesystem atomic rename.
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	staging, err = filepath.Abs(staging)
	if err != nil {
		return nil, err
	}
	if within(dir, staging) || within(staging, dir) {
		return nil, errors.New("libs: Staging und Bibliotheken überlappen")
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return nil, err
	}
	staged, err := os.ReadDir(staging)
	if err != nil {
		return nil, err
	}
	for _, de := range staged {
		if err := removeAll(filepath.Join(staging, de.Name())); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		dir: dir, staging: staging, lim: lim, log: log, retryAfter: 30 * time.Second,
		ctx: ctx, cancel: cancel, sem: make(chan struct{}, 1),
		dirs: map[string]*dirEntry{}, jobs: map[string]*job{}, sums: map[string]string{}, leases: map[string]int{},
	}
	kept, dropped := 0, 0
	des, err := os.ReadDir(dir)
	if err != nil {
		cancel()
		return nil, err
	}
	for _, de := range des {
		p := filepath.Join(dir, de.Name())
		if hashDirRe.MatchString(de.Name()) && de.IsDir() && verifyDir(p, de.Name()) == nil {
			m.dirs[de.Name()] = &dirEntry{bytes: installedBytes(p)}
			kept++
			continue
		}
		if err := removeAll(p); err != nil {
			m.log.Warn("libs: Rest nicht entfernt", "code", "libs_cleanup_failed")
		}
		dropped++
	}
	m.leaseCh = sync.NewCond(&m.mu)
	m.log.Info("libs geprüft", "bereit", kept, "verworfen", dropped)
	return m, nil
}

// installedBytes summiert die Dateigrößen laut Manifest (für die Grenze
// „gesamt“).
func installedBytes(root string) int64 {
	m, err := readManifest(root)
	if err != nil {
		return 0
	}
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}

// Path ist das Verzeichnis eines Locks.
func (m *Manager) Path(hash string) string { return filepath.Join(m.dir, hash) }

// PreparedCount zählt die erfolgreich vorbereiteten Locks seit dem Start
// (nicht die beim Start geprüften).
func (m *Manager) PreparedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.prepared
}

func (m *Manager) readyOnDisk(hash string) bool {
	info, err := os.Lstat(filepath.Join(m.Path(hash), manifestName))
	return err == nil && info.Mode().IsRegular()
}

// dirReady sagt, ob libs/<hash> fertig ist; ein verschwundenes Verzeichnis
// wird vergessen.
func (m *Manager) dirReady(hash string) bool {
	if _, ok := m.dirs[hash]; !ok {
		return false
	}
	if !m.readyOnDisk(hash) {
		delete(m.dirs, hash)
		return false
	}
	return true
}

// State sagt, wie es um den Lock steht. „Bereit“ gilt nur, solange das
// Verzeichnis noch da ist; sonst gilt eine laufende Vorbereitung vor einem
// Fehler. Der Zustand eines einzelnen Pins liefert Ensure.
func (m *Manager) State(hash string) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirReady(hash) {
		return State{Status: StatusReady}
	}
	var failed *State
	for _, j := range m.jobs {
		if j.hash != hash {
			continue
		}
		if j.state.Status == StatusPreparing {
			return j.state
		}
		failed = &j.state
	}
	if failed != nil {
		return *failed
	}
	return State{}
}

func mismatch() State {
	return State{Status: StatusFailed, Reason: contract.ReasonLibsHashMismatch}
}

// planKey unterscheidet die Pläne eines Locks: mit Identitäten der Dateien,
// sonst nur der Lock-Hash.
func planKey(plan Plan, idr Identifier) string {
	if idr == nil {
		return plan.Lock.Hash
	}
	var b strings.Builder
	b.WriteString(plan.Lock.Hash)
	for _, w := range plan.Wheels {
		b.WriteString("|" + w.File + "@" + idr.Identity(w.File))
	}
	return b.String()
}

// Ensure stößt die Vorbereitung an, falls nötig, und kehrt sofort zurück: Ein
// Lock wird einmal vorbereitet, gleichgültig, wie viele Pins ihn tragen. Ist
// libs/<hash> schon da, werden die Wheels dieses Pins trotzdem einmal gegen das
// Lock geprüft (nur mit Identifier). Ein endgültiger Fehler bleibt; ein
// vorübergehender wird nach der Wartezeit erneut versucht.
func (m *Manager) Ensure(plan Plan, src Source) State {
	hash := plan.Lock.Hash
	idr, _ := src.(Identifier)
	m.mu.Lock()
	defer m.mu.Unlock()
	key := planKey(plan, idr)
	// Solange der Auftrag läuft, meldet Ensure nichts anderes (er räumt noch auf).
	if j := m.jobs[key]; j != nil && j.state.Status == StatusPreparing {
		return j.state
	}
	known := true
	if idr != nil {
		for _, w := range plan.Wheels {
			sum, ok := m.sums[idr.Identity(w.File)]
			switch {
			case !ok:
				known = false
			case !slices.Contains(w.Entry.Hashes, sum):
				return mismatch()
			}
		}
	}
	if m.draining {
		return State{Status: StatusPreparing}
	}
	haveDir := m.dirReady(hash)
	if haveDir && known {
		return State{Status: StatusReady}
	}
	if j := m.jobs[key]; j != nil && (!j.state.Retry || time.Since(j.failedAt) < m.retryAfter) {
		return j.state
	}
	if m.ctx.Err() != nil {
		return State{Status: StatusFailed, Retry: true}
	}
	ctx, cancel := context.WithCancel(m.ctx)
	j := &job{hash: hash, state: State{Status: StatusPreparing}, cancel: cancel}
	m.jobs[key] = j
	m.wg.Add(1)
	go m.run(ctx, key, j, plan, src, haveDir)
	return j.state
}

func (m *Manager) run(ctx context.Context, key string, j *job, plan Plan, src Source, haveDir bool) {
	defer m.wg.Done()
	defer j.cancel()
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		m.finish(key, j, 0, ctx.Err(), false)
		return
	}
	var (
		n     int64
		files int
		err   error
	)
	start := time.Now()
	if haveDir {
		err = m.verifyWheels(ctx, plan, src)
	} else {
		n, files, err = m.build(ctx, plan, src)
		if err == nil {
			m.log.Info("libs vorbereitet", "wheels", len(plan.Wheels), "files", files, "bytes", n, "duration_ms", time.Since(start).Milliseconds())
		}
	}
	m.finish(key, j, n, err, !haveDir)
}

// finish trägt das Ergebnis ein, sofern der Auftrag noch derselbe ist (ein
// Reset in der Zwischenzeit hat ihn ersetzt).
func (m *Manager) finish(key string, j *job, n int64, err error, built bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.jobs[key] != j {
		return
	}
	if err == nil {
		delete(m.jobs, key)
		if built {
			m.dirs[j.hash] = &dirEntry{bytes: n}
			m.prepared++
		}
		return
	}
	var le *Error
	if errors.As(err, &le) {
		j.state = State{Status: StatusFailed, Reason: le.Reason}
		m.log.Warn("libs gescheitert", "reason", le.Reason)
	} else {
		j.state = State{Status: StatusFailed, Retry: true}
		m.log.Warn("libs gescheitert", "code", "libs_io")
	}
	j.failedAt = time.Now()
}

// limitedWriter schreibt höchstens left Bytes und zählt mit.
type limitedWriter struct {
	w    io.Writer
	left int64
	n    int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, tooLarge("Wheel-Datei über der Grenze")
	}
	n, err := l.w.Write(p)
	l.left -= int64(n)
	l.n += int64(n)
	return n, err
}

func (m *Manager) rememberSum(src Source, file, sum string) {
	idr, ok := src.(Identifier)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sums) >= maxKnownSums {
		m.sums = map[string]string{}
	}
	m.sums[idr.Identity(file)] = sum
}

// verifyWheels prüft die Wheels eines Pins gegen das Lock, ohne zu entpacken:
// libs/<hash> ist schon fertig, aber diese Dateien kennt der Manager noch nicht.
func (m *Manager) verifyWheels(ctx context.Context, plan Plan, src Source) error {
	idr, _ := src.(Identifier)
	for _, w := range plan.Wheels {
		if idr != nil {
			m.mu.Lock()
			_, known := m.sums[idr.Identity(w.File)]
			m.mu.Unlock()
			if known {
				continue
			}
		}
		h := sha256.New()
		sink := &limitedWriter{w: h, left: m.lim.MaxWheelFileBytes}
		if err := src.Fetch(ctx, w.File, sink, m.lim.MaxWheelFileBytes); err != nil {
			return err
		}
		sum := hex.EncodeToString(h.Sum(nil))
		m.rememberSum(src, w.File, sum)
		if !slices.Contains(w.Entry.Hashes, sum) {
			return &Error{Reason: contract.ReasonLibsHashMismatch, Msg: "SHA-256 des Wheels steht nicht im Lock"}
		}
	}
	return nil
}

// build holt jedes Wheel, prüft seinen SHA-256 gegen das Lock, entpackt es
// nach <hash>.tmp und benennt am Ende um. Bei jedem Fehler bleibt nichts
// zurück.
func (m *Manager) build(ctx context.Context, plan Plan, src Source) (installed int64, files int, err error) {
	hash := plan.Lock.Hash
	tmp := filepath.Join(m.staging, hash+".tmp")
	wheelTmp := filepath.Join(m.staging, hash+".wheel")
	published := false
	defer func() {
		_ = os.Remove(wheelTmp)
		if err != nil {
			_ = removeAll(tmp)
			if published {
				_ = removeAll(m.Path(hash))
			}
		}
	}()
	if err := removeAll(tmp); err != nil {
		return 0, 0, err
	}
	_ = os.Remove(wheelTmp)

	ex := newExtractor(tmp, m.lim, m.usedBytes(hash))
	for _, w := range plan.Wheels {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		if err := m.fetchAndExtract(ctx, ex, w, src, wheelTmp); err != nil {
			return 0, 0, err
		}
	}
	if err := ex.seal(hash); err != nil {
		return 0, 0, err
	}
	if err := verifyDir(tmp, hash); err != nil {
		return 0, 0, err
	}
	// Moving a directory across parents needs owner-write permission on it
	// to update '..' (also on the same filesystem). Keep it private, 0700,
	// until the already verified tree has reached its final name. The sandbox
	// UID cannot open it during this interval; readiness follows chmod+fsync.
	if err := os.Chmod(tmp, 0o700); err != nil {
		return 0, 0, err
	}
	if err := os.Rename(tmp, m.Path(hash)); err != nil {
		return 0, 0, err
	}
	published = true
	if err := os.Chmod(m.Path(hash), 0o555); err != nil {
		return 0, 0, err
	}
	if err := syncDir(m.Path(hash)); err != nil {
		return 0, 0, err
	}
	if err := syncDir(m.staging); err != nil {
		return 0, 0, err
	}
	if err := syncDir(m.dir); err != nil {
		return 0, 0, fmt.Errorf("libs: %w", err)
	}
	return ex.bytes, ex.files, nil
}

func (m *Manager) fetchAndExtract(ctx context.Context, ex *extractor, w PlannedWheel, src Source, wheelTmp string) error {
	f, err := os.OpenFile(wheelTmp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(wheelTmp) }()
	h := sha256.New()
	sink := &limitedWriter{w: io.MultiWriter(f, h), left: m.lim.MaxWheelFileBytes}
	if err := src.Fetch(ctx, w.File, sink, m.lim.MaxWheelFileBytes); err != nil {
		return err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	m.rememberSum(src, w.File, sum)
	if !slices.Contains(w.Entry.Hashes, sum) {
		return &Error{Reason: contract.ReasonLibsHashMismatch, Msg: "SHA-256 des Wheels steht nicht im Lock"}
	}
	return ex.addWheel(f, sink.n)
}

// usedBytes sind die entpackten Bytes aller fertigen Locks außer hash.
func (m *Manager) usedBytes(except string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for h, d := range m.dirs {
		if h != except {
			n += d.bytes
		}
	}
	return n
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// Prune entfernt, was keiner der Hashes in keep mehr referenziert. Ein Lock in
// Arbeit bleibt.
func (m *Manager) Prune(keep map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	busy := map[string]bool{}
	for key, j := range m.jobs {
		if j.state.Status == StatusPreparing {
			busy[j.hash] = true
		} else if !keep[j.hash] {
			delete(m.jobs, key)
		}
	}
	for hash := range m.dirs {
		if keep[hash] || busy[hash] || m.leases[hash] > 0 || m.draining {
			continue
		}
		if err := removeAll(m.Path(hash)); err != nil {
			m.log.Warn("libs: Aufräumen gescheitert", "code", "libs_cleanup_failed")
			continue
		}
		delete(m.dirs, hash)
	}
}

// Reset leert libs: Laufende Vorbereitungen werden abgebrochen, alles wird
// entfernt (Wechsel der Repository-URL). Verliehene Verzeichnisse (Acquire) bleiben,
// bis ihr Lauf endet; bis dahin gibt es keine neue Leihe.
func (m *Manager) Reset() {
	m.mu.Lock()
	m.draining = true // ab jetzt keine neue Leihe
	for _, j := range m.jobs {
		j.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	// Läufe, die ihr Verzeichnis halten, enden zuerst (höchstens ihre Wandzeit).
	for len(m.leases) > 0 {
		m.leaseCh.Wait()
	}
	m.dirs, m.jobs, m.sums = map[string]*dirEntry{}, map[string]*job{}, map[string]string{}
	des, err := os.ReadDir(m.dir)
	if err == nil {
		for _, de := range des {
			if err := removeAll(filepath.Join(m.dir, de.Name())); err != nil {
				m.log.Warn("libs: Aufräumen gescheitert", "code", "libs_cleanup_failed")
			}
		}
	}
	m.draining = false
}

// Close bricht laufende Vorbereitungen ab und wartet auf sie. Mehrfaches
// Schließen ist zulässig.
func (m *Manager) Close() {
	m.cancel()
	m.wg.Wait()
}

// Acquire leiht libs/<hash> für einen Lauf aus (SEC R4, fail-closed beim
// Entfernen): Solange die Leihe besteht, fasst weder Aufräumen noch Reset das
// Verzeichnis an. Gibt es das fertige Verzeichnis nicht (mehr) oder läuft ein
// Reset, gibt es keine Leihe — der Lauf startet dann nicht, statt mit einem
// teilweisen Baum zu laufen. Prüfung und Eintragen sind ein Schritt unter dem
// Schloss, das auch das Entfernen hält.
func (m *Manager) Acquire(hash string) (release func(), ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.draining || !m.dirReady(hash) {
		return nil, false
	}
	m.leases[hash]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.leases[hash]--; m.leases[hash] <= 0 {
				delete(m.leases, hash)
			}
			m.leaseCh.Broadcast()
		})
	}, true
}
