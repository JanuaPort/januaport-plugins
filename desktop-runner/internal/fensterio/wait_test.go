package fensterio

import (
	"errors"
	"go/ast"
	"strings"
	"testing"
	"time"
)

// B1: Der Start des WebView wartet nie ohne Grenze. Ohne Rückmeldung endet WaitFor nach der Frist mit dem
// festen Fehler, die Nachrichten laufen dabei weiter (step), und ein Fehler des Starts bricht sofort ab.
func TestWaitForBegrenzt(t *testing.T) {
	t.Run("fertig", func(t *testing.T) {
		n := 0
		err := WaitFor(time.Second, func() { n++ }, func() (bool, error) { return n >= 3, nil })
		if err != nil || n != 3 {
			t.Fatal(err, n)
		}
	})
	t.Run("fehlerBrichtSofortAb", func(t *testing.T) {
		cause := errors.New("CoInitialize wurde nicht aufgerufen")
		start := time.Now()
		err := WaitFor(3*time.Second, func() {}, func() (bool, error) { return false, cause })
		if !errors.Is(err, cause) || time.Since(start) > time.Second {
			t.Fatal(err, time.Since(start))
		}
	})
	t.Run("ohneRueckmeldungEndetNachDerFrist", func(t *testing.T) {
		n := 0
		start := time.Now()
		err := WaitFor(150*time.Millisecond, func() { n++; time.Sleep(5 * time.Millisecond) }, func() (bool, error) { return false, nil })
		if !errors.Is(err, ErrStartFrist) {
			t.Fatalf("Fehler: %v", err)
		}
		if d := time.Since(start); d < 150*time.Millisecond || d > 2*time.Second {
			t.Fatalf("Dauer %v", d)
		}
		if n < 5 {
			t.Fatalf("Nachrichten wurden nur %d-mal verteilt", n)
		}
	})
	t.Run("einmalAbfragenAuchBeiFristNull", func(t *testing.T) {
		if err := WaitFor(0, func() {}, func() (bool, error) { return true, nil }); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("abbruchDurchBeenden", func(t *testing.T) {
		err := WaitFor(3*time.Second, func() {}, func() (bool, error) { return false, ErrBeendet })
		if !errors.Is(err, ErrBeendet) {
			t.Fatal(err)
		}
	})
}

// B1: Die kopierte Edge-Schicht kennt weder Prozessende noch unbegrenzte Schleifen noch Rückkanäle, die das
// Fenster nicht braucht. Jeder Fehlerpfad meldet und kehrt zurück; das Fenster öffnet dann nicht.
func TestEdgeKopieOhneProzessendeUndOhneUnbegrenzteWartezeit(t *testing.T) {
	forbiddenCalls := map[string]string{
		"os.Exit":                             "beendet den Prozess (das Bedienfeld mit Halt)",
		"log.Fatal":                           "beendet den Prozess",
		"log.Fatalf":                          "beendet den Prozess",
		"log.Fatalln":                         "beendet den Prozess",
		"AddWebMessageReceived":               "zweiter Nachrichtenweg samt Echo an die Seite",
		"AddWebResourceRequested":             "Handler mit log.Fatal",
		"AddAcceleratorKeyPressed":            "ungenutzter Handler",
		"AddNavigationCompleted":              "ungenutzter Handler",
		"AddContainsFullScreenElementChanged": "ungenutzter Handler",
	}
	for _, sf := range productive(t, "../edge") {
		ast.Inspect(sf.file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				name := callName(x.Fun)
				for bad, why := range forbiddenCalls {
					if name == bad || strings.HasSuffix(name, "."+bad) {
						t.Errorf("%s: Aufruf von %s: %s", sf.path, bad, why)
					}
				}
			case *ast.FuncDecl:
				if x.Recv != nil && x.Name.Name == "Embed" {
					t.Errorf("%s: Embed wartet ohne Grenze auf Nachrichten; Begin und Ready sind der Weg", sf.path)
				}
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "w32" && x.Sel.Name == "User32GetMessageW" {
					t.Errorf("%s: GetMessage wartet ohne Grenze", sf.path)
				}
			}
			return true
		})
	}
}

func callName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return callName(x.X) + "." + x.Sel.Name
	}
	return ""
}

// Die Frist des Starts ist kurz genug, dass kein Hänger das Bedienfeld lange einfriert.
func TestStartFristBegrenzt(t *testing.T) {
	if startFrist <= 0 || startFrist > 30*time.Second {
		t.Fatalf("startFrist = %v", startFrist)
	}
}
