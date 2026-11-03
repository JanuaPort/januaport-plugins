package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// This guard also runs on Linux: Windows status must return before acquiring
// the runner lock, opening state, observing the clock or constructing an API.
func TestWindowsStatusBleibtVorStateLockUndNetz(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var command *ast.FuncDecl
	for _, declaration := range f.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == "run" {
			command = fn
		}
	}
	if command == nil {
		t.Fatal("CLI-Einstieg fehlt")
	}
	var status *ast.IfStmt
	for _, statement := range command.Body.List {
		branch, ok := statement.(*ast.IfStmt)
		if !ok {
			continue
		}
		ast.Inspect(branch.Cond, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if ok && literal.Kind == token.STRING {
				value, _ := strconv.Unquote(literal.Value)
				if value == "status" {
					status = branch
				}
			}
			return true
		})
		if status != nil {
			break
		}
	}
	if status == nil || status.Else != nil || len(status.Body.List) == 0 {
		t.Fatal("geschlossener Status-Lesepfad fehlt")
	}
	last, ok := status.Body.List[len(status.Body.List)-1].(*ast.ReturnStmt)
	if !ok || len(last.Results) != 1 {
		t.Fatal("Status kehrt nicht vor dem Läuferpfad zurück")
	}
	value, ok := last.Results[0].(*ast.Ident)
	if !ok || value.Name != "nil" {
		t.Fatal("Status kehrt nicht erfolgreich zurück")
	}
	allowedPrefix := map[string]bool{"len": true, "runServer": true, "platform.Root": true}
	allowedStatus := map[string]bool{"os.Stat": true, "filepath.Join": true, "fmt.Fprintf": true, "platform.Installed": true}
	check := func(statement ast.Stmt, allowed map[string]bool) {
		ast.Inspect(statement, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.ForStmt, *ast.RangeStmt, *ast.GoStmt, *ast.DeferStmt:
				t.Error("Status-Lesepfad darf nicht warten oder Hintergrundarbeit starten")
			case *ast.CallExpr:
				name := ""
				switch fn := node.Fun.(type) {
				case *ast.Ident:
					name = fn.Name
				case *ast.SelectorExpr:
					if pkg, ok := fn.X.(*ast.Ident); ok {
						name = pkg.Name + "." + fn.Sel.Name
					}
				}
				if !allowed[name] {
					t.Errorf("Aufruf außerhalb des Status-Lesevertrags: %s", name)
				}
			}
			return true
		})
	}
	for _, statement := range command.Body.List {
		if statement == status {
			check(&ast.ExprStmt{X: status.Cond}, map[string]bool{"len": true})
			check(status.Body, allowedStatus)
			return
		}
		check(statement, allowedPrefix)
	}
}
