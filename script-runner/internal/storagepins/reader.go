package storagepins

import (
	"crypto/ed25519"
	"encoding/base64"
	"path/filepath"

	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

// ReadState reads one coherent fetcher snapshot. The mediator deliberately has
// no access to runner-pins.pub: signature verification belongs to the fetcher.
// This does not protect against a compromised mediator with the shared UID.
func ReadState(root string) (State, Manifest, error) {
	var s State
	var m Manifest
	b, e := ReadFile(filepath.Join(root, "state.json"), MaxListBytes)
	if e != nil || exact(b, &s) != nil || s.Version != 1 || s.Approved == nil || s.Failed == nil {
		return State{}, Manifest{}, ErrInvalid
	}
	if s.HighestSeq == 0 {
		if s.Accepted != nil || len(s.Approved) != 0 || !validFailures(s.Failed, 0) {
			return State{}, Manifest{}, ErrInvalid
		}
		return s, m, nil
	}
	if !hexDigest(s.KeyFpr) || s.Accepted == nil || exact(s.Accepted.Manifest, &m) != nil || m.Purpose != Purpose || m.Version != 1 || m.Source != "speicher" || m.Seq != s.HighestSeq || m.Pins == nil {
		return State{}, Manifest{}, ErrInvalid
	}
	sig, e := base64.StdEncoding.Strict().DecodeString(s.Accepted.Sig)
	if e != nil || len(sig) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(sig) != s.Accepted.Sig {
		return State{}, Manifest{}, ErrInvalid
	}
	previous := ""
	for _, p := range m.Pins {
		if p.Name <= previous || !gitref.ValidPinName(p.Name) || p.Algo != "sha256" || !hexDigest(p.Commit) || !gitref.ValidPinPath(p.Path) {
			return State{}, Manifest{}, ErrInvalid
		}
		previous = p.Name
	}
	if !validApprovals(s.Approved, m) || !validFailures(s.Failed, len(m.Pins)) {
		return State{}, Manifest{}, ErrInvalid
	}
	return s, m, nil
}
