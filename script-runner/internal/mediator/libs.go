package mediator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/libs"
	"github.com/januaport/januaport-plugins/script-runner/internal/manifest"
	"github.com/januaport/januaport-plugins/script-runner/internal/pins"
	"github.com/januaport/januaport-plugins/script-runner/internal/store"
)

// libsVerdict bewertet einen Pin mit `requirements` (#1041): Lock und
// Wheel-Namen werden sofort geprüft (Format, Plattform, 1:1), die Wheels
// selbst holt und entpackt der Manager asynchron. Bis libs/<lockhash> fertig
// ist, bleibt der Pin pending; jede Abweichung ist ein endgültiger
// invalid-Grund, nie ein Werkzeug.
func (m *Mediator) libsVerdict(ctx context.Context, p pins.Pin, tree store.Tree, man manifest.Manifest, manifestSHA string) verdict {
	base := verdict{manifest: &man, manifestSHA: manifestSHA, verified: true}
	fail := func(reason string) verdict {
		base.manifest = nil
		return verdict{state: contract.StateInvalid, reason: reason, verified: true}
	}
	lock := tree.File(manifest.RequirementsFile)
	if lock == nil || m.libs == nil {
		return fail(contract.ReasonLibsInvalid)
	}
	refs, err := m.store.ListWheels(ctx, p.Commit, p.Path)
	if err != nil {
		if v := invalidVerdict(err); v.state == contract.StateInvalid {
			return fail(v.reason)
		}
		base.state, base.transient = contract.StatePending, true
		return base
	}
	names := make([]string, 0, len(refs))
	blobs := make(map[string]string, len(refs))
	for _, r := range refs {
		names = append(names, r.Name)
		blobs[r.Name] = r.BlobID
	}
	plan, err := libs.NewPlan(lock.Data, names)
	if err != nil {
		return fail(libsReason(err))
	}
	base.libs = plan.Lock.Hash
	switch st := m.libs.Ensure(plan, storeSource{s: m.store, blobs: blobs}); st.Status {
	case libs.StatusReady:
		base.state = contract.StateReady
	case libs.StatusFailed:
		if st.Retry {
			base.state, base.transient = contract.StatePending, true
		} else {
			v := fail(st.Reason)
			v.libs = base.libs
			return v
		}
	default:
		base.state, base.transient = contract.StatePending, true
	}
	return base
}

func libsReason(err error) string {
	var le *libs.Error
	if errors.As(err, &le) {
		return le.Reason
	}
	return contract.ReasonLibsInvalid
}

// libsStillReady: ein gecachtes ready mit Bibliotheken gilt nur, solange das
// Verzeichnis da ist; sonst wird neu bewertet (und neu vorbereitet).
func (m *Mediator) libsStillReady(v verdict) bool {
	if v.libs == "" || v.state != contract.StateReady {
		return true
	}
	return m.libs != nil && m.libs.State(v.libs).Status == libs.StatusReady
}

// libsReady sagt, ob für den Lauf alles bereit ist: Das Lock im Baum ist das
// geprüfte, und libs/<hash> ist fertig.
func (m *Mediator) libsReady(p pinState, tree store.Tree) bool {
	if p.libs == "" {
		return true
	}
	lock := tree.File(manifest.RequirementsFile)
	if lock == nil || m.libs == nil {
		return false
	}
	sum := sha256.Sum256(lock.Data)
	return hex.EncodeToString(sum[:]) == p.libs && m.libs.State(p.libs).Status == libs.StatusReady
}

// loadTree liest den Pin-Ordner für einen Lauf: mit Bibliotheken ohne
// wheels/, sonst wie bisher.
func (m *Mediator) loadTree(ctx context.Context, p pinState) (store.Tree, error) {
	if p.manifest != nil && p.manifest.UsesLibs() {
		return m.store.LoadSkippingWheels(ctx, p.pin.Commit, p.pin.Path)
	}
	return m.store.Load(ctx, p.pin.Commit, p.pin.Path)
}

// resetLibsOnURLChange leert libs bei einem Wechsel der Repository-URL. Nur
// eine gültige pins.json zählt; der erste Abgleich nach dem Start ist kein
// Wechsel (libs überlebt den Neustart; was nicht mehr referenziert ist, räumt
// pruneLibs auf).
func (m *Mediator) resetLibsOnURLChange(f pins.File, parsedOK bool) {
	if !parsedOK || f.RepoURL == "" {
		return
	}
	m.mu.Lock()
	changed := m.libsURL != "" && m.libsURL != f.RepoURL
	m.libsURL = f.RepoURL
	m.mu.Unlock()
	if changed && m.libs != nil {
		m.libs.Reset()
	}
}

// pruneLibs räumt auf, was kein Pin mehr referenziert — aber nur, wenn die
// Lage vollständig bekannt ist: jeder Pin hat sein Manifest gelesen oder ist
// ungültig, pins.json war lesbar, und es läuft gerade kein Skript.
func (m *Mediator) pruneLibs(states []pinState) {
	if m.libs == nil || m.activeRuns.Load() > 0 {
		return
	}
	keep := map[string]bool{}
	for _, s := range states {
		if !s.verified && s.state != contract.StateInvalid {
			return
		}
		if s.libs != "" {
			keep[s.libs] = true
		}
	}
	m.libs.Prune(keep)
}

// storeSource liest Wheels aus der Senke. Die Blob-IDs stammen aus dem
// nachgerechneten Baum; CopyBlob rechnet den Hash des Inhalts nach (M5).
type storeSource struct {
	s     *store.Store
	blobs map[string]string
}

func (s storeSource) Fetch(ctx context.Context, file string, dst io.Writer, max int64) error {
	id, ok := s.blobs[file]
	if !ok {
		return &libs.Error{Reason: contract.ReasonLibsInvalid, Msg: "Wheel nicht im Plan"}
	}
	_, err := s.s.CopyBlob(ctx, id, dst, max)
	var si *store.Invalid
	if errors.As(err, &si) {
		return &libs.Error{Reason: si.Reason, Msg: si.Msg}
	}
	return err
}

// Identity ist die Blob-ID des Wheels: Sie ändert sich mit dem Inhalt. Damit
// merkt sich der Manager die SHA-256 jedes gelesenen Wheels und prüft auch Pins,
// deren Lock schon vorbereitet ist.
func (s storeSource) Identity(file string) string { return s.blobs[file] }
