package panel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"testing"
)

// Nach Explorer-Start oder -Neustart weiß das Bedienfeld nicht sicher, ob das Symbol existiert:
// NIM_ADD scheitert bei vorhandenem, NIM_MODIFY bei fehlendem Symbol. Also immer die andere Operation probieren.
func TestTrayUpdateVersuche(t *testing.T) {
	const (
		add    = false // attempt(modify=false) = NIM_ADD
		modify = true  // attempt(modify=true) = NIM_MODIFY
	)
	for _, tc := range []struct {
		name      string
		tray      bool
		results   map[bool]bool // Ergebnis je Operation
		wantCalls []bool
		wantTray  bool
	}{
		{"Symbol da, Ändern gelingt", true, map[bool]bool{modify: true, add: true}, []bool{modify}, true},
		{"Symbol da, Ändern scheitert, Anlegen gelingt", true, map[bool]bool{modify: false, add: true}, []bool{modify, add}, true},
		{"Symbol da, beides scheitert", true, map[bool]bool{modify: false, add: false}, []bool{modify, add}, false},
		{"Symbol da, Ändern gelingt, Anlegen wäre gescheitert", true, map[bool]bool{modify: true, add: false}, []bool{modify}, true},
		{"Symbol unbekannt, Anlegen gelingt", false, map[bool]bool{modify: false, add: true}, []bool{add}, true},
		{"Symbol unbekannt, Anlegen scheitert (existiert schon), Ändern gelingt", false, map[bool]bool{modify: true, add: false}, []bool{add, modify}, true},
		{"Symbol unbekannt, beides scheitert", false, map[bool]bool{modify: false, add: false}, []bool{add, modify}, false},
		{"Symbol unbekannt, Anlegen gelingt, Ändern wäre gelungen", false, map[bool]bool{modify: true, add: true}, []bool{add}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []bool
			got := TrayUpdate(tc.tray, func(m bool) bool {
				calls = append(calls, m)
				return tc.results[m]
			})
			if got != tc.wantTray {
				t.Fatalf("TrayUpdate = %t, erwartet %t", got, tc.wantTray)
			}
			if !slices.Equal(calls, tc.wantCalls) {
				t.Fatalf("Versuche %v, erwartet %v", calls, tc.wantCalls)
			}
		})
	}
}

// render() muss das Ergebnis der Entscheidung als Zustand des Symbols übernehmen; sonst bliebe a.tray nach
// einem geglückten zweiten Versuch falsch und der Hinweis würde nie geprüft.
func TestRenderNutztTrayUpdate(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "cmd", "bedienfeld", "main_windows.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var render *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "render" {
			render = fn
		}
	}
	if render == nil {
		t.Fatal("render fehlt")
	}
	assigned := 0
	ast.Inspect(render, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		lhs, ok := as.Lhs[0].(*ast.SelectorExpr)
		if !ok || lhs.Sel.Name != "tray" || as.Tok != token.ASSIGN {
			return true
		}
		call, ok := as.Rhs[0].(*ast.CallExpr)
		if !ok {
			t.Error("a.tray wird in render nicht aus panel.TrayUpdate gesetzt")
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "TrayUpdate" {
			if len(call.Args) == 2 {
				if arg, ok := call.Args[0].(*ast.SelectorExpr); ok && arg.Sel.Name == "tray" {
					assigned++
				}
			}
		} else {
			t.Error("a.tray wird in render nicht aus panel.TrayUpdate gesetzt")
		}
		return true
	})
	if assigned != 1 {
		t.Fatalf("render setzt a.tray genau einmal aus TrayUpdate(a.tray, …), gefunden: %d", assigned)
	}
}
