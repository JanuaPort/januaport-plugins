package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Import the unchanged golden objects into a real object database. No test
// encoder stands between the store implementation and the byte comparison.
func TestDesktopPackageGolden(t *testing.T) {
	for _, algo := range []string{"sha1", "sha256"} {
		t.Run(algo, func(t *testing.T) {
			r := newRepo(t, algo)
			gold, err := os.ReadFile(filepath.Join("../../../desktop-runner/contract/golden", "package_"+algo+".bin"))
			if err != nil {
				t.Fatal(err)
			}
			read := bytes.NewReader(gold)
			next := func() []byte {
				var n uint32
				if err := binary.Read(read, binary.BigEndian, &n); err != nil {
					t.Fatal(err)
				}
				b := make([]byte, n)
				if _, err := read.Read(b); err != nil {
					t.Fatal(err)
				}
				return b
			}
			var head struct {
				Commit  string
				Path    string
				Objects int
			}
			if err := json.Unmarshal(next(), &head); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < head.Objects; i++ {
				b := next()
				typ := map[byte]string{'c': "commit", 't': "tree", 'b': "blob"}[b[0]]
				r.gitIn(b[1:], "hash-object", "-t", typ, "-w", "--stdin")
			}
			got, _, err := Open(r.gitDir()).DesktopPackage(context.Background(), head.Commit, head.Path, algo)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, gold) {
				t.Fatalf("package differs from %s golden", algo)
			}
		})
	}
}

func desktopCommit(r *testRepo, mode, name string, data []byte) string {
	blob := r.gitIn(data, "hash-object", "-w", "--stdin")
	id, _ := hex.DecodeString(blob)
	pin := r.gitIn(append([]byte(mode+" "+name+"\x00"), id...), "hash-object", "-t", "tree", "--literally", "-w", "--stdin")
	raw, _ := hex.DecodeString(pin)
	root := r.gitIn(append([]byte("40000 desk\x00"), raw...), "hash-object", "-t", "tree", "--literally", "-w", "--stdin")
	return r.git("commit-tree", root, "-m", "test")
}

func TestDesktopPackageRejectsUnsafeObjects(t *testing.T) {
	for _, tc := range []struct{ mode, name string }{
		{"120000", "link"}, {"160000", "sub"}, {"100600", "badmode"},
		{"100644", ".."}, {"100644", "CON.txt"}, {"100644", "name."}, {"100644", "name "},
		{"100644", "a:b"}, {"100644", "a\\b"}, {"100644", string([]byte{0xff})},
		{"100644", "a/b"}, {"100644", "."}, {"100644", "a\nb"},
		{"100644", "a<b"}, {"100644", "a>b"}, {"100644", "a\"b"},
		{"100644", "a|b"}, {"100644", "a?b"}, {"100644", "a*b"},
		{"100644", "prn.txt"}, {"100644", "aux"}, {"100644", "nul.dat"},
		{"100644", "COM1"}, {"100644", "com9.py"}, {"100644", "LPT1"}, {"100644", "lpt9.txt"},
	} {
		t.Run(fmt.Sprintf("%s_%x", tc.mode, tc.name), func(t *testing.T) {
			r := newRepo(t, "sha1")
			commit := desktopCommit(r, tc.mode, tc.name, []byte("x"))
			if _, _, err := Open(r.gitDir()).DesktopPackage(context.Background(), commit, "desk", "sha1"); err == nil {
				t.Fatal("unsafe object exported")
			}
		})
	}
}

func TestDesktopPackageExactObjectSet(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("a/desk/main.py", "x")
	r.write("a/desk/copy.py", "x")
	r.write("a/other/secret", "sibling")
	r.commit()
	r.write("a/desk/sub/data", "y")
	commit := r.commit()
	b, tree, err := Open(r.gitDir()).DesktopPackage(context.Background(), commit, "a/desk", "sha1")
	if err != nil {
		t.Fatal(err)
	}
	var h struct{ Objects int }
	n := int(binary.BigEndian.Uint32(b[:4]))
	_ = json.Unmarshal(b[4:4+n], &h)
	// commit, root, a, desk, shared blob x, sub, blob y; no parent or sibling.
	if h.Objects != 7 || tree.File("main.py") == nil || tree.File("copy.py") == nil || tree.File("sub/data") == nil || bytes.Contains(b, []byte("sibling")) {
		t.Fatalf("object set: %d", h.Objects)
	}
}

func TestDesktopPackageLimitIncludesFraming(t *testing.T) {
	// A real 128 MiB package limit, not a reduced injected testing limit.
	r := newRepo(t, "sha1")
	for i := 0; i < 9; i++ {
		r.write(fmt.Sprintf("desk/f%d", i), fmt.Sprint(i))
	}
	r.write("desk/big", "")
	r.commit()
	// Keep exactly one parent line in all commits; it is transported only as
	// raw commit content. 13 objects exercise the head's object-count digit.
	r.git("commit", "--allow-empty", "-q", "-m", "c")
	base := r.git("rev-parse", "HEAD")
	baseline, _, err := Open(r.gitDir()).DesktopPackage(context.Background(), base, "desk", "sha1")
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			r.write("desk/big", strings.Repeat("x", (128<<20)-len(baseline)+extra))
			commit := r.commit()
			b, _, err := Open(r.gitDir()).DesktopPackage(context.Background(), commit, "desk", "sha1")
			if extra == 0 {
				if err != nil || len(b) != (128<<20) {
					t.Fatalf("exact boundary: len=%d err=%v", len(b), err)
				}
			} else if !errors.Is(err, ErrDesktopTooLarge) {
				t.Fatalf("boundary+1: %v", err)
			}
		})
	}
}

func TestDesktopPackageSizeCheckedBeforeAllocation(t *testing.T) {
	r := newRepo(t, "sha1")
	commit := desktopCommit(r, "100644", "big", []byte(strings.Repeat("x", 128<<20)))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _, err := Open(r.gitDir()).DesktopPackage(context.Background(), commit, "desk", "sha1")
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrDesktopTooLarge) {
		t.Fatalf("oversized object: %v", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
		t.Fatalf("oversized blob allocated before rejection: %d bytes", allocated)
	}
}

func TestDesktopPackageCaseCollisionAndDepth(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("desk/A.py", "a")
	r.write("desk/a.py", "b")
	if _, _, err := Open(r.gitDir()).DesktopPackage(context.Background(), r.commit(), "desk", "sha1"); err == nil {
		t.Fatal("case collision accepted")
	}
	// Build deep objects directly, avoiding the host's path-length limits.
	blob := r.gitIn([]byte("x"), "hash-object", "-w", "--stdin")
	raw, _ := hex.DecodeString(blob)
	id := r.gitIn(append([]byte("100644 x\x00"), raw...), "hash-object", "-t", "tree", "-w", "--stdin")
	for i := 0; i < 256; i++ {
		raw, _ := hex.DecodeString(id)
		id = r.gitIn(append([]byte("40000 d\x00"), raw...), "hash-object", "-t", "tree", "-w", "--stdin")
	}
	commit := r.git("commit-tree", id, "-m", "deep")
	if _, _, err := Open(r.gitDir()).DesktopPackage(context.Background(), commit, "d", "sha1"); err == nil {
		t.Fatal("more than 256 edges accepted")
	}
}

func TestDesktopPackageUnicodeCollision(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("desk/K.py", "a")
	r.write("desk/K.py", "b")
	if _, _, err := Open(r.gitDir()).DesktopPackage(context.Background(), r.commit(), "desk", "sha1"); err == nil {
		t.Fatal("Unicode folding collision accepted")
	}
}
