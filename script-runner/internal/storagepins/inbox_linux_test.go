//go:build linux

package storagepins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInboxPublicationConfinedAndAtomic(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(root, "inbox")); e != nil {
		t.Fatal(e)
	}
	if WriteFile(filepath.Join(root, "inbox/list.json"), []byte("bad"), MaxListBytes) == nil {
		t.Fatal("parent link followed")
	}
	if _, e := os.Stat(filepath.Join(outside, "list.json")); !os.IsNotExist(e) {
		t.Fatal("outside write")
	}
	p := filepath.Join(root, "list.json")
	if e := os.WriteFile(p, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	if WriteFile(p, []byte("too large"), 2) == nil {
		t.Fatal("oversize published")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "old" {
		t.Fatal("partial replacement")
	}
	if e := WriteFile(p, []byte("new"), 3); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(p)
	if string(b) != "new" {
		t.Fatal("missing replacement")
	}
	if e := RemoveFile(p); e != nil {
		t.Fatal(e)
	}
}
