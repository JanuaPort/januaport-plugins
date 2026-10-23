package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/platform"
)

func TestPairLeererPubkeySpeichertNichts(t *testing.T) { pairStorageProbe(t, "") }
func TestPairRFC8032PubkeySpeichertNichts(t *testing.T) {
	b, err := os.ReadFile("contract/golden/manifest_test_key.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	pairStorageProbe(t, fixture["public_key_base64"].(string))
}
func pairStorageProbe(t *testing.T, forbidden string) {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	key := forbidden
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(client.PairReply{DeviceID: "0123456789abcdef", Token: "jnpt_test_secret", ExpiresAt: "2027-01-01T00:00:00Z", AnlagePubkey: key})
	}))
	defer srv.Close()
	args := []string{"pair", "--url", srv.URL, "--code", "0123456789abcdef.ABCD-EFGH-JKMN"}
	if err := run(args, io.Discard); err == nil {
		t.Fatal("verbotener Pubkey akzeptiert")
	}
	if hits != 1 {
		t.Fatal("Pubkey-Prüfung nicht erreicht")
	}
	root, err := platform.Root()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("abgewiesene Kopplung speichert Daten", err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key = base64.StdEncoding.EncodeToString(pub)
	if err = run(args, io.Discard); err != nil {
		t.Fatal("positive Kontrolle", err)
	}
	path := filepath.Join(root, "credential")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	key = forbidden
	if err = run(args, io.Discard); err == nil {
		t.Fatal("verbotene Neukopplung akzeptiert")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("bestehendes Credential verändert", err)
	}
}
