//go:build linux

package fetcher

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
)

func TestStorageMissingListLogsOnlyTransitionsAtAdapter(t *testing.T) {
	for _, which := range []string{"withdrawal", "startup", "missing_inbox"} {
		t.Run(which, func(t *testing.T) {
			x := newStorageFixture(t)
			x.ready()
			if which == "startup" {
				x.f = New(x.f.cfg)
			}
			list := filepath.Join(x.root, "speicher/inbox/list.json")
			remove := func() {
				t.Helper()
				if err := os.Remove(list); err != nil {
					t.Fatal(err)
				}
			}
			check := func(wantInfo int) {
				t.Helper()
				logs := x.logs.String()
				if strings.Contains(logs, "level=WARN") || strings.Count(logs, "level=INFO") != wantInfo {
					t.Fatalf("want no WARN and %d INFO lines: %s", wantInfo, logs)
				}
			}
			remove()
			inbox := filepath.Dir(list)
			if which == "missing_inbox" {
				if err := os.Rename(inbox, inbox+"-old"); err != nil {
					t.Fatal(err)
				}
			}
			x.logs.Reset()
			for i := 0; i < 4; i++ {
				x.f.StorageOnce()
				x.failed("fetch_failed") // Missing list still revokes existing approvals.
				check(1)
			}
			if which == "missing_inbox" {
				if err := os.Rename(inbox+"-old", inbox); err != nil {
					t.Fatal(err)
				}
			}
			x.publish()
			for i := 0; i < 4; i++ {
				x.f.StorageOnce()
				if len(x.state().Approved) != 1 {
					t.Fatal("restored list did not restore approval")
				}
				check(2)
			}
			remove()
			for i := 0; i < 4; i++ {
				x.f.StorageOnce()
				x.failed("fetch_failed")
				check(3)
			}
		})
	}
}

func TestStorageErrorsWarnEveryTickAtAdapter(t *testing.T) {
	for _, which := range []string{"signature", "hash", "list_read", "package_read", "dangling_list_link", "dangling_inbox_link"} {
		t.Run(which, func(t *testing.T) {
			x := newStorageFixture(t)
			x.ready()
			list := filepath.Join(x.root, "speicher/inbox/list.json")
			reason := "fetch_failed"
			switch which {
			case "signature":
				raw, err := os.ReadFile(list)
				if err != nil {
					t.Fatal(err)
				}
				var s storagepins.Signed
				if err = json.Unmarshal(raw, &s); err != nil {
					t.Fatal(err)
				}
				sig, err := base64.StdEncoding.DecodeString(s.Sig)
				if err != nil {
					t.Fatal(err)
				}
				sig[0] ^= 1
				s.Sig = base64.StdEncoding.EncodeToString(sig)
				raw, err = json.Marshal(s)
				if err != nil {
					t.Fatal(err)
				}
				x.write(list, raw)
			case "hash":
				x.pkg[len(x.pkg)-1] ^= 1
				x.write(x.packagePath(), x.pkg)
				reason = "hash_mismatch"
			case "package_read":
				if err := os.Remove(x.packagePath()); err != nil {
					t.Fatal(err)
				}
			default:
				target := list
				if which == "dangling_inbox_link" {
					target = filepath.Dir(list)
					if err := os.Rename(target, target+"-old"); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if which == "list_read" {
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(filepath.Join(x.root, "missing"), target); err != nil {
					t.Fatal(err)
				}
			}
			x.logs.Reset()
			for i := 1; i <= 4; i++ {
				x.f.StorageOnce()
				x.failed(reason)
				if logs := x.logs.String(); strings.Count(logs, "level=WARN msg=storage_fetch_failed") != i || strings.Contains(logs, "level=INFO") {
					t.Fatalf("tick %d must warn for %s: %s", i, which, logs)
				}
			}
		})
	}
}
