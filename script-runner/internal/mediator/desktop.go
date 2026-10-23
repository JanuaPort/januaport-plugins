package mediator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/manifest"
	"github.com/januaport/januaport-plugins/script-runner/internal/pins"
	"github.com/januaport/januaport-plugins/script-runner/internal/store"
)

type desktopState struct {
	pin pins.DesktopPin
	verdict
}

func (p desktopState) listed() contract.DesktopPinStatus {
	s := contract.DesktopPinStatus{Name: p.pin.Name, State: p.state, Reason: p.reason, DeclaredTools: []string{}}
	if p.state == contract.StateReady && p.manifest != nil {
		s.DeclaredTools = p.manifest.Tools
		s.TimeoutS = p.manifest.TimeoutS
		s.InputSchema = p.manifest.InputSchema
		s.Description = p.manifest.Description
	}
	return s
}

// readPinFiles binds both pin lists to the one fetcher repository. A separate
// desktop-only installation works without pins.json. Conflicting repositories
// fail closed for desktop pins instead of approving code from the other store.
func (m *Mediator) readPinFiles() (pins.File, pins.DesktopFile, bool) {
	var f pins.File
	var d pins.DesktopFile
	parsedOK := false
	if b, err := os.ReadFile(filepath.Join(m.cfg.KeyDir, "pins.json")); err == nil {
		if p, err := pins.Parse(b); err == nil {
			f = p
			parsedOK = true
		}
	}
	if b, err := readLimited(filepath.Join(m.cfg.KeyDir, "desktop-pins.json"), 1<<20); err == nil {
		if p, err := pins.ParseDesktop(b); err == nil {
			d = p
		}
	}
	if f.RepoURL == "" {
		f.RepoURL = d.RepoURL
	}
	if d.RepoURL != f.RepoURL {
		for i := range d.Pins {
			d.Pins[i].Invalid = contract.ReasonManifestInvalid
		}
	}
	return f, d, parsedOK
}

func (m *Mediator) evaluateDesktop(ctx context.Context, p pins.DesktopPin, cache map[cacheKey]verdict) verdict {
	if p.Invalid != "" {
		return verdict{state: contract.StateInvalid, reason: p.Invalid}
	}
	if v := m.fetchVerdict(p.Commit); v.state != contract.StateReady {
		return v
	}
	k := cacheKey{p.Name, p.Path, p.Commit}
	m.mu.Lock()
	v, hit := cache[k]
	m.mu.Unlock()
	if hit {
		return v
	}
	_, tree, err := m.store.DesktopPackage(ctx, p.Commit, p.Path, p.Algo)
	if errors.Is(err, store.ErrDesktopTooLarge) {
		v = verdict{state: contract.StateInvalid, reason: contract.ReasonManifestInvalid}
	} else if err != nil {
		v = invalidVerdict(err)
	} else {
		v = verifyDesktopManifest(tree, p.Name)
	}
	if ctx.Err() == nil && v.state != contract.StatePending {
		m.mu.Lock()
		cache[k] = v
		m.mu.Unlock()
	}
	return v
}

func verifyDesktopManifest(tree store.DesktopTree, name string) verdict {
	f := tree.File(manifest.DesktopFileName)
	if f == nil {
		return verdict{state: contract.StateInvalid, reason: contract.ReasonManifestMissing}
	}
	man, err := manifest.ParseDesktop(f.Data, name)
	if err != nil {
		return invalidVerdict(err)
	}
	if tree.File(man.Entry) == nil || tree.File(man.Python) == nil || man.UsesLibs() && tree.File(manifest.RequirementsFile) == nil {
		return verdict{state: contract.StateInvalid, reason: contract.ReasonManifestInvalid}
	}
	// No call to the Linux libs adapter: wheel/runtime bytes remain opaque.
	return verdict{state: contract.StateReady, manifest: &man.Manifest, manifestSHA: f.BlobID, verified: true}
}

// Caller holds m.mu. A name in desktop-pins.json reserves that name against
// the second execution path through tools/call, even for an invalid pin.
func (m *Mediator) desktopNameLocked(name string) bool {
	for _, p := range m.desktopPins {
		if p.pin.Name == name {
			return true
		}
	}
	return false
}

func (m *Mediator) authorizedDesktop(commit, path string) (pins.DesktopPin, string, bool) {
	f, d, _ := m.readPinFiles()
	for _, p := range d.Pins {
		if p.Invalid == "" && p.Commit == commit && p.Path == path && m.fetchVerdictFor(commit, f.RepoURL).state == contract.StateReady {
			return p, f.RepoURL, true
		}
	}
	return pins.DesktopPin{}, "", false
}

func (m *Mediator) desktopPackage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q, err := parseDesktopQuery(r)
	if err != nil {
		desktopError(w, 404)
		return
	}
	p, repo, ok := m.authorizedDesktop(q[0], q[1])
	if !ok {
		desktopError(w, 404)
		return
	}
	b, tree, err := m.store.DesktopPackage(r.Context(), p.Commit, p.Path, p.Algo)
	if errors.Is(err, store.ErrDesktopTooLarge) {
		desktopError(w, 413)
		return
	}
	if err != nil || verifyDesktopManifest(tree, p.Name).state != contract.StateReady {
		desktopError(w, 404)
		return
	}
	// Recheck pin file and approval after the potentially expensive tree walk.
	current, currentRepo, ok := m.authorizedDesktop(p.Commit, p.Path)
	if !ok || currentRepo != repo || current.Algo != p.Algo || current.Name != p.Name {
		desktopError(w, 404)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.jnpt.gitobjects.v1")
	_, _ = w.Write(b)
}

func parseDesktopQuery(r *http.Request) ([2]string, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	// ParseQuery also rejects malformed escaping and semicolon separators.
	if r.URL.RawQuery == "" {
		err = errors.New("query")
	}
	if len(q) != 2 || len(q["commit"]) != 1 || len(q["path"]) != 1 {
		err = errors.New("query")
	}
	return [2]string{q.Get("commit"), q.Get("path")}, err
}

func desktopError(w http.ResponseWriter, status int) {
	class, message := "not_found", "Der Auftrag oder das Paket wurde nicht gefunden."
	if status == 413 {
		class, message = "too_large", "Die Anfrage überschreitet die zulässige Größe."
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error struct {
			Class   string `json:"class"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Class   string `json:"class"`
		Message string `json:"message"`
	}{class, message}})
}
