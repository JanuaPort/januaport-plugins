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

// Named exceptions; intentionally empty in L4. K6b must name its one exception.
var importExceptions = []struct{ Name, From, To string }{}

func TestPanelImportWaechter(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-json", "./cmd/bedienfeld", "./internal/panel")
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
	type pkg struct {
		ImportPath string
		Imports    []string
	}
	graph := map[string][]string{}
	d := json.NewDecoder(bytes.NewReader(out))
	for {
		var p pkg
		err = d.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		graph[p.ImportPath] = p.Imports
	}
	const base = "github.com/januaport/januaport-plugins/desktop-runner/"
	for _, root := range []string{base + "cmd/bedienfeld", base + "internal/panel"} {
		seen := map[string]bool{}
		var visit func(string, bool)
		visit = func(p string, throughPipe bool) {
			throughPipe = throughPipe || p == base+"internal/pipe"
			key := p + strconv.FormatBool(throughPipe)
			if seen[key] {
				return
			}
			seen[key] = true
			for _, dep := range graph[p] {
				allowed := false
				for _, ex := range importExceptions {
					if ex.Name != "" && ex.From == p && ex.To == dep {
						allowed = true
					}
				}
				if !allowed && (dep == "net/http" || dep == base+"internal/client" || (dep == "net" && !throughPipe)) {
					t.Errorf("verbotener Importweg %s -> %s", p, dep)
				}
				visit(dep, throughPipe)
			}
		}
		if _, ok := graph[root]; !ok {
			t.Fatal("Paket fehlt", root)
		}
		visit(root, false)
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
	if value(constants["dialogExStyle"])&0x08000000 == 0 || value(constants["buttonStyle"])&0xf != 0 || value(constants["showNoActivate"]) != 4 {
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
