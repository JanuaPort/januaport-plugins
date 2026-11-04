package fensterio

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// SEC K6b-2 / VM-Befund: Auf dem Thread der Oberfläche muss COM als STA laufen, sonst scheitert die
// WebView2-Umgebung mit „CoInitialize wurde nicht aufgerufen“. Das Bedienfeld ruft EnterUI direkt nach
// LockOSThread im selben Zweig (nicht in einer Goroutine) auf.
func TestBedienfeldInitialisiertCOMAufDemUIThread(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "../../cmd/bedienfeld/main_windows.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lock, enter token.Pos
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		for _, stmt := range fn.Body.List { // nur oberste Ebene: nie in go func(){}
			switch s := stmt.(type) {
			case *ast.ExprStmt:
				if c, ok := s.X.(*ast.CallExpr); ok {
					switch callName(c.Fun) {
					case "runtime.LockOSThread":
						lock = c.Pos()
					case "fensterio.EnterUI":
						enter = c.Pos()
					}
				}
			case *ast.AssignStmt:
				if len(s.Rhs) == 1 {
					if c, ok := s.Rhs[0].(*ast.CallExpr); ok && callName(c.Fun) == "fensterio.EnterUI" {
						enter = c.Pos()
					}
				}
			}
		}
	}
	if lock == token.NoPos {
		t.Fatal("main sperrt den Thread nicht")
	}
	if enter == token.NoPos || enter < lock {
		t.Fatal("main ruft fensterio.EnterUI nicht unmittelbar nach runtime.LockOSThread auf (COM-STA fehlt)")
	}
}

// Der Adapter prüft den Thread (fester Fehler statt Hänger), wartet nur begrenzt und kennt Embed nicht mehr.
func TestAdapterPruefteThreadUndWartetBegrenzt(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "webview_windows.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	literals := map[string]bool{}
	calls := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				s, _ := strconv.Unquote(x.Value)
				literals[s] = true
			}
		case *ast.CallExpr:
			calls[callName(x.Fun)] = true
		}
		return true
	})
	for _, step := range []string{"com apartment", "umgebung", "embed begin", "embed wait"} {
		if !literals[step] {
			t.Errorf("Schritt %q fehlt im Adapter", step)
		}
	}
	if !calls["WaitFor"] {
		t.Error("der Adapter wartet nicht über WaitFor (begrenzt)")
	}
	if calls["s.chromium.Embed"] {
		t.Error("s.chromium.Embed wartet ohne Grenze")
	}
	if !calls["s.chromium.Cancel"] {
		t.Error("nach Frist oder Fehler muss der Start abgebrochen werden (Cancel), damit späte Rückrufe nichts tun")
	}
}
