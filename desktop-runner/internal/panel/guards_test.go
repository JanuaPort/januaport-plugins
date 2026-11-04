package panel

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// importExceptions bleibt leer (SEC K6b-2 B2): Keine Kantenliste. Stattdessen wird der Wächter je
// Wurzel geschnitten. internal/panel, internal/panelio und internal/pipe bleiben strikt ohne
// net/http, internal/client und freies net. Für cmd/bedienfeld gibt es genau eine benannte Tür:
// cmd/bedienfeld -> internal/fensterio. Hinter dieser Tür gelten die eigenen Wächter von
// internal/fensterio (B3, AST-Test dort); der Wächter steigt für diese Wurzel nicht hinein.
var importExceptions = []struct{ Name, From, To string }{}

const guardBase = "github.com/januaport/januaport-plugins/desktop-runner/"

// doors nennt je Wurzel die einzige Kante, hinter der der strenge Wächter nicht weiterläuft.
var doors = map[string]string{guardBase + "cmd/bedienfeld": guardBase + "internal/fensterio"}

type guardPkg struct {
	ImportPath string
	Imports    []string
}

func loadGraph(t *testing.T) map[string][]string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-json", "./cmd/bedienfeld", "./internal/panel", "./internal/panelio", "./internal/pipe")
	cmd.Dir = filepath.Join("..", "..")
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GOOS=") && !strings.HasPrefix(v, "CGO_ENABLED=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GOOS=windows", "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	graph := map[string][]string{}
	d := json.NewDecoder(bytes.NewReader(out))
	for {
		var p guardPkg
		err = d.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		graph[p.ImportPath] = p.Imports
	}
	return graph
}

// visitRoot läuft den Importgraphen ab und meldet jede verbotene Kante. Die Tür der Wurzel wird
// weder gemeldet noch betreten; sie muss aber tatsächlich genutzt werden (sonst ist sie Altlast).
func visitRoot(t *testing.T, graph map[string][]string, root string) (doorUsed bool) {
	t.Helper()
	door := doors[root]
	seen := map[string]bool{}
	var visit func(string, bool)
	visit = func(p string, throughPipe bool) {
		throughPipe = throughPipe || p == guardBase+"internal/pipe"
		key := p + strconv.FormatBool(throughPipe)
		if seen[key] {
			return
		}
		seen[key] = true
		for _, dep := range graph[p] {
			if p == root && dep == door {
				doorUsed = true
				continue
			}
			allowed := false
			for _, ex := range importExceptions {
				if ex.Name != "" && ex.From == p && ex.To == dep {
					allowed = true
				}
			}
			if !allowed && (dep == "net/http" || dep == guardBase+"internal/client" || dep == guardBase+"internal/fensterio" || (dep == "net" && !throughPipe)) {
				t.Errorf("verbotener Importweg %s -> %s", p, dep)
			}
			visit(dep, throughPipe)
		}
	}
	if _, ok := graph[root]; !ok {
		t.Fatal("Paket fehlt", root)
	}
	visit(root, false)
	return doorUsed
}

func TestPanelImportWaechter(t *testing.T) {
	if len(importExceptions) != 0 {
		t.Fatal("importExceptions muss leer bleiben (B2): Wächter je Wurzel schneiden")
	}
	graph := loadGraph(t)
	for _, root := range []string{guardBase + "internal/panel", guardBase + "internal/panelio", guardBase + "internal/pipe"} {
		if visitRoot(t, graph, root) {
			t.Errorf("%s hat eine Tür", root)
		}
	}
	// cmd/bedienfeld: genau die benannte Tür, sonst strikt.
	if !visitRoot(t, graph, guardBase+"cmd/bedienfeld") {
		t.Error("cmd/bedienfeld nutzt die benannte Tür internal/fensterio nicht")
	}
	if len(doors) != 1 {
		t.Error("mehr als eine Tür")
	}
}

