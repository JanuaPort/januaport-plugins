package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const validPairCode = "0123456789abcdef.ABCD-EFGH-JKMN"

func pairFixture(t *testing.T, key string) (*API, *int) {
	t.Helper()
	hits := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PairReply{"0123456789abcdef", "jnpt_test_secret", "2027-01-01T00:00:00Z", key})
	}))
	t.Cleanup(srv.Close)
	a, err := NewAPI(Trust{URL: srv.URL}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, hits
}

func TestPairCodeFormat(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	api, hits := pairFixture(t, base64.StdEncoding.EncodeToString(pub))
	codes := []string{
		validPairCode, strings.TrimPrefix(validPairCode, "0123456789abcdef."),
		strings.Replace(validPairCode, "abcdef", "ABCDEF", 1),
		strings.Replace(validPairCode, "abcdef.", "abcde.", 1),
		strings.Replace(validPairCode, "abcdef.", "abcdef0.", 1),
		strings.Replace(validPairCode, ".", "/", 1),
		strings.ToLower(validPairCode), validPairCode[:len(validPairCode)-1],
		" " + validPairCode, validPairCode + "\n",
	}
	for _, bad := range []string{"IJKL", "OOOO", "UUUU", "ＡＢＣＤ"} {
		codes = append(codes, strings.Replace(validPairCode, "ABCD", bad, 1))
	}
	for _, code := range codes {
		before := *hits
		_, err := api.Pair(context.Background(), code, "test")
		if code == validPairCode {
			if err != nil || *hits != before+1 {
				t.Errorf("gültiger Code abgewiesen: %v", err)
			}
		} else if err == nil || *hits != before {
			t.Errorf("ungültiger Code übertragen/akzeptiert: %q", code)
		}
	}
}

func goldenPublicKey(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../contract/golden/manifest_test_key.json")
	if err != nil {
		t.Fatal(err)
	}
	var key map[string]any
	if err = json.Unmarshal(b, &key); err != nil {
		t.Fatal(err)
	}
	return key["public_key_base64"].(string)
}

func TestPairLeererPubkey(t *testing.T) {
	assertRejectedPairKey(t, "")
}
func TestPairRFC8032Pubkey(t *testing.T) {
	assertRejectedPairKey(t, goldenPublicKey(t))
}
func assertRejectedPairKey(t *testing.T, key string) {
	t.Helper()
	api, hits := pairFixture(t, key)
	reply, err := api.Pair(context.Background(), validPairCode, "test")
	if *hits != 1 {
		t.Fatal("Pubkey-Prüfung nicht erreicht")
	}
	if err == nil || reply != (PairReply{}) {
		t.Fatal("verbotener Pubkey liefert Credential")
	}
}
