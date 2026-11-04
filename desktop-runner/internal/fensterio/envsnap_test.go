package fensterio

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Die Momentaufnahme der Umgebung muss vor dem Paket-Init des Go-Laders laufen (der überschreibt WEBVIEW2_*).
// Go (seit 1.21) initialisiert unabhängige Pakete in der Reihenfolge ihres Importpfads, sobald ihre
// Abhängigkeiten fertig sind. internal/envsnap hängt nur von os ab und sortiert vor github.com/wailsapp/...;
// das hält dieser Test fest. Die native Sonde TestWindowsEnvMomentaufnahmeVorLader belegt es am Prozess.
func TestEnvsnapLaeuftVorDemLader(t *testing.T) {
	const own = "github.com/januaport/januaport-plugins/desktop-runner/internal/envsnap"
	const loader = "github.com/wailsapp/go-webview2/webviewloader"
	if !(own < loader) {
		t.Fatalf("%s sortiert nicht vor %s", own, loader)
	}
	files, err := filepath.Glob("../envsnap/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("keine Quellen: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p != "os" {
				t.Errorf("%s importiert %s: envsnap darf nur os brauchen, sonst initialisiert es später", f, p)
			}
		}
	}
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go (\d+)\.(\d+)`).FindStringSubmatch(string(mod))
	if m == nil {
		t.Fatal("go-Direktive fehlt")
	}
	minor, _ := strconv.Atoi(m[2])
	if m[1] == "1" && minor < 21 {
		t.Fatalf("go %s.%s: Die Initialisierungsreihenfolge nach Importpfad gilt erst ab 1.21", m[1], m[2])
	}
}
