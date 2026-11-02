package store

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/fetcher"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitobjects"
	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
)

func TestStorageSourceAtRealFetcherAdapter(t *testing.T) {
	for _, mode := range []string{"valid", "inbox_only", "missing_key", "expired", "revoked", "hash", "symlink", "wrong_identity", "state_extra", "expires_during_read", "approval_changes_during_read"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			root := t.TempDir()
			keys := t.TempDir()
			pub, key, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				t.Fatal(e)
			}
			write := func(p string, b []byte) {
				t.Helper()
				if e := os.WriteFile(p, b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			raw, e := os.ReadFile("../../contract/golden/storage/valid.json")
			if e != nil {
				t.Fatal(e)
			}
			var signed storagepins.Signed
			var m storagepins.Manifest
			if json.Unmarshal(raw, &signed) != nil || json.Unmarshal(signed.Manifest, &m) != nil {
				t.Fatal("golden")
			}
			pin := m.Pins[0]
			pkg, e := os.ReadFile("../../contract/golden/storage/valid.gitobjects")
			if e != nil {
				t.Fatal(e)
			}
			if e := os.MkdirAll(filepath.Join(root, "speicher/inbox/pkg"), 0700); e != nil {
				t.Fatal(e)
			}
			keyPath := filepath.Join(keys, "runner-pins.pub")
			write(keyPath, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"))
			f := fetcher.New(fetcher.Config{StoreDir: root, KeyPath: filepath.Join(keys, "runtime-key"), StorageNow: func() time.Time { return now }})
			f.StorageOnce()
			now = now.Add(13 * time.Minute)
			publish := func() {
				m.IssuedAt = now.Format(time.RFC3339)
				b, _ := json.Marshal(m)
				b, e = gitobjects.Canonical(b)
				if e != nil {
					t.Fatal(e)
				}
				b, _ = json.Marshal(storagepins.Signed{Manifest: b, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, b))})
				write(filepath.Join(root, "speicher/inbox/list.json"), b)
			}
			publish()
			pkgPath := filepath.Join(root, "speicher/inbox/pkg", pin.Name+"-"+pin.Commit+".pkg")
			write(pkgPath, pkg)
			if mode != "inbox_only" {
				f.StorageOnce()
			}
			switch mode {
			case "missing_key":
				if e = os.Remove(keyPath); e != nil {
					t.Fatal(e)
				}
				f.StorageOnce()
			case "expired":
				now = now.Add(10*time.Minute + time.Nanosecond)
			case "revoked":
				m.Seq++
				m.Pins = []storagepins.Pin{}
				publish()
				f.StorageOnce()
			case "hash":
				write(pkgPath, append(pkg, 'x'))
			case "symlink":
				target := filepath.Join(t.TempDir(), "pkg")
				write(target, pkg)
				if e = os.Remove(pkgPath); e != nil {
					t.Fatal(e)
				}
				if e = os.Symlink(target, pkgPath); e != nil {
					t.Fatal(e)
				}
			case "wrong_identity":
				pin.Name = "other"
			case "state_extra":
				p := filepath.Join(root, "speicher/state.json")
				b, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				write(p, append([]byte(`{"expires_at":"2099",`), b[1:]...))
			}
			reads := 0
			s := OpenStorage(filepath.Join(root, "speicher"), pin, func() time.Time {
				reads++
				if mode == "expires_during_read" && reads == 2 {
					return now.Add(10*time.Minute + time.Nanosecond)
				}
				if mode == "approval_changes_during_read" && reads == 1 {
					// ReadState already captured the first approval. Publish a
					// different, well-formed hash before the second snapshot.
					state, _, err := storagepins.ReadState(filepath.Join(root, "speicher"))
					if err != nil {
						t.Fatal(err)
					}
					state.Approved[0].PackageSHA256 = strings.Repeat("0", 64)
					b, _ := json.Marshal(state)
					if err = storagepins.WriteFile(filepath.Join(root, "speicher/state.json"), b, storagepins.MaxListBytes); err != nil {
						t.Fatal(err)
					}
				}
				return now
			})
			tree, e := s.Load(context.Background(), pin.Commit, pin.Path)
			if mode == "expires_during_read" || mode == "approval_changes_during_read" {
				if reads != 2 {
					t.Fatalf("expected two approval snapshots, got %d", reads)
				}
			}
			if mode == "valid" {
				if e != nil || tree.File("main.py") == nil {
					t.Fatal("valid source", e)
				}
				var out bytes.Buffer
				id := tree.File("main.py").BlobID
				if _, e = s.CopyBlob(context.Background(), id, &out, 1<<20); e != nil || !bytes.Equal(out.Bytes(), tree.File("main.py").Data) {
					t.Fatal("blob source", e)
				}
			} else {
				if e == nil {
					t.Fatal("unapproved storage read", mode)
				}
				if mode == "hash" {
					i, ok := e.(*Invalid)
					if !ok || i.Reason != contract.ReasonHashMismatch {
						t.Fatal("hash reason", e)
					}
				}
			}
		})
	}
}
