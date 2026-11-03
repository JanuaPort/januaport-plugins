package mediator

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
	"github.com/januaport/januaport-plugins/script-runner/internal/pins"
	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
	"github.com/januaport/januaport-plugins/script-runner/internal/store"
)

func (m *Mediator) pinSource(p pinState) *store.Store {
	if !p.storage {
		return m.store
	}
	return store.OpenStorage(filepath.Join(m.cfg.StoreDir, "speicher"), storagepins.Pin{Name: p.pin.Name, Commit: p.pin.Commit, Path: p.pin.Path, Algo: "sha256"}, m.cfg.Now)
}

func (m *Mediator) pinApproval(p pinState) verdict {
	if !p.storage {
		return m.fetchVerdict(p.pin.Commit)
	}
	if e := m.pinSource(p).CheckStorage(); e != nil {
		return invalidVerdict(e)
	}
	return verdict{state: contract.StateReady}
}

// storageStates exposes the verified snapshot using the existing wire shape.
// An inbox-only candidate can be pending/invalid, never ready. No storage
// result is cached: renewal, expiry and revocation are checked on each read.
func (m *Mediator) storageStates(ctx context.Context) []pinState {
	root := filepath.Join(m.cfg.StoreDir, "speicher")
	s, manifest, e := storagepins.ReadState(root)
	m.pruneStoragePackages(s, e == nil)
	checked := e == nil && s.Accepted != nil
	if !checked {
		b, err := storagepins.ReadFile(filepath.Join(root, "inbox/list.json"), storagepins.MaxListBytes)
		var envelope storagepins.Signed
		if err != nil || json.Unmarshal(b, &envelope) != nil || json.Unmarshal(envelope.Manifest, &manifest) != nil {
			return nil
		}
	}
	states := make([]pinState, 0, len(manifest.Pins))
	seen := map[string]bool{}
	for i, p := range manifest.Pins {
		if !gitref.ValidPinName(p.Name) || !gitref.ValidCommit(p.Commit) || len(p.Commit) != 64 || !gitref.ValidPinPath(p.Path) || seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		ps := pinState{pin: pins.Pin{Name: p.Name, Commit: p.Commit, Path: p.Path}, storage: true, storageAccepted: checked, verdict: verdict{state: contract.StatePending}}
		if checked {
			ps.verdict = m.pinApproval(ps)
			if ps.state == contract.StateReady {
				ps.verdict = m.verifySource(ctx, ps.pin, m.pinSource(ps))
			}
		} else if e != nil {
			ps.verdict = verdict{state: contract.StateInvalid, reason: contract.ReasonFetchFailed}
		}
		for _, f := range s.Failed {
			if f.Index == -1 || f.Index == i {
				ps.verdict = verdict{state: f.State, reason: f.Reason}
				break
			}
		}
		states = append(states, ps)
	}
	return states
}

// Only previously approved packages are pruned here. Newly delivered packages
// remain available for the fetcher; the courier separately prunes list removals.
func (m *Mediator) pruneStoragePackages(s storagepins.State, valid bool) {
	c := m.storageCourier
	c.pruneMu.Lock()
	defer c.pruneMu.Unlock()
	keep := map[string]bool{}
	if valid && s.Fresh(m.cfg.Now()) {
		for _, a := range s.Approved {
			keep[a.Name+"-"+a.Commit+".pkg"] = true
		}
	}
	old := c.approvedPackages
	if old == nil {
		old = map[string]bool{}
	}
	for _, a := range s.Approved {
		old[a.Name+"-"+a.Commit+".pkg"] = true
	}
	for name := range old {
		if !keep[name] {
			_ = storagepins.RemoveFile(filepath.Join(c.root, "inbox/pkg", name))
		}
	}
	c.approvedPackages = keep
}
