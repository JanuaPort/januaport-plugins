package storagepins

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/gitobjects"
)

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("../../contract/golden/storage/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestCoreGoldenVectorsAndChecksums(t *testing.T) {
	sums := strings.Fields(string(readGolden(t, "SHA256SUMS")))
	for i := 0; i < len(sums); i += 2 {
		h := sha256.Sum256(readGolden(t, sums[i+1]))
		if hex.EncodeToString(h[:]) != sums[i] {
			t.Fatal("checksum", sums[i+1])
		}
	}
	var cases []struct {
		Case        string
		Valid       bool
		Now         string
		HighestSeen uint64
		MaxAge      string
		PublicKey   string
	}
	if err := json.Unmarshal(readGolden(t, "expectations.json"), &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Case, func(t *testing.T) {
			key := c.PublicKey
			if key == "" {
				key = strings.TrimSpace(string(readGolden(t, "test-key.pub")))
			}
			pub, e := base64.StdEncoding.DecodeString(key)
			if e != nil {
				t.Fatal(e)
			}
			now, e := time.Parse(time.RFC3339, c.Now)
			if e != nil {
				t.Fatal(e)
			}
			// Golden expiry is > 10 min too; production policy is the fixed MaxAge.
			_, _, err := Verify(readGolden(t, c.Case+".json"), pub, c.HighestSeen, time.Time{}, now)
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v: %v", c.Valid, err)
			}
		})
	}
}

func signList(t *testing.T, key ed25519.PrivateKey, seq uint64, at time.Time) []byte {
	t.Helper()
	m := Manifest{Purpose: Purpose, Version: 1, Source: "speicher", Seq: seq, IssuedAt: at.UTC().Format(time.RFC3339), Pins: []Pin{}}
	raw, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	raw, e = gitobjects.Canonical(raw)
	if e != nil {
		t.Fatal(e)
	}
	out, e := json.Marshal(Signed{Manifest: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, raw))})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	p, k, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return p, k
}

func TestVerifySameSequenceNeverMovesIssuedAtBackwards(t *testing.T) {
	p, k := testKey(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for _, d := range []time.Duration{-time.Second, 0, time.Second} {
		_, _, err := Verify(signList(t, k, 9, now.Add(d)), p, 9, now, now)
		if (err == nil) != (d >= 0) {
			t.Fatalf("delta %v: %v", d, err)
		}
	}
}

func TestKeyFileRejectsTestAndSmallOrderKeys(t *testing.T) {
	p, _ := testKey(t)
	path := filepath.Join(t.TempDir(), "runner-pins.pub")
	good := []byte(base64.StdEncoding.EncodeToString(p) + "\n")
	rfc, _ := hex.DecodeString("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
	neutral := make([]byte, 32)
	neutral[0] = 1
	for name, b := range map[string][]byte{"valid": good, "golden": readGolden(t, "test-key.pub"), "rfc": []byte(base64.StdEncoding.EncodeToString(rfc) + "\n"), "neutral": []byte(base64.StdEncoding.EncodeToString(neutral) + "\n"), "order4": []byte(base64.StdEncoding.EncodeToString(make([]byte, 32)) + "\n"), "noLF": good[:len(good)-1], "twoLF": append(bytes.Clone(good), '\n'), "short": []byte("AA==\n"), "garbage": []byte("secret-canary")} {
		t.Run(name, func(t *testing.T) {
			if e := os.WriteFile(path, b, 0600); e != nil {
				t.Fatal(e)
			}
			_, e := LoadKey(path)
			if (e == nil) != (name == "valid") {
				t.Fatalf("key accepted=%v", e == nil)
			}
		})
	}
}
