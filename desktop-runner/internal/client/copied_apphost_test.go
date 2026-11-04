package client

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// vars liefert den formatierten Text der Variablendeklarationen einer Datei.
func vars(t *testing.T, path string) map[string]string {
	t.Helper()
	set := token.NewFileSet()
	f, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			var b bytes.Buffer
			format.Node(&b, set, vs)
			out[vs.Names[0].Name] = b.String()
		}
	}
	return out
}

// Wächter für die markierte Kopie aus script-runner/internal/apphost/package.go:
// Markup-, Geheimnis- und MIME-Regeln bleiben wortgleich; die CSP der apphost
// (HTTP-Header) wird nicht übernommen, der Läufer prüft stattdessen das Meta-Literal.
func TestCopiedApphostFunctions(t *testing.T) {
	const source = "../../../script-runner/internal/apphost/package.go"
	srcFuncs, copyFuncs := functions(t, source), functions(t, "copied_apphost.go")
	if srcFuncs["safeMarkup"] == "" || copyFuncs["safeMarkup"] != srcFuncs["safeMarkup"] {
		t.Fatal("safeMarkup weicht von der Quelle ab")
	}
	// loadPackage ist angepasst (Läufer-Pakete, Fenster-CSP); die Funktionsmenge ist geschlossen.
	if len(copyFuncs) != 2 || copyFuncs["loadAppPackage"] == "" {
		t.Fatal("Funktionsmenge der Kopie geändert", len(copyFuncs))
	}
	srcVars, copyVars := vars(t, source), vars(t, "copied_apphost.go")
	for _, name := range []string{"mimeTypes", "secretPattern"} {
		if srcVars[name] == "" || srcVars[name] != copyVars[name] {
			t.Fatalf("%s weicht von der Quelle ab", name)
		}
	}
	head, err := os.ReadFile("copied_apphost.go")
	if err != nil || !strings.HasPrefix(string(head), "// Kopie aus script-runner/internal/apphost/package.go") {
		t.Fatal("Kopfzeile der Kopie fehlt")
	}
	// Die apphost-CSP darf nie in den Läufer wandern.
	if strings.Contains(string(head), "frame-ancestors") || strings.Contains(string(head), "form-action 'self'") {
		t.Fatal("apphost-CSP in der Kopie")
	}
}
