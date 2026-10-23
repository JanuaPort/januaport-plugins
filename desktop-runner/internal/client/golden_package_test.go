package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGoldenPaketeBytegenauUndManipulation(t *testing.T) {
	for _, algo := range []string{"sha1", "sha256"} {
		t.Run(algo, func(t *testing.T) {
			raw, err := os.ReadFile("../../contract/golden/package_" + algo + ".bin")
			if err != nil {
				t.Fatal(err)
			}
			original := bytes.Clone(raw)
			n := int(binary.BigEndian.Uint32(raw[:4]))
			var h packageHead
			if err = json.Unmarshal(raw[4:4+n], &h); err != nil {
				t.Fatal(err)
			}
			p := Pin{Pin: "demo", Algo: algo, Commit: h.Commit, Path: h.Path}
			files, err := PackageFiles(raw, p)
			if err != nil {
				t.Fatal("unverändertes Golden abgewiesen", err)
			}
			// The format Goldens contain one blob. Compare its exact payload, not
			// normalized text or a reconstructed package.
			blobOffset := -1
			var blob []byte
			for off := 4 + n; off < len(raw); {
				size := int(binary.BigEndian.Uint32(raw[off : off+4]))
				if raw[off+4] == 'b' {
					blobOffset = off + 5
					blob = raw[blobOffset : off+4+size]
				}
				off += 4 + size
			}
			if len(files) != 1 || blobOffset < 0 {
				t.Fatal("unerwartete Golden-Struktur")
			}
			for _, data := range files {
				if !bytes.Equal(data, blob) {
					t.Fatal("Blob-Bytes verändert")
				}
			}
			if !bytes.Equal(raw, original) {
				t.Fatal("Paketbytes verändert")
			}
			bad := bytes.Clone(raw)
			bad[blobOffset] ^= 1
			if _, err = PackageFiles(bad, p); err != ErrHash {
				t.Fatal("Manipulation akzeptiert", err)
			}
			st, err := OpenState(t.TempDir(), testCipher{})
			if err != nil {
				t.Fatal(err)
			}
			dir := envKey(p)
			if err = os.MkdirAll(filepath.Join(st.Root, "packages"), 0700); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(st.Root, "packages", dir), bad)
			starts := 0
			config := Config{ExpectedAccount: "own"}
			config.StartPolicy.Mode = "always"
			engine := Engine{State: st, Config: config, Session: func() (Session, error) { return Session{Active: true, Account: "own"}, nil }, Spawn: func(context.Context, Launch) (Process, error) { starts++; return nil, ErrProtocol }}
			result := engine.Execute(context.Background(), Result{Commit: p.Commit}, Environment{Origin: p, Dir: dir}, json.RawMessage("{}"), "", 1, 0)
			if result.End != "refused" || result.Detail != "hash" || starts != 0 {
				t.Fatalf("Ergebnis=%+v Starts=%d", result, starts)
			}
		})
	}
}
