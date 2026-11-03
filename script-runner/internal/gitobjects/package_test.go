package gitobjects

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func golden(t *testing.T) ([]byte, string, string) {
	t.Helper()
	b, err := os.ReadFile("../../contract/golden/storage/valid.gitobjects")
	if err != nil {
		t.Fatal(err)
	}
	var h packageHead
	if err := json.Unmarshal(b[4:4+binary.BigEndian.Uint32(b)], &h); err != nil {
		t.Fatal(err)
	}
	return b, h.Commit, h.Path
}

func TestStoragePackage(t *testing.T) {
	b, commit, path := golden(t)
	if err := Verify(b, commit, path); err != nil {
		t.Fatal(err)
	}
	frames := [][]byte{}
	for rest := b; len(rest) > 0; {
		n := 4 + int(binary.BigEndian.Uint32(rest))
		frames = append(frames, bytes.Clone(rest[:n]))
		rest = rest[n:]
	}
	swapped := append([][]byte{}, frames...)
	swapped[1], swapped[2] = swapped[2], swapped[1]
	corrupt := bytes.Clone(b)
	corrupt[len(corrupt)-1] ^= 1
	for name, data := range map[string][]byte{
		"unsorted": bytes.Join(swapped, nil), "hash": corrupt, "truncated": b[:len(b)-1], "trailing": append(bytes.Clone(b), 0),
		"missing": bytes.Join(frames[:len(frames)-1], nil), "extra": append(bytes.Clone(b), frames[1]...), "header": []byte("secret-canary"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := Verify(data, commit, path); err != ErrHash {
				t.Fatalf("want hash_mismatch, got %v", err)
			}
		})
	}
	for _, p := range []string{"../scripts/demo", "scripts/../demo", "/scripts/demo", "scripts/CON", "scripts/demo/"} {
		if Verify(b, commit, p) == nil {
			t.Fatal("unsafe or wrong path", p)
		}
	}
	if Verify(b, strings.Repeat("0", 64), path) == nil {
		t.Fatal("wrong commit")
	}
	if _, err := digest("sha1", nil); err == nil {
		t.Fatal("SHA-1 enabled")
	}
}

func functions(t *testing.T, path string) map[string]string {
	t.Helper()
	set := token.NewFileSet()
	f, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			var b bytes.Buffer
			if err := format.Node(&b, set, fn); err != nil {
				t.Fatal(err)
			}
			out[fn.Name.Name] = b.String()
		}
	}
	return out
}

// Same checked-copy pattern as desktop-runner. Only the named S4b deltas
// (SHA-256 only, sorted objects, bounded DAG expansion) are normalized.
func TestCopiedContractFunctions(t *testing.T) {
	source := functions(t, "../../../desktop-runner/contract/contract_test.go")
	copied := functions(t, "copied_contract.go")
	names := []string{"validID", "safeName", "safePath", "objectID", "digest", "decodePackage", "canonical", "validUnicodeEscapes", "strict"}
	if len(copied) != len(names) {
		t.Fatal("copied function set changed")
	}
	for _, name := range names {
		want, got := source[name], copied[name]
		if name == "digest" {
			want = want[:strings.Index(want, "\n\tif algo !=")] + "\n\treturn \"\", fmt.Errorf(\"Algorithmus\")\n}"
		}
		if name == "decodePackage" {
			got = strings.Replace(got, "\tprevious := \"\"\n", "", 1)
			got = strings.Replace(got, "\t\tif id <= previous {\n\t\t\treturn fmt.Errorf(\"Objektreihenfolge\")\n\t\t}\n\t\tprevious = id\n", "", 1)
			got = strings.Replace(got, "\tvisits := 0\n", "", 1)
			got = strings.Replace(got, "\t\tvisits++\n", "", 1)
			got = strings.Replace(got, "depth > 256 || visits > 30000", "depth > 256", 1)
		}
		if strings.ReplaceAll(want, "\n\n", "\n") != strings.ReplaceAll(got, "\n\n", "\n") {
			t.Errorf("copy differs: %s", name)
		}
	}
}
