package fensterio

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modBase = "github.com/januaport/januaport-plugins/desktop-runner/"

type srcFile struct {
	path string
	file *ast.File
}

// productive parst alle Nicht-Test-Dateien eines Verzeichnisses, unabhängig von Build-Tags.
func productive(t *testing.T, dir string) []srcFile {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("keine Quellen in %s: %v", dir, err)
	}
	var out []srcFile
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, srcFile{p, f})
	}
	return out
}

// pkgSelectors liefert alle Namen X aus Ausdrücken pkg.X, wobei pkg ein Import mit dem Pfad ist.
func pkgSelectors(f *ast.File, importPath string) map[string]bool {
	alias := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == importPath {
			alias = filepath.Base(p)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
		}
	}
	out := map[string]bool{}
	if alias == "" {
		return out
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == alias {
				out[sel.Sel.Name] = true
			}
		}
		return true
	})
	return out
}

func importsOf(f *ast.File) []string {
	var out []string
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		out = append(out, p)
	}
	return out
}

// B3: Das Bedienfeld ruft aus internal/client nur Zugangsdaten-Lesen und die AppAPI-Methoden. Alles, was
// die Engine, den Zustand oder den Widerruf berührt, ist verboten; die Namen stehen in keiner erlaubten Liste.
var clientErlaubt = map[string]bool{
	"NewReader": true, "Reader": true, "AppsView": true, "App": true, "NewAPI": true, "API": true, "Trust": true,
	"AppSession": true, "PersonSession": true, "HTTPError": true, "RPCError": true, "ErrRevoked": true, "ErrProtocol": true, "IsOffline": true,
}

func TestFensterioRuftNurLeserUndAppAPI(t *testing.T) {
	for _, sf := range productive(t, ".") {
		for name := range pkgSelectors(sf.file, modBase+"internal/client") {
			if !clientErlaubt[name] {
				t.Errorf("%s: client.%s ist nicht erlaubt (B3)", sf.path, name)
			}
		}
	}
}

// Das Bedienfeld selbst braucht aus client gar nichts; die Tür fensterio kapselt alles.
func TestBedienfeldImportiertClientNicht(t *testing.T) {
	for _, sf := range productive(t, filepath.Join("..", "..", "cmd", "bedienfeld")) {
		for _, imp := range importsOf(sf.file) {
			if imp == modBase+"internal/client" || imp == "net/http" || imp == "os/exec" {
				t.Errorf("%s importiert %s", sf.path, imp)
			}
		}
	}
}

// B2/B3: Das Bedienfeld importiert platform nicht (platform bringt den Client mit); hinter der Tür nutzt fensterio
// daraus nur Wurzel, DPAPI und das private Anlegen des Benutzerdatenordners.
func TestPlatformNurHinterDerTuer(t *testing.T) {
	for _, sf := range productive(t, filepath.Join("..", "..", "cmd", "bedienfeld")) {
		for _, imp := range importsOf(sf.file) {
			if imp == modBase+"internal/platform" {
				t.Errorf("%s importiert platform direkt", sf.path)
			}
		}
	}
	erlaubt := map[string]bool{"Root": true, "DPAPI": true, "MkdirPrivate": true}
	n := 0
	for _, sf := range productive(t, ".") {
		for name := range pkgSelectors(sf.file, modBase+"internal/platform") {
			n++
			if !erlaubt[name] {
				t.Errorf("%s: platform.%s", sf.path, name)
			}
		}
	}
	if n == 0 {
		t.Fatal("kein platform-Zugriff gefunden: Test prüft nichts")
	}
}

// B1, strukturell: Die UI-Thread-Datei(en) rufen weder Gateway noch Brücke noch Pipe-Wrapper noch Lesen
// der Zugangsdaten auf. Sie dürfen nur Receive/Drain/Deliver/Reset/Close (nicht blockierend) benutzen.
func TestBedienfeldUIThreadOhneNetzUndBruecke(t *testing.T) {
	verboten := map[string]bool{
		"Empfangen": true, "Werkzeuge": true, "Werkzeug": true, "AppToken": true, "AppRPC": true, "AppLogin": true, "AppSession": true, "Anhalten": true,
		"Status": true, "Exchange": true, "Credential": true, "Apps": true, "Open": true,
		"NewGateway": true, "NewLokal": true, "NewAPI": true, "NewReader": true, "Do": true, "Get": true, "Post": true,
	}
	files := productive(t, filepath.Join("..", "..", "cmd", "bedienfeld"))
	for _, sf := range files {
		ast.Inspect(sf.file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && verboten[sel.Sel.Name] {
				// Blockierende Arbeit gehört in den Hintergrund-Helfer (fensterio.Runtime/Watch); nur dort.
				t.Errorf("%s: UI-Thread-Code ruft %s", sf.path, sel.Sel.Name)
			}
			return true
		})
	}
}

// B1/B2: Ein Absturz der WebView darf Tray und Pipe nie mitreißen: kein Prozessende, kein Panic, keine
// Abhängigkeit zu panel/panelio, keine Prozessstarts im Fenstercode.
func TestFensterioKeinAbbruchUndKeinPanel(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "..", "cmd", "bedienfeld")} {
		for _, sf := range productive(t, dir) {
			if dir == "." {
				for _, imp := range importsOf(sf.file) {
					if imp == modBase+"internal/panel" || imp == modBase+"internal/panelio" || imp == "os/exec" || imp == "net/http/httputil" {
						t.Errorf("%s importiert %s", sf.path, imp)
					}
				}
			}
			ast.Inspect(sf.file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok {
						name := id.Name + "." + x.Sel.Name
						if name == "os.Exit" || strings.HasPrefix(name, "log.Fatal") || strings.HasPrefix(name, "log.Panic") || name == "syscall.Exit" || (name == "net.Listen" && filepath.Base(sf.path) != "loopback_windows.go") || name == "http.ListenAndServe" {
							t.Errorf("%s: %s", sf.path, name)
						}
					}
				case *ast.CallExpr:
					if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "panic" {
						t.Errorf("%s: panic", sf.path)
					}
				}
				return true
			})
		}
	}
}
