// Package storagepins implements the signed storage-pin contract and its
// durable, fail-closed sequence journal (JanuaPort/januaport#1277).
package storagepins

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"filippo.io/edwards25519"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitobjects"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

const (
	Purpose               = "skriptlaeufer-pins.v1"
	MaxAge                = 10 * time.Minute
	Skew                  = 2 * time.Minute
	MaxListBytes    int64 = 1 << 20
	MaxPackageBytes int64 = 128 << 20
	MaxTotalBytes   int64 = 1 << 30
)

var ErrInvalid = errors.New("fetch_failed")

type Manifest struct {
	Purpose  string `json:"zweck"`
	Version  int    `json:"version"`
	Source   string `json:"source"`
	Seq      uint64 `json:"seq"`
	IssuedAt string `json:"issued_at"`
	Pins     []Pin  `json:"pins"`
}
type Pin struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
	Path   string `json:"path"`
	Algo   string `json:"algo"`
}
type Signed struct {
	Manifest json.RawMessage `json:"manifest"`
	Sig      string          `json:"sig"`
}

// exact rejects duplicates, unknown/missing/null fields and Go's case-folded
// aliases by comparing the decoded schema with the entire canonical input.
func exact(b []byte, v any) error {
	c, e := gitobjects.Canonical(b)
	if e != nil {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(c))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return ErrInvalid
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return ErrInvalid
	}
	schema, e := gitobjects.Canonical(raw)
	if e != nil || !bytes.Equal(c, schema) {
		return ErrInvalid
	}
	return nil
}

var reservedTestName = regexp.MustCompile(`^test_[a-f0-9]{8}$`)

// Verify is the pure verifier. Golden keys are usable here, never in LoadKey.
// previousIssued belongs to highest, read from accepted in the same state.
func Verify(raw []byte, public ed25519.PublicKey, highest uint64, previousIssued, now time.Time) (Signed, Manifest, error) {
	return verifyMaxAge(raw, public, highest, previousIssued, now, MaxAge)
}

func verifyMaxAge(raw []byte, public ed25519.PublicKey, highest uint64, previousIssued, now time.Time, maxAge time.Duration) (Signed, Manifest, error) {
	var s Signed
	var m Manifest
	bad := func() (Signed, Manifest, error) { return Signed{}, Manifest{}, ErrInvalid }
	if int64(len(raw)) > MaxListBytes || exact(raw, &s) != nil || exact(s.Manifest, &m) != nil {
		return bad()
	}
	if m.Purpose != Purpose || m.Version != 1 || m.Source != "speicher" || m.Pins == nil || m.Seq == 0 || m.Seq < highest {
		return bad()
	}
	issued, e := time.Parse(time.RFC3339, m.IssuedAt)
	if e != nil || !strings.HasSuffix(m.IssuedAt, "Z") || issued.After(now.Add(Skew)) || maxAge <= 0 || now.Sub(issued) > maxAge {
		return bad()
	}
	if m.Seq == highest && issued.Before(previousIssued) {
		return bad()
	}
	canonical, e := gitobjects.Canonical(s.Manifest)
	if e != nil {
		return bad()
	}
	sig, e := base64.StdEncoding.Strict().DecodeString(s.Sig)
	if e != nil || base64.StdEncoding.EncodeToString(sig) != s.Sig || !validPublicKey(public) || !ed25519.Verify(public, canonical, sig) {
		return bad()
	}
	previous := ""
	for _, p := range m.Pins {
		if p.Name <= previous || !gitref.ValidPinName(p.Name) || reservedTestName.MatchString(p.Name) || p.Algo != "sha256" || len(p.Commit) != 64 || !gitref.ValidCommit(p.Commit) || !gitref.ValidPinPath(p.Path) {
			return bad()
		}
		previous = p.Name
	}
	s.Manifest = canonical
	return s, m, nil
}

func validPublicKey(public ed25519.PublicKey) bool {
	p, e := new(edwards25519.Point).SetBytes(public)
	if e != nil {
		return false
	}
	return new(edwards25519.Point).MultByCofactor(p).Equal(edwards25519.NewIdentityPoint()) == 0
}

// LoadKey accepts exactly the materializer's 32-byte Standard-Base64 + LF.
func LoadKey(path string) (ed25519.PublicKey, error) {
	b, e := ReadFile(path, 45)
	if e != nil || len(b) != 45 || b[44] != '\n' {
		return nil, ErrInvalid
	}
	p, e := base64.StdEncoding.Strict().DecodeString(string(b[:44]))
	if e != nil || len(p) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(p) != string(b[:44]) || !validPublicKey(p) {
		return nil, ErrInvalid
	}
	// Public fixtures must never become production trust anchors (SEC S4b-K).
	switch hex.EncodeToString(p) {
	case "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a", // RFC 8032
		"2152f8d19b791d24453242e15f2eab6cb7cffa7b6a5ed30097960e069881db12": // S4b-K golden
		return nil, ErrInvalid
	}
	return ed25519.PublicKey(p), nil
}

func fingerprint(p ed25519.PublicKey) string { h := sha256.Sum256(p); return hex.EncodeToString(h[:]) }

func hexDigest(s string) bool { return len(s) == 64 && gitref.ValidCommit(s) }
