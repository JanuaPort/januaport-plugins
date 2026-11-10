package panel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// #1240 F-T: Der Wächter TestFokusWaechter verbot RegisterHotKey überall. Er ist verengt, nicht gestrichen:
// Die Anmeldung gibt es genau einmal, nur in cmd/bedienfeld/hotkey_windows.go, immer mit MOD_NOREPEAT; der
// WM_HOTKEY-Zweig ruft nur das Modell auf, und die Datei wartet nirgends mit Sleep (SEC S3).

const (
	modAltBit       = 0x1
	modControlBit   = 0x2
	modShiftBit     = 0x4
	modWinBit       = 0x8
	modNoRepeatBit  = 0x4000
	wmHotkeyMessage = 0x312
	hotkeyFile      = "hotkey_windows.go"
)

type bedienfeldFile struct {
	name string
	file *ast.File
}

func parseBedienfeld(t *testing.T) []bedienfeldFile {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "cmd", "bedienfeld", "*.go"))
	if err != nil || len(paths) == 0 {
		t.Fatal("Bedienfeld fehlt", err)
	}
	var out []bedienfeldFile
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, bedienfeldFile{filepath.Base(path), f})
	}
	return out
}

func constantsOf(files []bedienfeldFile) map[string]ast.Expr {
	constants := map[string]ast.Expr{}
	for _, f := range files {
		ast.Inspect(f.file, func(n ast.Node) bool {
			if x, ok := n.(*ast.ValueSpec); ok {
				for i, name := range x.Names {
					if i < len(x.Values) {
						constants[name.Name] = x.Values[i]
					}
				}
			}
			return true
		})
	}
	return constants
}

func constValue(t *testing.T, constants map[string]ast.Expr, e ast.Expr) uint64 {
	t.Helper()
	switch x := e.(type) {
	case *ast.BasicLit:
		v, err := strconv.ParseUint(x.Value, 0, 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	case *ast.Ident:
		def, ok := constants[x.Name]
		if !ok {
			t.Fatalf("Konstante %s fehlt", x.Name)
		}
		return constValue(t, constants, def)
	case *ast.BinaryExpr:
		if x.Op == token.OR {
			return constValue(t, constants, x.X) | constValue(t, constants, x.Y)
		}
	case *ast.ParenExpr:
		return constValue(t, constants, x.X)
	}
	t.Fatalf("unprüfbarer Ausdruck %T", e)
	return 0
}

func stringLiterals(f *ast.File, want string) int {
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		if lit, ok := node.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if v, _ := strconv.Unquote(lit.Value); v == want {
				n++
			}
		}
		return true
	})
	return n
}

// RegisterHotKey und UnregisterHotKey kommen je genau einmal vor, und zwar nur in hotkey_windows.go.
func TestHotkeyWaechterAnmeldungNurAnEinerStelle(t *testing.T) {
	files := parseBedienfeld(t)
	for _, api := range []string{"RegisterHotKey", "UnregisterHotKey"} {
		total, inHotkeyFile := 0, 0
		for _, f := range files {
			n := stringLiterals(f.file, api)
			total += n
			if f.name == hotkeyFile {
				inHotkeyFile += n
			} else if n != 0 {
				t.Errorf("%s in %s: erlaubt ist nur %s", api, f.name, hotkeyFile)
			}
		}
		if total != 1 || inHotkeyFile != 1 {
			t.Errorf("%s kommt %d-mal vor (davon %d in %s), erwartet genau einmal in %s", api, total, inHotkeyFile, hotkeyFile, hotkeyFile)
		}
	}
	// Auch eine eigene Auflösung unter anderem Namen ändert die Zahl der Aufrufstellen nicht: Es gibt genau
	// eine Anmeldung (registerHotKey.Call) und eine Abmeldung (unregisterHotKey.Call).
	calls := map[string]int{}
	for _, f := range files {
		ast.Inspect(f.file, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Call" {
					if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "registerHotKey" || id.Name == "unregisterHotKey") {
						calls[id.Name]++
						if f.name != hotkeyFile {
							t.Errorf("%s.Call in %s", id.Name, f.name)
						}
					}
				}
			}
			return true
		})
	}
	if calls["registerHotKey"] != 1 || calls["unregisterHotKey"] != 1 {
		t.Errorf("Aufrufstellen: %v, erwartet je genau eine", calls)
	}
}

