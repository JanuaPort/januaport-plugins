package panel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestBedienfeldProcAufloesung(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "cmd", "bedienfeld", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatal("Bedienfeld fehlt", err)
	}
	guardedPipe := false
	for _, path := range files {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				switch id.Name {
				case "MustFindProc", "MustLoadDLL":
					t.Errorf("panikfähige API-Auflösung in %s", path)
				case "panic":
					if !strings.HasSuffix(path, "_test.go") {
						t.Errorf("Panic im Bedienfeld: %s", path)
					}
				}
			}
			if stmt, ok := n.(*ast.IfStmt); ok {
				cond, ok := stmt.Cond.(*ast.BinaryExpr)
				if !ok || cond.Op != token.EQL {
					return true
				}
				left, lok := cond.X.(*ast.Ident)
				right, rok := cond.Y.(*ast.Ident)
				if lok && rok && left.Name == "nativeLoadError" && right.Name == "nil" {
					ast.Inspect(stmt.Body, func(n ast.Node) bool {
						g, ok := n.(*ast.GoStmt)
						if ok {
							sel, ok := g.Call.Fun.(*ast.SelectorExpr)
							if ok {
								pkg, ok := sel.X.(*ast.Ident)
								guardedPipe = ok && pkg.Name == "panelio" && sel.Sel.Name == "Run"
							}
						}
						return true
					})
				}
			}
			return true
		})
	}
	if !guardedPipe {
		t.Fatal("Pipe darf bei fehlender Windows-API die Störung nicht löschen")
	}
}
