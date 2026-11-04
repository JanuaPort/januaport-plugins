package fenster

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Plan 5, Import-Wächter: Die Fenster-Policy ist reines Go. Sie darf weder einen
// Netzweg, Prozessstart, Dateisystem- noch Windows-Zugriff importieren; so bleibt
// jede Entscheidung auf Linux als Tabellentest prüfbar. Erlaubt ist eine
// ausdrückliche, kurze Liste von Standardpaketen (Allowlist statt Denylist).
var erlaubteImporte = map[string]bool{
	"bytes": true, "context": true, "encoding/json": true, "errors": true, "fmt": true,
	"io": true, "net/url": true, "regexp": true, "slices": true, "sort": true, "strings": true,
	"time": true, "unicode/utf8": true,
}

func TestFensterImportWaechter(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		checked++
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "//go:build") || strings.Contains(string(src), "// +build") {
			t.Errorf("%s: kein plattformabhängiger Code in internal/fenster", f)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !erlaubteImporte[path] {
				t.Errorf("%s importiert %q", f, path)
			}
		}
	}
	if checked < 4 {
		t.Fatal("zu wenige Quelldateien geprüft", checked)
	}
	// Die Allowlist selbst darf die verbotenen Wege nie enthalten.
	for _, bad := range []string{"net/http", "net", "os/exec", "os", "syscall", "unsafe", "golang.org/x/sys/windows", "io/ioutil"} {
		if erlaubteImporte[bad] {
			t.Errorf("Allowlist enthält %q", bad)
		}
	}
	entries, _ := os.ReadDir(".")
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("Unterverzeichnis %s im reinen Paket", e.Name())
		}
	}
}
