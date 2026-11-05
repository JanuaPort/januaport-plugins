// Package statefixture schreibt Läufer-Zustand für Tests anderer Pakete: Zugangsdaten, signiertes
// Manifest und eine versiegelte App-Ablage, so wie sie der Läufer hinterlässt. Nur für Tests.
package statefixture

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
)

// Cipher ist die Test-Verschlüsselung (kein DPAPI): ein Präfix, damit Fremdes auffällt.
type Cipher struct{}

func (Cipher) Seal(b []byte) ([]byte, error) { return append([]byte("TEST-CIPHER:"), b...), nil }
func (Cipher) Open(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte("TEST-CIPHER:")) {
		return nil, errors.New("cipher")
	}
	return b[12:], nil
}

// Token ist ein Testwert aus Teilstrings: kein Token-Muster im Quelltext.
func Token() string { return "jn" + "pt_" + strings.Repeat("0123456789abcdef", 2) }

// Page ist eine gültige App-Startseite ohne eigenes CSP; der Läufer setzt das Literal selbst ein.
func Page(body string) []byte {
	return []byte("<!doctype html><html><head><meta charset=\"utf-8\"><title>t</title><script src=\"app.js\"></script></head><body>" + body + "</body></html>")
}

// Bed ist ein geschriebener Läufer-Zustand.
type Bed struct {
	Root string
	St   *client.State
	Cred client.Credential
	Key  ed25519.PrivateKey
	Apps []client.App
}

// New legt Zugangsdaten für den Kern unter serverURL an.
func New(t *testing.T, root, serverURL string) *Bed {
	t.Helper()
	st, err := client.OpenState(root, Cipher{})
	if err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(nil)
	b := &Bed{Root: st.Root, St: st, Key: key}
	b.Cred = client.Credential{Trust: client.Trust{URL: serverURL}, DeviceID: "aaaaaaaaaaaaaaaa", Token: Token(), ExpiresAt: "2099-01-01T00:00:00Z", Pubkey: pub}
	if err = st.Save("credential", b.Cred); err != nil {
		t.Fatal(err)
	}
	return b
}

// AddApp legt eine App mit der gegebenen Startseite ab (wie Engine.Sync) und gibt den Manifesteintrag zurück.
func (b *Bed) AddApp(t *testing.T, name string, files map[string][]byte) client.App {
	t.Helper()
	commit, raw := testfixture.PackageAt("apps/"+name, files, "")
	app := client.App{Name: name, Commit: commit, Path: "apps/" + name}
	if err := client.NewAppStore(b.St).Prepare(raw, app); err != nil {
		t.Fatal(err)
	}
	b.Apps = append(b.Apps, app)
	return app
}

// SaveManifest signiert und speichert ein Vertrag-5-Manifest mit den gegebenen Apps.
func (b *Bed) SaveManifest(t *testing.T, apps []client.App, seq uint64, testDevice bool) {
	t.Helper()
	if apps == nil {
		apps = []client.App{}
	}
	m := client.Manifest{Apps: &apps, TestDevice: testDevice, Contract: client.WindowsContract, DeviceID: b.Cred.DeviceID, Seq: seq, IssuedAt: "2026-11-04T10:00:00Z", GraceHours: 24, Pins: []client.Pin{}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	can, err := client.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	signed := client.SignedManifest{Manifest: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(b.Key, can))}
	bundle := client.Bundle{Manifest: client.ManifestState{Envelope: signed, LastSync: time.Now().UTC()}, Environments: map[string]client.Environment{}}
	if err = b.St.Save("manifest", bundle); err != nil {
		t.Fatal(err)
	}
}
