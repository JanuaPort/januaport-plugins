package panel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// Zeigt das Bedienfeld einen Windows-Hinweis (NIF_INFO), dann nur hier: eigener NIM_MODIFY, fester Text,
// einmal je neuer Rückfrage, ohne Aktivierung. Die Quelle ist Windows-only; der Wächter liest sie als Text.
func TestHinweisNurAnEinerStelle(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "bedienfeld", "main_windows.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	constants := map[string]ast.Expr{}
	funcs := map[string]*ast.FuncDecl{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ValueSpec:
			for i, name := range x.Names {
				if i < len(x.Values) {
					constants[name.Name] = x.Values[i]
				}
			}
		case *ast.FuncDecl:
			funcs[x.Name.Name] = x
		}
		return true
	})
	notify, render := funcs["notify"], funcs["render"]
	if notify == nil || render == nil {
		t.Fatal("notify oder render fehlt")
	}

	// Feste Werte: NIF_INFO, NIM_MODIFY, NIIF_INFO | NIIF_NOSOUND.
	for name, want := range map[string]uint64{"nifInfo": 0x10, "nimModify": 1, "noticeInfoFlags": 0x11} {
		expr, ok := constants[name]
		if !ok || evalConst(t, constants, expr) != want {
			t.Errorf("Konstante %s fehlt oder hat nicht den Wert %#x", name, want)
		}
	}

	// Inhalt: nur die festen Konstanten, kein Zugriff auf Modell, Dialog oder Rückfragetext.
	fixed := map[string]bool{}
	nimUsed, flagsUsed, calls := false, false, 0
	ast.Inspect(notify, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "panel" {
				fixed[x.Sel.Name] = true
			}
			switch x.Sel.Name {
			case "model", "View", "Dialog", "Text", "Title", "Titel", "Options", "Tooltip", "Menu":
				t.Errorf("Hinweis liest %s: er trägt weder Rückfrage noch Pin", x.Sel.Name)
			}
		case *ast.Ident:
			if x.Name == "noticeInfoFlags" {
				flagsUsed = true
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "notifyIcon" && sel.Sel.Name == "Call" {
					calls++
					if len(x.Args) > 0 {
						if first, ok := x.Args[0].(*ast.Ident); ok && first.Name == "nimModify" {
							nimUsed = true
						}
					}
				}
			}
		}
		return true
	})
	if !fixed["NoticeTitle"] || !fixed["NoticeText"] {
		t.Error("Hinweis nutzt nicht panel.NoticeTitle und panel.NoticeText")
	}
	if !flagsUsed {
		t.Error("InfoFlags ohne noticeInfoFlags (NIIF_NOSOUND)")
	}
	if calls != 1 || !nimUsed {
		t.Error("Hinweis ist genau ein NIM_MODIFY-Aufruf")
	}

	// Einmaligkeit: notify wird genau einmal aufgerufen, in render, unter TakeNotice.
	notifyCalls, guarded := 0, false
	for name, fn := range funcs {
		ast.Inspect(fn, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "notify" {
					notifyCalls++
					if name != "render" {
						t.Errorf("notify wird in %s aufgerufen", name)
					}
				}
			}
			return true
		})
	}
	ast.Inspect(render, func(n ast.Node) bool {
		stmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		takes, notifies := false, false
		ast.Inspect(stmt.Cond, func(c ast.Node) bool {
			if call, ok := c.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "TakeNotice" {
					takes = true
				}
			}
			return true
		})
		ast.Inspect(stmt.Body, func(c ast.Node) bool {
			if call, ok := c.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "notify" {
					notifies = true
				}
			}
			return true
		})
		if takes && notifies {
			guarded = true
		}
		return true
	})
	if notifyCalls != 1 || !guarded {
		t.Errorf("notify muss genau einmal in render unter TakeNotice stehen (Aufrufe: %d, geschützt: %t)", notifyCalls, guarded)
	}
}

func evalConst(t *testing.T, constants map[string]ast.Expr, e ast.Expr) uint64 {
	t.Helper()
	switch x := e.(type) {
	case *ast.BasicLit:
		v, err := strconv.ParseUint(x.Value, 0, 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	case *ast.Ident:
		inner, ok := constants[x.Name]
		if !ok {
			t.Fatalf("Konstante %s fehlt", x.Name)
		}
		return evalConst(t, constants, inner)
	case *ast.BinaryExpr:
		if x.Op == token.OR {
			return evalConst(t, constants, x.X) | evalConst(t, constants, x.Y)
		}
	case *ast.ParenExpr:
		return evalConst(t, constants, x.X)
	}
	t.Fatalf("unprüfbarer Ausdruck %T", e)
	return 0
}
