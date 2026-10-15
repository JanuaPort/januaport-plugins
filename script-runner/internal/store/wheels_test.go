package store

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

func wheelRepo(t *testing.T, format string) (*testRepo, string) {
	t.Helper()
	r := newRepo(t, format)
	r.write("tool/main.py", "print('hi')\n")
	r.write("tool/jnpt-script.yaml", "name: x\n")
	r.write("tool/requirements.lock", "x\n")
	r.write("tool/wheels/b-1.0-py3-none-any.whl", "WHEEL-B")
	r.write("tool/wheels/a-1.0-py3-none-any.whl", "WHEEL-A")
	return r, r.commit()
}

func invalidReason(t *testing.T, err error) string {
	t.Helper()
	var inv *Invalid
	if !errors.As(err, &inv) {
		t.Fatalf("err = %v, want *Invalid", err)
	}
	return inv.Reason
}

// Der Pin-Ordner ohne wheels/: Load verhält sich wie bisher (byte-gleich),
// wheels/ gehört dort zum Baum.
func TestLoadLegacyStillIncludesWheelsDir(t *testing.T) {
	r, commit := wheelRepo(t, "sha1")
	tree, err := Open(r.gitDir()).Load(context.Background(), commit, "tool")
	if err != nil {
		t.Fatal(err)
	}
	if tree.File("wheels/a-1.0-py3-none-any.whl") == nil || len(tree.Files) != 5 {
		t.Fatalf("Dateien = %v", paths(tree))
	}
	if tree.HasWheelsDir {
		t.Fatal("Load markiert wheels/ als übersprungen")
	}
}

func TestLoadSkippingWheels(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			r, commit := wheelRepo(t, format)
			tree, err := Open(r.gitDir()).LoadSkippingWheels(context.Background(), commit, "tool")
			if err != nil {
				t.Fatal(err)
			}
			if !tree.HasWheelsDir {
				t.Fatal("HasWheelsDir fehlt")
			}
			if got := strings.Join(paths(tree), ","); got != "jnpt-script.yaml,main.py,requirements.lock" {
				t.Fatalf("Dateien = %s", got)
			}
		})
	}
}

func TestLoadSkippingWheelsOnlySkipsTopLevelWheelsDirectory(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("tool/main.py", "x")
	r.write("tool/sub/wheels/inner.txt", "inner")
	r.write("tool/wheelsfile", "wheelsfile")
	commit := r.commit()
	tree, err := Open(r.gitDir()).LoadSkippingWheels(context.Background(), commit, "tool")
	if err != nil {
		t.Fatal(err)
	}
	if tree.HasWheelsDir || tree.File("sub/wheels/inner.txt") == nil || tree.File("wheelsfile") == nil {
		t.Fatalf("Dateien = %v, HasWheelsDir = %v", paths(tree), tree.HasWheelsDir)
	}
	// ... und ein ENTRY „wheels“, das eine Datei ist, bleibt eine Datei.
	r2 := newRepo(t, "sha1")
	r2.write("tool/main.py", "x")
	r2.write("tool/wheels", "ich bin eine Datei")
	tree, err = Open(r2.gitDir()).LoadSkippingWheels(context.Background(), r2.commit(), "tool")
	if err != nil || tree.File("wheels") == nil || tree.HasWheelsDir {
		t.Fatalf("Dateien = %v, err = %v", paths(tree), err)
	}
}

// Die Grenze von 4 MiB gilt für den Code, nicht für die Wheels.
func TestLoadSkippingWheelsIgnoresWheelSize(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("tool/main.py", "x")
	r.write("tool/wheels/big-1.0-py3-none-any.whl", strings.Repeat("x", MaxTreeBytes+10))
	commit := r.commit()
	s := Open(r.gitDir())
	if _, err := s.LoadSkippingWheels(context.Background(), commit, "tool"); err != nil {
		t.Fatalf("LoadSkippingWheels: %v", err)
	}
	// Load (Altbestand) weist den großen Ordner wie bisher ab.
	_, err := s.Load(context.Background(), commit, "tool")
	if got := invalidReason(t, err); got != contract.ReasonManifestInvalid {
		t.Fatalf("Load: Grund %q", got)
	}
}

func TestListWheels(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			r, commit := wheelRepo(t, format)
			refs, err := Open(r.gitDir()).ListWheels(context.Background(), commit, "tool")
			if err != nil {
				t.Fatal(err)
			}
			if len(refs) != 2 || refs[0].Name != "a-1.0-py3-none-any.whl" || refs[1].Name != "b-1.0-py3-none-any.whl" {
				t.Fatalf("Wheels = %+v", refs)
			}
			if want := r.git("rev-parse", commit+":tool/wheels/a-1.0-py3-none-any.whl"); refs[0].BlobID != want {
				t.Fatalf("BlobID = %s, want %s", refs[0].BlobID, want)
			}
		})
	}
}

func TestListWheelsWithoutDirectory(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("tool/main.py", "x")
	refs, err := Open(r.gitDir()).ListWheels(context.Background(), r.commit(), "tool")
	if err != nil || len(refs) != 0 {
		t.Fatalf("Wheels = %+v, err = %v", refs, err)
	}
}