// Jede Anmeldung trägt MOD_NOREPEAT (0x4000) und sonst genau Strg+Alt: kein Shift, kein Win, kein frei gewählter Modifier.
func TestHotkeyWaechterNoRepeatUndFesteModifier(t *testing.T) {
	files := parseBedienfeld(t)
	constants := constantsOf(files)
	mods, ok := constants["hotkeyModifiers"]
	if !ok {
		t.Fatal("Konstante hotkeyModifiers fehlt")
	}
	v := constValue(t, constants, mods)
	if v&modNoRepeatBit == 0 {
		t.Error("MOD_NOREPEAT (0x4000) fehlt: Ein gehaltenes Kürzel würde wiederholt auslösen")
	}
	if v&modAltBit == 0 || v&modControlBit == 0 {
		t.Errorf("hotkeyModifiers %#x enthält nicht Strg und Alt", v)
	}
	if v&(modShiftBit|modWinBit) != 0 || v&^uint64(modAltBit|modControlBit|modNoRepeatBit) != 0 {
		t.Errorf("hotkeyModifiers %#x enthält mehr als Strg, Alt und MOD_NOREPEAT", v)
	}
	// Die Anmeldung nimmt genau diese Konstante, keinen anderen Wert.
	used := false
	for _, f := range files {
		if f.name != hotkeyFile {
			continue
		}
		ast.Inspect(f.file, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Call" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "registerHotKey" {
					for _, arg := range c.Args {
						if call, ok := arg.(*ast.CallExpr); ok && len(call.Args) == 1 {
							if inner, ok := call.Args[0].(*ast.Ident); ok && inner.Name == "hotkeyModifiers" {
								used = true
							}
						}
						if inner, ok := arg.(*ast.Ident); ok && inner.Name == "hotkeyModifiers" {
							used = true
						}
					}
				}
			}
			return true
		})
	}
	if !used {
		t.Error("registerHotKey.Call nimmt hotkeyModifiers nicht")
	}
}

// Der WM_HOTKEY-Zweig ruft nur das Modell auf (a.model.PressHotkey, mit time.Now): Er prüft, sendet und öffnet nichts.
func TestHotkeyWaechterZweigRuftNurDasModellAuf(t *testing.T) {
	files := parseBedienfeld(t)
	constants := constantsOf(files)
	if v := constValue(t, constants, constants["wmHotkey"]); v != wmHotkeyMessage {
		t.Fatalf("wmHotkey = %#x, erwartet WM_HOTKEY 0x312", v)
	}
	found := false
	for _, f := range files {
		ast.Inspect(f.file, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			isHotkey := false
			for _, e := range clause.List {
				if id, ok := e.(*ast.Ident); ok && id.Name == "wmHotkey" {
					isHotkey = true
				}
			}
			if !isHotkey {
				return true
			}
			found = true
			for _, stmt := range clause.Body {
				ast.Inspect(stmt, func(m ast.Node) bool {
					call, ok := m.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch fun := call.Fun.(type) {
					case *ast.SelectorExpr:
						if sel, ok := fun.X.(*ast.SelectorExpr); ok {
							if base, ok := sel.X.(*ast.Ident); ok && base.Name == "a" && sel.Sel.Name == "model" && fun.Sel.Name == "PressHotkey" {
								return true
							}
						}
						if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "time" && fun.Sel.Name == "Now" {
							return true
						}
					case *ast.Ident:
						if fun.Name == "int" {
							return true
						}
					}
					t.Errorf("WM_HOTKEY-Zweig in %s ruft mehr als das Modell auf: %T", f.name, call.Fun)
					return true
				})
			}
			return true
		})
	}
	if !found {
		t.Fatal("WM_HOTKEY-Zweig (case wmHotkey) fehlt")
	}
}

// S3: Warten gibt es nur als Zeitgeber im Modell. hotkey_windows.go enthält kein Sleep, weder als Aufruf noch als Name.
func TestHotkeyWaechterKeinSleep(t *testing.T) {
	found := false
	for _, f := range parseBedienfeld(t) {
		if f.name != hotkeyFile {
			continue
		}
		found = true
		ast.Inspect(f.file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if strings.Contains(strings.ToLower(x.Name), "sleep") {
					t.Errorf("%s in %s", x.Name, hotkeyFile)
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING && strings.Contains(strings.ToLower(x.Value), "sleep") {
					t.Errorf("Zeichenkette %s in %s", x.Value, hotkeyFile)
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal(hotkeyFile + " fehlt")
	}
}

// Die Kürzel-Datei ruft weder Fokus-API noch Fenster nach vorn: Das Kürzel öffnet nichts.
func TestHotkeyWaechterDateiOeffnetNichts(t *testing.T) {
	for _, f := range parseBedienfeld(t) {
		if f.name != hotkeyFile {
			continue
		}
		for _, bad := range []string{"SetForegroundWindow", "SetFocus", "SetActiveWindow", "ShowWindow", "CreateWindowExW", "MessageBoxW", "BringWindowToTop", "SwitchToThisWindow"} {
			if stringLiterals(f.file, bad) != 0 {
				t.Errorf("%s in %s", bad, hotkeyFile)
			}
		}
	}
}
