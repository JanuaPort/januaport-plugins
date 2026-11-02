//go:build linux

package storagepins

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestHostileFileAdapter(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if e := os.WriteFile(target, []byte("canary"), 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(root, "link")
	if e := os.Symlink(target, link); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadFile(link, 100); e == nil {
		t.Fatal("symlink read")
	}
	if _, e := ReadFile(root, 100); e == nil {
		t.Fatal("directory read")
	}
	fifo := filepath.Join(root, "fifo")
	if e := unix.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadFile(fifo, 100); e == nil {
		t.Fatal("fifo read")
	}
	if _, e := ReadFile(target, 5); e == nil {
		t.Fatal("over limit")
	}
	if b, e := ReadFile(target, 6); e != nil || string(b) != "canary" {
		t.Fatal("inclusive limit", e)
	}
	parentLink := filepath.Join(root, "parent")
	if e := os.Symlink(root, parentLink); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadFile(filepath.Join(parentLink, "target"), 100); e == nil {
		t.Fatal("parent symlink")
	}
}

func TestAtomicStateTempLinkNeverWritten(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if e := os.WriteFile(target, []byte("canary"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(target, filepath.Join(root, ".state-collision")); e != nil {
		t.Fatal(e)
	}
	if e := atomicWrite(root, []byte("replacement"), func() string { return ".state-collision" }); e == nil {
		t.Fatal("existing temp accepted")
	}
	b, e := os.ReadFile(target)
	if e != nil || !bytes.Equal(b, []byte("canary")) {
		t.Fatal("target overwritten")
	}
	if e := os.Symlink(target, filepath.Join(root, "state.json")); e != nil {
		t.Fatal(e)
	}
	if e := atomicWrite(root, []byte("new-state"), func() string { return ".state-new" }); e != nil {
		t.Fatal(e)
	}
	b, e = os.ReadFile(target)
	if e != nil || string(b) != "canary" {
		t.Fatal("destination link followed")
	}
	b, e = ReadFile(filepath.Join(root, "state.json"), 100)
	if e != nil || string(b) != "new-state" {
		t.Fatal("rename not published")
	}
}

func TestAtomicStateExistingRegularTempNeverWritten(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, ".state-collision")
	if e := os.WriteFile(p, []byte("canary"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := atomicWrite(root, []byte("replacement"), func() string { return ".state-collision" }); e == nil {
		t.Fatal("existing file reused")
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != "canary" {
		t.Fatal("existing file modified")
	}
}
