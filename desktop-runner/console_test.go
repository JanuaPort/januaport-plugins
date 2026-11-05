package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Läuft auch unter Linux: Die Entscheidung ist rein und kennt kein Windows.
func TestKonsolenplan(t *testing.T) {
	none := consolePlan{}
	both := consolePlan{Attach: true, Stdout: true, Stderr: true}
	for _, tc := range []struct {
		name                     string
		args                     []string
		stdoutValid, stderrValid bool
		want                     consolePlan
	}{
		{"run nie", []string{"run"}, false, false, none},
		{"run mit gültigen Handles", []string{"run"}, true, true, none},
		{"run mit Zusatzargument bleibt run", []string{"run", "--x"}, false, false, none},
		{"status ohne Handles", []string{"status"}, false, false, both},
		{"status mit Umleitung", []string{"status"}, true, true, none},
		{"pair nur Fehlerkanal fehlt", []string{"pair", "--url", "https://a.example", "--code", "x"}, true, false, consolePlan{Attach: true, Stderr: true}},
		{"install nur Ausgabe fehlt", []string{"install"}, false, true, consolePlan{Attach: true, Stdout: true}},
		{"uninstall ohne Handles", []string{"uninstall"}, false, false, both},
		{"Fehlaufruf ohne Argumente", nil, false, false, both},
		{"Fehlaufruf unbekannter Befehl", []string{"unbekannt"}, false, false, both},
		{"Groß geschriebenes RUN ist ein Fehlaufruf", []string{"RUN"}, false, false, both},
		{"Fehlaufruf mit gültigen Handles", []string{"unbekannt"}, true, true, none},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := planConsole(tc.args, tc.stdoutValid, tc.stderrValid); got != tc.want {
				t.Fatalf("planConsole(%q, %t, %t) = %+v, erwartet %+v", tc.args, tc.stdoutValid, tc.stderrValid, got, tc.want)
			}
		})
	}
}

// Der Anschluss muss vor jeder Ausgabe stehen: erste Anweisung von main().
func TestMainHaengtKonsoleZuerstAn(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range f.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		if len(fn.Body.List) == 0 {
			t.Fatal("main ist leer")
		}
		first, ok := fn.Body.List[0].(*ast.ExprStmt)
		if !ok {
			t.Fatal("main beginnt nicht mit einem Aufruf")
		}
		call, ok := first.X.(*ast.CallExpr)
		if !ok {
			t.Fatal("main beginnt nicht mit einem Aufruf")
		}
		name, ok := call.Fun.(*ast.Ident)
		if !ok || name.Name != "attachConsole" {
			t.Fatal("main ruft attachConsole nicht als Erstes auf")
		}
		return
	}
	t.Fatal("main fehlt")
}
