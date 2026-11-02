package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitobjects"
	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
)

// objectSource is owned by the consuming sink. Git's batch adapter remains
// unchanged; storage opens a newly authorized immutable graph per operation.
type objectSource interface {
	get(string) (string, []byte, error)
	close()
}
type storageSource struct {
	root string
	pin  storagepins.Pin
	now  func() time.Time
}
type packageObjects map[string]gitobjects.Object

func (p packageObjects) get(id string) (string, []byte, error) {
	o, ok := p[id]
	if !ok {
		return "", nil, errMissing
	}
	return o.Type, o.Data, nil
}
func (packageObjects) close() {}

func OpenStorage(root string, pin storagepins.Pin, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{storage: &storageSource{root: root, pin: pin, now: now}}
}

func storageInvalid(reason string) error {
	return &Invalid{Reason: reason, Msg: "Speicherfreigabe ungültig"}
}

func (s *Store) storageApproval() (storagepins.Approval, error) {
	src := s.storage
	state, manifest, e := storagepins.ReadState(src.root)
	if e != nil || !state.Fresh(src.now()) || state.KeyFpr == "" {
		return storagepins.Approval{}, storageInvalid(contract.ReasonFetchFailed)
	}
	for _, failure := range state.Failed {
		if failure.Index == -1 || (failure.Index >= 0 && manifest.Pins[failure.Index].Name == src.pin.Name) {
			return storagepins.Approval{}, storageInvalid(contract.ReasonFetchFailed)
		}
	}
	for _, a := range state.Approved {
		if a.Name == src.pin.Name && a.Commit == src.pin.Commit && a.Path == src.pin.Path {
			return a, nil
		}
	}
	return storagepins.Approval{}, storageInvalid(contract.ReasonFetchFailed)
}

func (s *Store) storageObjects() (objectSource, error) {
	a, e := s.storageApproval()
	if e != nil {
		return nil, e
	}
	b, e := storagepins.ReadFile(filepath.Join(s.storage.root, "inbox/pkg", a.Name+"-"+a.Commit+".pkg"), storagepins.MaxPackageBytes)
	if e != nil {
		return nil, storageInvalid(contract.ReasonFetchFailed)
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != a.PackageSHA256 {
		return nil, storageInvalid(contract.ReasonHashMismatch)
	}
	objects, e := gitobjects.Objects(b, a.Commit, a.Path)
	if e != nil {
		return nil, storageInvalid(contract.ReasonHashMismatch)
	}
	// A rename or expiration during reading must not authorize the old snapshot.
	current, e := s.storageApproval()
	if e != nil {
		return nil, e
	}
	if current != a {
		return nil, storageInvalid(contract.ReasonFetchFailed)
	}
	return packageObjects(objects), nil
}

func (s *Store) openObjects(ctx context.Context, commit, path string) (objectSource, error) {
	if s.storage == nil {
		return s.batch(ctx)
	}
	if commit != s.storage.pin.Commit || path != s.storage.pin.Path || ctx.Err() != nil {
		return nil, storageInvalid(contract.ReasonFetchFailed)
	}
	return s.storageObjects()
}

// CheckStorage is used immediately before code handoff, including after
// waiting for a sandbox. It rereads approval, expiry and exact package hash.
func (s *Store) CheckStorage() error {
	b, e := s.storageObjects()
	if e == nil {
		b.close()
	}
	return e
}

func (s *Store) copyStorageBlob(ctx context.Context, id string, dst io.Writer, max int64) (int64, error) {
	b, e := s.openObjects(ctx, s.storage.pin.Commit, s.storage.pin.Path)
	if e != nil {
		return 0, e
	}
	defer b.close()
	typ, data, e := b.get(id)
	if e != nil || typ != "blob" {
		return 0, storageInvalid(contract.ReasonHashMismatch)
	}
	if int64(len(data)) > max {
		return 0, storageInvalid(contract.ReasonLibsTooLarge)
	}
	if objectID(typ, data, 32) != id {
		return 0, storageInvalid(contract.ReasonHashMismatch)
	}
	if _, e = s.storageApproval(); e != nil {
		return 0, e
	}
	n, e := dst.Write(data)
	return int64(n), e
}
