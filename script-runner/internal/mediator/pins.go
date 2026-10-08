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

// refresh liest runtime-key und pins.json, meldet dem abholer die
// gewünschten Commits und bewertet jeden Pin. Die Dateien sind klein; sie
// werden bei jedem Durchlauf gelesen, das deckt „bei jeder Änderung“ und
// „alle 60 s“ aus Nachtrag 2a ab.
func (m *Mediator) refresh(ctx context.Context) {
	key := readKey(filepath.Join(m.cfg.KeyDir, "runtime-key"))
	var f pins.File
	if b, err := os.ReadFile(filepath.Join(m.cfg.KeyDir, "pins.json")); err == nil {
		if parsed, err := pins.Parse(b); err == nil {
			f = parsed
		}
	}
	m.writeWanted(f)

	m.mu.Lock()
	if f.RepoURL != m.repoURL {
		m.cache = map[cacheKey]verdict{}
		m.repoURL = f.RepoURL
	}
	cache := m.cache
	m.mu.Unlock()

	states := make([]pinState, 0, len(f.Pins))
	for _, p := range f.Pins {
		states = append(states, pinState{pin: p, verdict: m.evaluate(ctx, p, cache)})
	}

	m.mu.Lock()
	m.key = key
	m.pins = states
	m.mu.Unlock()
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
	k := cacheKey{p.Name, p.Path, p.Commit}
	m.mu.Lock()
	v, hit := cache[k]
	m.mu.Unlock()
	if hit {
		return v
	}
	has, err := m.store.HasCommit(ctx, p.Commit)
	if err != nil || !has {
		if _, err := os.Stat(filepath.Join(m.cfg.StoreDir, "failed", p.Commit)); err == nil {
			return verdict{state: contract.StateInvalid, reason: contract.ReasonFetchFailed}
		}
		return verdict{state: contract.StatePending}
	}
	v = m.verify(ctx, p)
	if ctx.Err() == nil {
		m.mu.Lock()
		cache[k] = v
		m.mu.Unlock()
	}
	return v
}

// verify prüft den Pin an der Senke: Baum, Manifest, Einstieg.
func (m *Mediator) verify(ctx context.Context, p pins.Pin) verdict {
	tree, err := m.store.Load(ctx, p.Commit, p.Path)
	if err != nil {
		return invalidVerdict(err)
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
	return verdict{state: contract.StateReady, manifest: &man, manifestSHA: mf.BlobID}
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
	for sha := range want {
		p := filepath.Join(dir, sha)
		if _, err := os.Stat(p); err != nil {
			_ = os.WriteFile(p, nil, 0o644)
		}
	}
	writeIfChanged(filepath.Join(m.cfg.StoreDir, "repo_url"), f.RepoURL+"\n")
}

func writeIfChanged(path, content string) {
	if b, err := os.ReadFile(path); err == nil && string(b) == content {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, []byte(content), 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

// readyPin liefert einen Pin für einen Lauf.
func (m *Mediator) findPin(name string) (pinState, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
