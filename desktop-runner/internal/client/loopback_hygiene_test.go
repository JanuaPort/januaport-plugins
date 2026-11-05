package client

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func checkLoopbackServer(t *testing.T, path string) {
	t.Helper()
	f, e := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if e != nil {
		t.Fatal(e)
	}
	imports := map[string]string{}
	hasHTTP := false
	server := false
	for _, i := range f.Imports {
		p, _ := strconv.Unquote(i.Path.Value)
		name := p
		if i.Name != nil {
			name = i.Name.Name
		}
		imports[name] = p
		if p == "net/http" {
			hasHTTP = true
			if i.Name != nil {
				t.Error("HTTP darf nicht umbenannt werden")
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := s.X.(*ast.Ident); ok && id.Name == "net" && s.Sel.Name != "Listen" && s.Sel.Name != "TCPAddr" {
				t.Error("Netzclient im Loopback", s.Sel.Name)
			}
			if id, ok := s.X.(*ast.Ident); ok && id.Name == "http" {
				switch s.Sel.Name {
				case "Server", "HandlerFunc", "Request", "ResponseWriter":
				default:
					t.Error("HTTP-Client/ungeprüfter Weg", s.Sel.Name)
				}
			}
		}
		if c, ok := n.(*ast.CompositeLit); ok {
			if s, ok := c.Type.(*ast.SelectorExpr); ok && s.Sel.Name == "Server" {
				fields := map[string]bool{}
				for _, el := range c.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						if id, ok := kv.Key.(*ast.Ident); ok {
							fields[id.Name] = true
							if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Value == "0" {
								t.Error("Nullgrenze", id.Name)
							}
						}
					}
				}
				server = fields["ReadHeaderTimeout"] && fields["MaxHeaderBytes"]
			}
		}
		return true
	})
	if !hasHTTP || !server {
		t.Error("begrenzter net/http-Server fehlt")
	}
	for _, p := range imports {
		if p == "os/exec" || p == "net/http/httputil" || p == "github.com/januaport/januaport-plugins/desktop-runner/internal/client" {
			t.Error("Client im Loopback", p)
		}
	}
}
