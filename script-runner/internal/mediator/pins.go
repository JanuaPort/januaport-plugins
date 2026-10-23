package mediator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
	"github.com/januaport/januaport-plugins/script-runner/internal/manifest"
	"github.com/januaport/januaport-plugins/script-runner/internal/pins"
	"github.com/januaport/januaport-plugins/script-runner/internal/store"
)

// pinState ist ein Pin mit seinem Zustand (Vertrag §2).
type pinState struct {
	pin pins.Pin
	verdict
}

// verdict ist das Ergebnis der Prüfung an der Senke für (Name, Pfad, Commit).
type verdict struct {
	state       string
	reason      string
	manifest    *manifest.Manifest
	manifestSHA string
	// libs ist der Lock-Hash der Bibliotheken (#1041), solange sie der Pin
	// verlangt und das Lock lesbar ist. verified: das Manifest wurde gelesen,
	// libs ist also bekannt. transient: nicht cachen, der nächste Abgleich
	// fragt neu (Vorbereitung läuft oder Lesefehler).
	libs      string
	verified  bool
	transient bool
}

type cacheKey struct{ name, path, commit string }

func (p pinState) listed() contract.ListedPin {
	lp := contract.ListedPin{Name: p.pin.Name, Commit: p.pin.Commit, State: p.state, Reason: p.reason}
	if p.state == contract.StateReady && p.manifest != nil {
		lp.Description = p.manifest.Description
		lp.InputSchema = p.manifest.InputSchema
		lp.Script = &contract.ScriptMeta{
			Path: p.pin.Path, DeclaredTools: p.manifest.Tools,
			TimeoutS: p.manifest.TimeoutS, ManifestSHA: p.manifestSHA,
		}
	}
	return lp
}

// refresh liest runtime-key und beide Pin-Dateien, meldet dem abholer die
// gewünschten Commits und bewertet jeden Pin. Die Dateien sind klein; sie
// werden bei jedem Durchlauf gelesen, das deckt „bei jeder Änderung“ und
// „alle 60 s“ aus Nachtrag 2a ab.
func (m *Mediator) refresh(ctx context.Context) {
	key := readKey(filepath.Join(m.cfg.KeyDir, "runtime-key"))
	f, desktop, parsedOK := m.readPinFiles()
	wanted := pins.File{RepoURL: f.RepoURL, Pins: append([]pins.Pin(nil), f.Pins...)}
	for _, p := range desktop.Pins {
		wanted.Pins = append(wanted.Pins, p.Pin)
	}
	m.writeWanted(wanted)

	m.mu.Lock()
	if f.RepoURL != m.repoURL {
		m.cache = map[cacheKey]verdict{}
		m.desktopCache = map[cacheKey]verdict{}
		m.repoURL = f.RepoURL
	}
	cache := m.cache
	desktopCache := m.desktopCache
	m.mu.Unlock()
	m.resetLibsOnURLChange(f, parsedOK)

	states := make([]pinState, 0, len(f.Pins))
	for _, p := range f.Pins {
		for _, d := range desktop.Pins {
			if d.Name == p.Name {
				p.Invalid = contract.ReasonManifestInvalid
			}
		}
		states = append(states, pinState{pin: p, verdict: m.evaluate(ctx, p, cache)})
	}
	desktopStates := make([]desktopState, 0, len(desktop.Pins))
	for _, p := range desktop.Pins {
		desktopStates = append(desktopStates, desktopState{pin: p, verdict: m.evaluateDesktop(ctx, p, desktopCache)})
	}

	m.mu.Lock()
	m.key = key
	m.pins = states
	m.desktopPins = desktopStates
	m.mu.Unlock()
	if parsedOK {
		m.pruneLibs(states)
	}
}

func readKey(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (m *Mediator) evaluate(ctx context.Context, p pins.Pin, cache map[cacheKey]verdict) verdict {
	if p.Invalid != "" {
		return verdict{state: contract.StateInvalid, reason: p.Invalid}
	}
	// L3 gilt vor dem Cache: ein vorhandenes Objekt und ein früheres ready
	// überleben weder eine abgewiesene Vorfahr-Prüfung noch eine fehlende Freigabe.
	if v := m.fetchVerdict(p.Commit); v.state != contract.StateReady {
		return v
	}
	k := cacheKey{p.Name, p.Path, p.Commit}
	m.mu.Lock()
	v, hit := cache[k]
	m.mu.Unlock()
	if hit && m.libsStillReady(v) {
		return v
	}
	has, err := m.store.HasCommit(ctx, p.Commit)
	if err != nil || !has {
		return verdict{state: contract.StatePending}
	}
	v = m.verify(ctx, p)
	if ctx.Err() == nil && !v.transient {
		m.mu.Lock()
		cache[k] = v
		m.mu.Unlock()
	}
	return v
}

// fetchVerdict liest die Freigabe des abholers ohne Cache. Fehler gehen
// vor Freigaben; leere alte failed-Marken bedeuten weiterhin fetch_failed.
// Die repo_url-Bindung verhindert Freigaben aus dem vorherigen Repository.
func (m *Mediator) fetchVerdict(sha string) verdict {
	m.mu.Lock()
	url := m.repoURL
	m.mu.Unlock()
	return m.fetchVerdictFor(sha, url)
}

func (m *Mediator) fetchVerdictFor(sha, url string) verdict {
	b, err := os.ReadFile(filepath.Join(m.cfg.StoreDir, "failed", sha))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		reason := contract.ReasonFetchFailed
		if string(b) == contract.ReasonNotOnDefaultBranch {
			reason = contract.ReasonNotOnDefaultBranch
		}
		return verdict{state: contract.StateInvalid, reason: reason}
	}
	b, err = os.ReadFile(filepath.Join(m.cfg.StoreDir, "approved", sha))
	if err != nil || url == "" || string(b) != url {
		return verdict{state: contract.StatePending}
	}
	return verdict{state: contract.StateReady}
}