// B2: Hinter der Tür ist alles Netz erlaubt, vor ihr nichts. Die Tür darf nicht von den Modellen
// aus erreichbar sein; das prüft der strenge Lauf oben (internal/fensterio ist dort verboten).
func TestFensterioNurUeberDieTuer(t *testing.T) {
	graph := loadGraph(t)
	importers := []string{}
	for pkg, imports := range graph {
		for _, dep := range imports {
			if dep == guardBase+"internal/fensterio" {
				importers = append(importers, pkg)
			}
		}
	}
	if len(importers) != 1 || importers[0] != guardBase+"cmd/bedienfeld" {
		t.Fatalf("internal/fensterio wird von %v importiert, erlaubt ist nur cmd/bedienfeld", importers)
	}
}

func TestFokusWaechter(t *testing.T) {
	dir := filepath.Join("..", "..", "cmd", "bedienfeld")
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatal("Win32 fehlt", err)
	}
	constants := map[string]ast.Expr{}
	funcs := map[string]*ast.FuncDecl{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if x.Name == "BS_DEFPUSHBUTTON" || x.Name == "SetForegroundWindow" || x.Name == "SetFocus" || x.Name == "SetActiveWindow" {
					t.Error("Fokus-API/Stil", x.Name)
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					v, _ := strconv.Unquote(x.Value)
					for _, bad := range []string{"SetForegroundWindow", "SetFocus", "SetActiveWindow", "RegisterHotKey", "MessageBoxW"} {
						if v == bad {
							t.Error("Fokus-API", v)
						}
					}
				}
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
	}
	var value func(ast.Expr) uint64
	value = func(e ast.Expr) uint64 {
		switch x := e.(type) {
		case *ast.BasicLit:
			v, err := strconv.ParseUint(x.Value, 0, 64)
			if err != nil {
				t.Fatal(err)
			}
			return v
		case *ast.Ident:
			return value(constants[x.Name])
		case *ast.BinaryExpr:
			if x.Op == token.OR {
				return value(x.X) | value(x.Y)
			}
		case *ast.ParenExpr:
			return value(x.X)
		}
		t.Fatalf("unprüfbarer Stil %T", e)
		return 0
	}
	if value(constants["dialogExStyle"])&0x08000000 == 0 || value(constants["buttonStyle"])&0xf != 0 || value(constants["showNoActivate"]) != 4 || value(constants["trayFlags"])&0x10 != 0 {
		t.Fatal("aktivierender Dialogstil")
	}
	f := funcs["openDialog"]
	if f == nil {
		t.Fatal("openDialog fehlt")
	}
	styles := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			for _, arg := range c.Args {
				if id, ok := arg.(*ast.Ident); ok {
					styles[id.Name] = true
				}
			}
		}
		return true
	})
	for _, name := range []string{"dialogExStyle", "buttonStyle", "showNoActivate"} {
		if !styles[name] {
			t.Error("Stil nicht am Adapter verwendet", name)
		}
	}
}

func TestBedienfeldNurGepruefterDial(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "cmd", "bedienfeld", "main_windows.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	dials, locks, wakes := 0, 0, 0
	ast.Inspect(f, func(n ast.Node) bool {
		if x, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := x.X.(*ast.Ident); ok {
				if id.Name == "pipe" && x.Sel.Name == "Dial" {
					dials++
				}
				if id.Name == "runtime" && x.Sel.Name == "LockOSThread" {
					locks++
				}
			}
		}
		if x, ok := n.(*ast.BasicLit); ok && x.Kind == token.STRING {
			v, _ := strconv.Unquote(x.Value)
			if v == "PostMessageW" {
				wakes++
			}
			for _, bad := range []string{"CreateFileW", "CreateNamedPipeW", "WinVerifyTrust", "GetNamedPipeServerProcessId", "OpenProcess", "EDIT"} {
				if v == bad {
					t.Error("zweiter Prüf-/Pipe-Weg oder Edit-Feld", v)
				}
			}
			if strings.Contains(v, `JanuaPort.Bedienfeld.`) {
				t.Error("eigener Pipe-Name", v)
			}
		}
		return true
	})
	if dials != 1 || locks != 1 || wakes != 1 {
		t.Fatal("Dial/UI-Thread/Wake-Anbindung", dials, locks, wakes)
	}
}
