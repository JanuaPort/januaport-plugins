package store

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

// testRepo baut ein Arbeits-Repository mit echtem git. Die Senke liest aus
// dessen Objekt-DB (.git) — dieselbe Form wie der Bare-Store des abholers.
type testRepo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T, format string) *testRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git fehlt")
	}
	r := &testRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "--object-format="+format, ".")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "user.name", "Test")
	r.git("config", "core.symlinks", "true")
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	return r.gitIn(nil, args...)
}

func (r *testRepo) gitIn(stdin []byte, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+r.dir)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *testRepo) write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *testRepo) commit() string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "c")
	return r.git("rev-parse", "HEAD")
}

func (r *testRepo) gitDir() string { return filepath.Join(r.dir, ".git") }

func TestLoadValidTreeSHA1AndSHA256(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			r := newRepo(t, format)
			r.write("tool/main.py", "print('hi')\n")
			r.write("tool/vendor/lib.py", "X = 1\n")
			r.write("tool/jnpt-script.yaml", "name: x\n")
			r.write("other/secret.py", "nope\n")
			commit := r.commit()

			s := Open(r.gitDir())
			ok, err := s.HasCommit(context.Background(), commit)
			if err != nil || !ok {
				t.Fatalf("HasCommit = %v, %v", ok, err)
			}
			tree, err := s.Load(context.Background(), commit, "tool")
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, f := range tree.Files {
				got[f.Path] = string(f.Data)
			}
			want := map[string]string{"main.py": "print('hi')\n", "vendor/lib.py": "X = 1\n", "jnpt-script.yaml": "name: x\n"}
			if len(got) != len(want) {
				t.Fatalf("Dateien = %v", got)
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
			blob := r.git("rev-parse", commit+":tool/jnpt-script.yaml")
			if f := tree.File("jnpt-script.yaml"); f == nil || f.BlobID != blob {
				t.Fatalf("BlobID des Manifests = %+v, want %s", f, blob)
			}
		})
	}
}

func TestHasCommitMissing(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("a", "a")
	r.commit()
	ok, err := Open(r.gitDir()).HasCommit(context.Background(), strings.Repeat("c", 40))
	if err != nil || ok {
		t.Fatalf("HasCommit = %v, %v", ok, err)
	}
}

// M5 / R6: Symlink-Eintrag → abgewiesen.
func TestLoadRejectsSymlink(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("tool/main.py", "x")
	if err := os.Symlink("/etc/passwd", filepath.Join(r.dir, "tool", "passwd")); err != nil {
		t.Skip("Symlinks nicht verfügbar:", err)
	}
	commit := r.commit()
	assertInvalid(t, r, commit, "tool", contract.ReasonSymlink)
}

// M5 / R6: Gitlink-Eintrag (Submodul) → abgewiesen.
func TestLoadRejectsGitlink(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("tool/main.py", "x")
	r.git("add", "-A")
	r.git("update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("1", 40)+",tool/sub")
	r.git("commit", "-q", "-m", "c")
	assertInvalid(t, r, r.git("rev-parse", "HEAD"), "tool", contract.ReasonGitlink)
}

// M5 / R6: Eintrag „..“ im Baum → abgewiesen, auch wenn git ihn nie so
// schreiben würde (handgebauter Baum).
func TestLoadRejectsDotDotEntry(t *testing.T) {
	r := newRepo(t, "sha1")
	blob := r.gitIn([]byte("x"), "hash-object", "-w", "--stdin")
	raw, _ := hex.DecodeString(blob)
	inner := append([]byte("100644 ..\x00"), raw...)
	innerID := r.gitIn(inner, "hash-object", "-t", "tree", "--literally", "-w", "--stdin")
	rawInner, _ := hex.DecodeString(innerID)
	outer := append([]byte("40000 tool\x00"), rawInner...)
	outerID := r.gitIn(outer, "hash-object", "-t", "tree", "--literally", "-w", "--stdin")
	commit := r.git("commit-tree", outerID, "-m", "c")
	assertInvalid(t, r, commit, "tool", contract.ReasonBadPath)
}

// M5 / R6: manipuliertes Objekt → abgewiesen. Der Inhalt einer losen
// Objektdatei wird gegen einen anderen getauscht; git selbst merkt das beim
// Lesen nicht, die Senke rechnet den Hash nach.
func TestLoadRejectsTamperedObject(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("tool/main.py", "print('original')\n")
	commit := r.commit()
	blob := r.git("rev-parse", commit+":tool/main.py")
	objPath := filepath.Join(r.gitDir(), "objects", blob[:2], blob[2:])
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	evil := "print('evil')\n"
	_, _ = zw.Write([]byte("blob " + itoa(len(evil)) + "\x00" + evil))
	_ = zw.Close()
	_ = os.Chmod(objPath, 0o644)
	if err := os.WriteFile(objPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	assertInvalid(t, r, commit, "tool", contract.ReasonHashMismatch)
}

func TestLoadRejectsMissingPathAndFile(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("tool/main.py", "x")
	commit := r.commit()
	assertInvalid(t, r, commit, "nope", contract.ReasonBadPath)
	assertInvalid(t, r, commit, "tool/main.py", contract.ReasonBadPath)
}

func TestLoadRejectsOversizedTree(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("tool/big.bin", strings.Repeat("x", MaxTreeBytes+1))
	commit := r.commit()
	assertInvalid(t, r, commit, "tool", contract.ReasonManifestInvalid)
}

func assertInvalid(t *testing.T, r *testRepo, commit, path, reason string) {
	t.Helper()
	_, err := Open(r.gitDir()).Load(context.Background(), commit, path)
	var inv *Invalid
	if !errors.As(err, &inv) {
		t.Fatalf("Load(%s) err = %v, want *Invalid", path, err)
	}
	if inv.Reason != reason {
		t.Fatalf("Reason = %q, want %q", inv.Reason, reason)
	}
}

func itoa(n int) string {
	var b []byte
	for n > 0 || len(b) == 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// SEC-Zweitprüfung (Nebenpunkt): ein Symlink IM Pin-Pfad — ein
// Pfadbestandteil hat Modus 120000 — meldet `symlink`, nicht hash_mismatch.
func TestLoadRejectsSymlinkInPinPath(t *testing.T) {
	tests := []struct{ format, name, link, path, target string }{
		{"sha1", "letzter Bestandteil", "tool", "tool", "real"},
		{"sha1", "mittlerer Bestandteil", "scripts", "scripts/real", "real"},
		{"sha1", "verschachtelt", "a/tool", "a/tool", "../real"},
		{"sha1", "absolutes Ziel", "tool", "tool", "/etc"},
		{"sha256", "letzter Bestandteil", "tool", "tool", "real"},
		{"sha256", "mittlerer Bestandteil", "scripts", "scripts/real", "real"},
	}
	for _, tt := range tests {
		t.Run(tt.format+"/"+tt.name, func(t *testing.T) {
			r := newRepo(t, tt.format)
			r.write("real/main.py", "x")
			r.write("real/real/main.py", "x")
			full := filepath.Join(r.dir, filepath.FromSlash(tt.link))
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			if err := os.Symlink(tt.target, full); err != nil {
				t.Skip("Symlinks nicht verfügbar:", err)
			}
			commit := r.commit()
			if mode := r.git("ls-tree", commit, tt.link); !strings.HasPrefix(mode, "120000") {
				t.Fatalf("Testfehler: %s ist kein Symlink-Eintrag: %q", tt.link, mode)
			}
			assertInvalid(t, r, commit, tt.path, contract.ReasonSymlink)
		})
	}
}