// verify prüft den Pin an der Senke: Baum, Manifest, Einstieg und, mit
// `requirements`, die Bibliotheken. Ein Ordner wheels/ gehört nur dann nicht
// zum Baum, wenn das Manifest `requirements` trägt; sonst verhält sich der Pin
// wie bisher (Altbestand).
func (m *Mediator) verify(ctx context.Context, p pins.Pin) verdict {
	tree, err := m.store.LoadSkippingWheels(ctx, p.Commit, p.Path)
	if err != nil {
		return invalidVerdict(err)
	}
	if tree.HasWheelsDir && !wantsLibs(tree, p.Name) {
		if tree, err = m.store.Load(ctx, p.Commit, p.Path); err != nil {
			return invalidVerdict(err)
		}
	}
	mf := tree.File(manifest.FileName)
	if mf == nil {
		return verdict{state: contract.StateInvalid, reason: contract.ReasonManifestMissing}
	}
	man, err := manifest.Parse(mf.Data, p.Name)
	if err != nil {
		return invalidVerdict(err)
	}
	if tree.File(man.Entry) == nil {
		return verdict{state: contract.StateInvalid, reason: contract.ReasonManifestInvalid}
	}
	if man.UsesLibs() {
		return m.libsVerdict(ctx, p, tree, man, mf.BlobID)
	}
	return verdict{state: contract.StateReady, manifest: &man, manifestSHA: mf.BlobID, verified: true}
}

// wantsLibs sagt, ob das Manifest im Baum gültig ist und `requirements` trägt.
func wantsLibs(tree store.Tree, pinName string) bool {
	mf := tree.File(manifest.FileName)
	if mf == nil {
		return false
	}
	man, err := manifest.Parse(mf.Data, pinName)
	return err == nil && man.UsesLibs()
}

func invalidVerdict(err error) verdict {
	var si *store.Invalid
	if errors.As(err, &si) {
		return verdict{state: contract.StateInvalid, reason: si.Reason}
	}
	var mi *manifest.Invalid
	if errors.As(err, &mi) {
		return verdict{state: contract.StateInvalid, reason: mi.Reason}
	}
	// Lesefehler von git: später erneut versuchen.
	return verdict{state: contract.StatePending}
}

// writeWanted meldet dem abholer repo_url und die gewünschten Commits
// (Nachtrag 2a). Nur gültige Zeilen; alles andere in wanted/ wird entfernt.
// Neue Wunschdateien erst nach der atomaren Veröffentlichung von repo_url:
// Der abholer kann bereits auf die erste sichtbare Wunschdatei reagieren.
func (m *Mediator) writeWanted(f pins.File) {
	dir := filepath.Join(m.cfg.StoreDir, "wanted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	want := map[string]bool{}
	for _, p := range f.Pins {
		if p.Invalid == "" && gitref.ValidCommit(p.Commit) {
			want[p.Commit] = true
		}
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !want[e.Name()] {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	if err := writeIfChanged(filepath.Join(m.cfg.StoreDir, "repo_url"), f.RepoURL+"\n"); err != nil {
		return
	}
	for sha := range want {
		p := filepath.Join(dir, sha)
		if _, err := os.Stat(p); err != nil {
			_ = os.WriteFile(p, nil, 0o644)
		}
	}
}

func writeIfChanged(path, content string) error {
	if b, err := os.ReadFile(path); err == nil && string(b) == content {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// findPin liefert ausschließlich einen lokalen Skript-Pin für einen Lauf.
func (m *Mediator) findPin(name string) (pinState, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.desktopNameLocked(name) {
		return pinState{}, m.isolation, false
	}
	for _, p := range m.pins {
		if p.pin.Name == name && p.pin.Invalid == "" {
			return p, m.isolation, true
		}
	}
	return pinState{}, m.isolation, false
}

// forget verwirft das Prüfergebnis eines Pins; der nächste Abgleich prüft
// neu.
func (m *Mediator) forget(p pins.Pin) {
	m.mu.Lock()
	delete(m.cache, cacheKey{p.Name, p.Path, p.Commit})
	m.mu.Unlock()
}
