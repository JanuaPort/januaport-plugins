package client

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func functions(t *testing.T, p string) map[string]string {
	t.Helper()
	set := token.NewFileSet()
	f, e := parser.ParseFile(set, p, nil, 0)
	if e != nil {
		t.Fatal(e)
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			var b bytes.Buffer
			format.Node(&b, set, fn)
			out[fn.Name.Name] = b.String()
		}
	}
	return out
}
func TestCopiedContractFunctions(t *testing.T) {
	source := functions(t, "../../contract/contract_test.go")
	copy := functions(t, "copied_contract.go")
	expected := []string{"validID", "safeName", "safePath", "objectID", "digest", "decodePackage", "canonical", "validUnicodeEscapes"}
	if len(copy) != len(expected) {
		t.Fatal("kopierte Funktionsmenge geändert")
	}
	for _, name := range expected {
		got := copy[name]
		if name == "decodePackage" {
			got = strings.Replace(got, "\tvisits := 0\n", "", 1)
			got = strings.Replace(got, "\t\tvisits++\n", "", 1)
			got = strings.Replace(got, "depth > 256 || visits > 30000", "depth > 256", 1)
		}
		got = strings.ReplaceAll(got, "\n\n", "\n")
		if got != strings.ReplaceAll(source[name], "\n\n", "\n") {
			t.Fatalf("Kopie weicht ab: %s", name)
		}
	}
	a, _ := os.ReadFile("python-allowlist.json")
	b, _ := os.ReadFile("../../contract/python-allowlist.json")
	if !bytes.Equal(a, b) {
		t.Fatal("Allowlist-Kopie")
	}
}
func TestCopiedLibraryFunctions(t *testing.T) {
	for _, v := range []struct {
		source, copy string
		names        []string
	}{
		{"../../../script-runner/internal/libs/lock.go", "copied_lock.go", []string{"normalizeName", "ParseLock", "stripComment", "parseEntry"}},
		{"../../../script-runner/internal/libs/extract.go", "copied_zip.go", []string{"checkStructure", "findEndRecord"}},
	} {
		source := functions(t, v.source)
		copy := functions(t, v.copy)
		expected := len(v.names)
		if v.copy == "copied_lock.go" {
			expected += 2
		}
		if len(copy) != expected {
			t.Fatal("Funktionsmenge", v.copy)
		}
		for _, name := range v.names {
			if source[name] != copy[name] {
				t.Fatal("Kopie weicht ab", name)
			}
		}
	}
}
func TestKeineListenerDownloaderOderPip(t *testing.T) {
	err := filepath.WalkDir("../..", func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() && (d.Name() == "contract" || d.Name() == ".probe") {
			return filepath.SkipDir
		}
		if d.IsDir() || strings.HasSuffix(p, "_test.go") || (!strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, ".py")) {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		s := string(b)
		for _, bad := range []string{"net.Listen", "http.ListenAndServe", "exec.Command(\"pip", "python -m pip", "import pip", "pip.main", "http.Get(", "http.Post("} {
			if strings.Contains(s, bad) {
				t.Errorf("verbotener Weg in %s: %s", p, bad)
			}
		}
		if strings.Contains(s, `"net/http"`) && filepath.Base(p) != "http.go" {
			t.Error("zweiter HTTP-Adapter", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestLoggerDeckelUndFesteCodes(t *testing.T) {
	root := t.TempDir()
	l := Logger{Root: root}
	if l.Write(LogCode(255), 1) != ErrProtocol {
		t.Fatal("freier Code")
	}
	for i := 0; i < 7; i++ {
		if e := os.WriteFile(filepath.Join(root, "runner.log"), bytes.Repeat([]byte("1"), 1<<20), 0600); e != nil {
			t.Fatal(e)
		}
		if e := l.Write(LogStarted, 1); e != nil {
			t.Fatal(e)
		}
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 5 {
		t.Fatal(len(entries))
	}
	for _, d := range entries {
		info, _ := d.Info()
		if info.Size() > 1<<20 {
			t.Fatal("Log zu groß")
		}
	}
}

func TestCopiedCollisionProbe(t *testing.T) {
	a := functions(t, "../../contract/contract_test.go")
	b := functions(t, "copied_collision_test.go")
	if len(b) != 1 || a["TestSHA1Collision"] != b["TestSHA1Collision"] {
		t.Fatal("Kollisionsprobe weicht ab")
	}
}