func TestListWheelsRejects(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(r *testRepo)
		reason string
	}{
		{"Unterordner in wheels/", func(r *testRepo) { r.write("tool/wheels/sub/a-1.0-py3-none-any.whl", "x") }, contract.ReasonLibsInvalid},
		{"Datei ohne .whl", func(r *testRepo) { r.write("tool/wheels/a-1.0.tar.gz", "x") }, contract.ReasonLibsInvalid},
		{"README in wheels/", func(r *testRepo) { r.write("tool/wheels/README.md", "x") }, contract.ReasonLibsInvalid},
		{"Symlink in wheels/", func(r *testRepo) {
			if err := os.Symlink("/etc/passwd", filepath.Join(r.dir, "tool", "wheels", "evil-1.0-py3-none-any.whl")); err != nil {
				t.Skip("Symlinks nicht verfügbar:", err)
			}
		}, contract.ReasonSymlink},
		{"zu viele Wheels", func(r *testRepo) {
			for i := 0; i <= MaxWheelFiles; i++ {
				r.write("tool/wheels/p"+itoa(i)+"-1.0-py3-none-any.whl", "x")
			}
		}, contract.ReasonLibsTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRepo(t, "sha1")
			r.write("tool/main.py", "x")
			r.write("tool/wheels/ok-1.0-py3-none-any.whl", "x")
			tt.setup(r)
			_, err := Open(r.gitDir()).ListWheels(context.Background(), r.commit(), "tool")
			if got := invalidReason(t, err); got != tt.reason {
				t.Fatalf("Grund = %q, want %q", got, tt.reason)
			}
		})
	}
}

func TestListWheelsRejectsGitlinkEntry(t *testing.T) {
	r := newRepo(t, "sha1")
	r.write("tool/main.py", "x")
	r.git("add", "-A")
	r.git("update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("1", 40)+",tool/wheels/sub-1.0-py3-none-any.whl")
	r.git("commit", "-q", "-m", "c")
	_, err := Open(r.gitDir()).ListWheels(context.Background(), r.git("rev-parse", "HEAD"), "tool")
	if got := invalidReason(t, err); got != contract.ReasonGitlink {
		t.Fatalf("Grund = %q", got)
	}
}

func TestCopyBlob(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			r, commit := wheelRepo(t, format)
			s := Open(r.gitDir())
			refs, err := s.ListWheels(context.Background(), commit, "tool")
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			n, err := s.CopyBlob(context.Background(), refs[0].BlobID, &buf, 1<<20)
			if err != nil || buf.String() != "WHEEL-A" || n != 7 {
				t.Fatalf("CopyBlob = %q, %d, %v", buf.String(), n, err)
			}
		})
	}
}

// M5 gilt auch für Wheels: ein getauschter Objektinhalt wird beim Lesen
// erkannt (Hash nachgerechnet), nicht erst beim Entpacken.
func TestCopyBlobRejectsTamperedObject(t *testing.T) {
	r, commit := wheelRepo(t, "sha1")
	s := Open(r.gitDir())
	refs, _ := s.ListWheels(context.Background(), commit, "tool")
	blob := refs[0].BlobID
	objPath := filepath.Join(r.gitDir(), "objects", blob[:2], blob[2:])
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	evil := "EVIL-AAA"
	_, _ = zw.Write([]byte("blob " + itoa(len(evil)) + "\x00" + evil))
	_ = zw.Close()
	_ = os.Chmod(objPath, 0o644)
	if err := os.WriteFile(objPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err := s.CopyBlob(context.Background(), blob, &out, 1<<20)
	if got := invalidReason(t, err); got != contract.ReasonHashMismatch {
		t.Fatalf("Grund = %q", got)
	}
}

func TestCopyBlobRejectsMissingAndOversized(t *testing.T) {
	r, commit := wheelRepo(t, "sha1")
	s := Open(r.gitDir())
	refs, _ := s.ListWheels(context.Background(), commit, "tool")
	var out bytes.Buffer
	if _, err := s.CopyBlob(context.Background(), strings.Repeat("1", 40), &out, 1<<20); invalidReason(t, err) != contract.ReasonHashMismatch {
		t.Fatalf("fehlendes Objekt: %v", err)
	}
	if _, err := s.CopyBlob(context.Background(), refs[0].BlobID, &out, 3); invalidReason(t, err) != contract.ReasonLibsTooLarge {
		t.Fatalf("zu groß: %v", err)
	}
	if _, err := s.CopyBlob(context.Background(), "../etc/passwd", &out, 1<<20); err == nil {
		t.Fatal("ungültige Objekt-ID angenommen")
	}
	if _, err := s.CopyBlob(context.Background(), "-rf", &out, 1<<20); err == nil {
		t.Fatal("Objekt-ID mit führendem Bindestrich angenommen")
	}
	// kein Blob: die ID eines Baums
	tree := r.git("rev-parse", commit+":tool")
	if _, err := s.CopyBlob(context.Background(), tree, &out, 1<<20); err == nil {
		t.Fatal("Baum als Blob gelesen")
	}
}

func paths(t Tree) []string {
	var out []string
	for _, f := range t.Files {
		out = append(out, f.Path)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
