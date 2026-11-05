package fensterio

import (
	"go/ast"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

// SEC (Kopie internal/edge): Das Ende eines Fehlers ist immer ein Rückkehren. Kein panic, kein os.Exit, keine
// Endlosschleife, kein Warten ohne Grenze; der Aufrufer bekommt über Ready den ersten Fehler und macht daraus
// den festen Fehler. Dieser Wächter ergänzt TestEdgeKopieOhneProzessendeUndOhneUnbegrenzteWartezeit.
func TestEdgeKopieOhnePanicEndlosschleifeUndWarten(t *testing.T) {
	for _, sf := range productive(t, "../edge") {
		ast.Inspect(sf.file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				name := callName(x.Fun)
				last := name[strings.LastIndex(name, ".")+1:]
				switch {
				case name == "panic":
					t.Errorf("%s: panic (ein Fehler muss zurückkehren)", sf.path)
				case last == "Sleep" || strings.Contains(last, "Wait"):
					t.Errorf("%s: %s wartet", sf.path, name)
				}
			case *ast.ForStmt:
				if x.Cond == nil && x.Init == nil && x.Post == nil {
					t.Errorf("%s: for ohne Bedingung", sf.path)
				}
			}
			return true
		})
	}
}

// SEC (Kopie internal/edge), Bedingung 3: In den Rückrufen der Umgebung und des Controllers folgt auf jeden
// errorCallback unmittelbar ein return. Sonst liefe der Rückruf mit halbem Zustand weiter (nil-Zeiger) oder
// würde das Fenster doch noch zeigen.
func TestEdgeRueckrufeKehrenNachFehlerZurueck(t *testing.T) {
	checked := 0
	for _, sf := range productive(t, "../edge") {
		if !strings.HasSuffix(sf.path, "chromium.go") {
			continue
		}
		for _, d := range sf.file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || (fn.Name.Name != "EnvironmentCompleted" && fn.Name.Name != "CreateCoreWebView2ControllerCompleted") {
				continue
			}
			checked++
			calls := 0
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				block, ok := n.(*ast.BlockStmt)
				if !ok {
					return true
				}
				for i, st := range block.List {
					es, ok := st.(*ast.ExprStmt)
					if !ok {
						continue
					}
					call, ok := es.X.(*ast.CallExpr)
					if !ok || callName(call.Fun) != "e.errorCallback" {
						continue
					}
					calls++
					if i+1 >= len(block.List) {
						t.Errorf("%s: errorCallback am Blockende ohne return", fn.Name.Name)
						continue
					}
					if _, ok := block.List[i+1].(*ast.ReturnStmt); !ok {
						t.Errorf("%s: auf errorCallback folgt kein return", fn.Name.Name)
					}
				}
				return true
			})
			if calls == 0 {
				t.Errorf("%s: keine Fehlerpfade gefunden, Wächter blind", fn.Name.Name)
			}
		}
	}
	if checked != 2 {
		t.Fatalf("geprüfte Rückrufe: %d", checked)
	}
}

// SEC Bedingung 1: Die Kopie nennt Herkunft, Tag, Commit und Prüfsumme, und die gepinnte Fassung in go.mod ist
// dieselbe. Das Skript upstream-diff.sh zeigt den Unterschied zum Original reproduzierbar.
func TestEdgeKopieNenntUpstream(t *testing.T) {
	doc, err := os.ReadFile("../edge/doc.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	for _, want := range []string{"github.com/wailsapp/go-webview2", "v1.0.19", "upstream-diff.sh", "h1:"} {
		if !strings.Contains(text, want) {
			t.Errorf("doc.go nennt %q nicht", want)
		}
	}
	if !regexp.MustCompile(`\b[0-9a-f]{40}\b`).MatchString(text) {
		t.Error("doc.go nennt keinen Commit-Hash (40 Hex-Zeichen)")
	}
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "github.com/wailsapp/go-webview2 v1.0.19") {
		t.Error("go.mod pinnt eine andere Fassung als die Kopie")
	}
	sum, err := os.ReadFile("../../go.sum")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`h1:[A-Za-z0-9+/=]+`)
	hash := ""
	for _, line := range strings.Split(string(sum), "\n") {
		if strings.HasPrefix(line, "github.com/wailsapp/go-webview2 v1.0.19 h1:") {
			hash = re.FindString(line)
		}
	}
	if hash == "" || !strings.Contains(text, hash) {
		t.Errorf("doc.go nennt die Prüfsumme aus go.sum nicht (%q)", hash)
	}
	if _, err := os.Stat("../edge/upstream-diff.sh"); err != nil {
		t.Errorf("upstream-diff.sh fehlt: %v", err)
	}
}

// Lauf 11: WM_SETFOCUS kam während Close und traf den schon geleerten Controller; das beendete den Prozess. Jede
// Methode der Kopie, die e.controller oder e.webview aufruft, prüft das Feld vorher auf nil (Close leert den
// Controller, Ereignisse und Fensternachrichten kommen trotzdem noch). Methoden ohne Prüfung gibt es nicht.
func TestEdgeMethodenPruefenControllerUndWebviewAufNil(t *testing.T) {
	checked := 0
	for _, sf := range productive(t, "../edge") {
		for _, d := range sf.file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil || len(fn.Recv.List) != 1 {
				continue
			}
			switch fn.Name.Name {
			case "EnvironmentCompleted", "CreateCoreWebView2ControllerCompleted", "Close":
				continue // Start und Ende des Controllers; dort gelten die Tests der Rückrufe
			}
			uses := map[string]bool{}
			guarded := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
						if inner, ok := sel.X.(*ast.SelectorExpr); ok {
							if id, ok := inner.X.(*ast.Ident); ok && id.Name == "e" && (inner.Sel.Name == "controller" || inner.Sel.Name == "webview") {
								uses[inner.Sel.Name] = true
							}
						}
					}
				case *ast.BinaryExpr:
					if x.Op == token.EQL {
						if sel, ok := x.X.(*ast.SelectorExpr); ok {
							if id, ok := sel.X.(*ast.Ident); ok && id.Name == "e" {
								if nilID, ok := x.Y.(*ast.Ident); ok && nilID.Name == "nil" {
									guarded[sel.Sel.Name] = true
								}
							}
						}
					}
				}
				return true
			})
			for field := range uses {
				checked++
				if !guarded[field] {
					t.Errorf("%s: %s ruft e.%s auf, ohne es auf nil zu prüfen", sf.path, fn.Name.Name, field)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("keine Methode gefunden, Wächter blind")
	}
}
