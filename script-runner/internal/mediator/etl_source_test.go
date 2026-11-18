package mediator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// SEC E5 (#1215): Die Eigenschaften des Strom-Clients stehen im Quelltext und
// werden hier gepinnt, damit keine Änderung sie still entfernt:
//   - CheckRedirect ist gesetzt (der Client folgt keiner Umleitung),
//   - Proxy ist ausdrücklich nil (keine Proxy-Umgebung),
//   - nirgends http.ProxyFromEnvironment, http.DefaultClient oder DefaultTransport,
//   - der Pfad ist ein festes Literal und kommt nicht aus der Eingabe.
func TestETLClientSourceInvariants(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "etl.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var clientRedirect, transportProxyNil, fixedPath bool
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && id.Name == "http" {
				switch x.Sel.Name {
				case "ProxyFromEnvironment", "DefaultClient", "DefaultTransport", "Get", "Post", "PostForm", "Head":
					t.Errorf("etl.go nutzt http.%s", x.Sel.Name)
				}
			}
		case *ast.CompositeLit:
			sel, ok := x.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			for _, el := range x.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, _ := kv.Key.(*ast.Ident)
				if key == nil {
					continue
				}
				switch {
				case sel.Sel.Name == "Client" && key.Name == "CheckRedirect":
					clientRedirect = true
				case sel.Sel.Name == "Transport" && key.Name == "Proxy":
					if id, ok := kv.Value.(*ast.Ident); ok && id.Name == "nil" {
						transportProxyNil = true
					}
				}
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING && x.Value == `"/api/etl/stream"` {
				fixedPath = true
			}
		}
		return true
	})
	if !clientRedirect {
		t.Error("http.Client ohne CheckRedirect")
	}
	if !transportProxyNil {
		t.Error("http.Transport ohne ausdrückliches Proxy: nil")
	}
	if !fixedPath {
		t.Error("fester Pfad /api/etl/stream fehlt")
	}
}

// Das Token verlässt den vermittler nur im Authorization-Header: etl.go ruft
// den Logger nicht mit Token, Zeilen oder Fehlertexten auf.
func TestETLSourceNeverLogsErrorsOrTokens(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "etl.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !map[string]bool{"Debug": true, "Info": true, "Warn": true, "Error": true}[sel.Sel.Name] {
			return true
		}
		for _, a := range call.Args {
			ast.Inspect(a, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok && (id.Name == "err" || strings.Contains(strings.ToLower(id.Name), "token") || strings.Contains(strings.ToLower(id.Name), "body")) {
					t.Errorf("etl.go: Logeintrag mit %q", id.Name)
				}
				return true
			})
		}
		return true
	})
}
