package client

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/robfig/cron/v3"
	"strings"
	"time"
	_ "time/tzdata"
)

func parseSchedule(s Schedule) (cron.Schedule, error) {
	if len(strings.Fields(s.Cron)) != 5 || s.TZ == "" || s.TZ == "Local" {
		return nil, ErrManifest
	}
	loc, err := time.LoadLocation(s.TZ)
	if err != nil {
		return nil, ErrManifest
	}
	v, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(s.Cron)
	if err != nil {
		return nil, ErrManifest
	}
	v.(*cron.SpecSchedule).Location = loc
	return v, nil
}
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	if strict(b, &m) != nil || (m.Contract != 1 && m.Contract != 2) || m.DeviceID == "" || m.Seq == 0 || !utc(m.IssuedAt) || m.GraceHours < 1 || m.GraceHours > 72 || m.Pins == nil {
		return m, ErrManifest
	}
	names := map[string]bool{}
	for _, p := range m.Pins {
		if !safeName(p.Pin) || names[p.Pin] || !validID(p.Algo, p.Commit) || (m.Contract == 1 && (!safePath(p.Path) || p.Container != nil)) || (m.Contract == 2 && !validContainerPin(p)) || p.DeclaredTools == nil || p.Schedules == nil {
			return m, ErrManifest
		}
		names[p.Pin] = true
		tools := map[string]bool{}
		for _, s := range p.DeclaredTools {
			if s == "" || tools[s] {
				return m, ErrManifest
			}
			tools[s] = true
		}
		sn := map[string]bool{}
		for _, s := range p.Schedules {
			if !safeName(s.Name) || sn[s.Name] || !inputOK(s.Input) {
				return m, ErrManifest
			}
			sn[s.Name] = true
			if _, err := parseSchedule(s); err != nil {
				return m, err
			}
		}
	}
	return m, nil
}
func VerifyManifest(e SignedManifest, pub []byte, device string, minSeq uint64) (Manifest, error) {
	m, err := ParseManifest(e.Manifest)
	if err != nil {
		return m, ErrManifest
	}
	b, err := canonical(e.Manifest)
	if err != nil {
		return m, ErrManifest
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(e.Sig)
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, b, sig) || m.DeviceID != device || m.Seq <= minSeq {
		return m, ErrManifest
	}
	return m, nil
}
func OfflineAllowed(p Pin, grace int, last, now time.Time) bool {
	return len(p.DeclaredTools) == 0 && grace >= 1 && grace <= 72 && !last.IsZero() && !now.Before(last) && now.Sub(last) < time.Duration(grace)*time.Hour
}
func utc(s string) bool {
	_, e := time.Parse(time.RFC3339, s)
	return e == nil && strings.HasSuffix(s, "Z")
}
func inputOK(b json.RawMessage) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 1 && len(b) <= 256<<10 && b[0] == '{' && json.Valid(b)
}
func RunKey(p Pin, s Schedule, slot time.Time) string {
	return p.Pin + "/" + s.Name + "/" + slot.UTC().Format("2006-01-02T15:04:00Z")
}
