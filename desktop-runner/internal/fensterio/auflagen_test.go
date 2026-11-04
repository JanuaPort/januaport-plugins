package fensterio

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// SEC-R4 F3: Zwei Fehler der Bibliothek v1.0.19 liefen bei jedem Fensterstart (upstream 7f3bc71 und 9a5551d). Die Kopie
// ruft PutRasterizationScale nicht mehr auf und gibt PutShouldDetectMonitorScaleChanges den Wert als BOOL, nicht als
// Zeiger.
func TestEdgeOhneDieZweiUpstreamFehler(t *testing.T) {
	foundBool := false
	for _, sf := range productive(t, "../edge") {
		ast.Inspect(sf.file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if strings.HasSuffix(callName(x.Fun), "PutRasterizationScale") && !strings.HasSuffix(sf.path, "ICoreWebView2Controller3.go") {
					t.Errorf("%s: Aufruf von PutRasterizationScale (übergibt einen Zeiger statt double)", sf.path)
				}
			case *ast.FuncDecl:
				if x.Name.Name == "PutShouldDetectMonitorScaleChanges" && x.Recv != nil {
					ast.Inspect(x.Body, func(m ast.Node) bool {
						if u, ok := m.(*ast.UnaryExpr); ok && u.Op == token.AND {
							if id, ok := u.X.(*ast.Ident); ok && id.Name == "value" {
								t.Errorf("%s: PutShouldDetectMonitorScaleChanges übergibt &value statt des Werts", sf.path)
							}
						}
						if c, ok := m.(*ast.CallExpr); ok && callName(c.Fun) == "boolToInt" {
							foundBool = true
						}
						return true
					})
				}
			}
			return true
		})
	}
	if !foundBool {
		t.Error("PutShouldDetectMonitorScaleChanges übergibt den Wert nicht über boolToInt")
	}
}

// SEC-R4 F5: Aussagen in Code und Doku müssen zur Messung passen. Diese Sätze waren falsch und kommen nicht zurück.
func TestKeineFalschenAussagen(t *testing.T) {
	forbidden := []string{
		"WebResourceRequested wird nicht benutzt",
		"Abbruch vor der Anfrage",
		"Das Gerätetoken wird dabei nicht gelesen",
	}
	var files []string
	for _, pattern := range []string{"*.go", "../../CLAUDE.md", "../../README.md", "../../CHANGELOG.md"} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "auflagen_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, bad := range forbidden {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s: %q", f, bad)
			}
		}
	}
	if checked < 5 {
		t.Fatalf("zu wenige Dateien geprüft: %d", checked)
	}
}

// SEC-R4 F6: Verschwindet eine App aus der Liste (Widerruf, Entzug), schließt ihr Fenster. Das Bedienfeld löscht dabei
// nichts (B3); die Dateien der WebView sind erst frei, wenn das Fenster zu ist.
type fakeFenster struct {
	name   string
	open   map[string]*fakeFenster
	closed atomic.Int32
	remove bool
}

func (f *fakeFenster) Close() {
	f.closed.Add(1)
	if f.remove {
		delete(f.open, f.name) // wie OnClosed des echten Fensters
	}
}

func TestCloseMissingSchliesstNurVerschwundene(t *testing.T) {
	for _, remove := range []bool{true, false} {
		t.Run(fmt.Sprint("entferntSichSelbst=", remove), func(t *testing.T) {
			open := map[string]*fakeFenster{}
			for _, n := range []string{"kasse", "lager", "bank"} {
				open[n] = &fakeFenster{name: n, open: open, remove: remove}
			}
			keep, gone1, gone2 := open["kasse"], open["lager"], open["bank"]
			got := CloseMissing(open, []string{"kasse"})
			sort.Strings(got)
			if !reflect.DeepEqual(got, []string{"bank", "lager"}) {
				t.Fatalf("geschlossen: %v", got)
			}
			if keep.closed.Load() != 0 || gone1.closed.Load() != 1 || gone2.closed.Load() != 1 {
				t.Fatalf("Close-Aufrufe: %d %d %d", keep.closed.Load(), gone1.closed.Load(), gone2.closed.Load())
			}
		})
	}
	if got := CloseMissing(map[string]*fakeFenster{}, nil); len(got) != 0 {
		t.Fatalf("leere Menge: %v", got)
	}
	// Leere Liste schließt alles.
	open := map[string]*fakeFenster{"a": {name: "a"}}
	if got := CloseMissing(open, nil); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("leere Liste: %v", got)
	}
}

// Das Bedienfeld ruft CloseMissing, wenn eine neue App-Liste ankommt.
func TestBedienfeldSchliesstFensterVerschwundenerApps(t *testing.T) {
	found := false
	for _, f := range []string{"../../cmd/bedienfeld/apps_windows.go", "../../cmd/bedienfeld/main_windows.go"} {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok && callName(c.Fun) == "fensterio.CloseMissing" {
				found = true
			}
			return true
		})
	}
	if !found {
		t.Error("cmd/bedienfeld ruft fensterio.CloseMissing nicht auf")
	}
}

// Verschwindet die Zustandsdatei (Widerruf des Läufers), ist das eindeutig: die Liste wird sofort leer, ohne drei
// Fehlversuche abzuwarten.
func TestWatchLeertDieListeSofortBeiFehlenderDatei(t *testing.T) {
	var mu sync.Mutex
	state := AppList{Names: []string{"a"}}
	var err error
	read := func() (AppList, error) { mu.Lock(); defer mu.Unlock(); return state, err }
	var got []AppList
	var gmu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Watch(ctx, 2*time.Millisecond, read, func(l AppList) { gmu.Lock(); got = append(got, l); gmu.Unlock() })
	waitFor(t, "erste Meldung", func() bool { gmu.Lock(); defer gmu.Unlock(); return len(got) == 1 })
	mu.Lock()
	err = fmt.Errorf("lesen: %w", fs.ErrNotExist)
	mu.Unlock()
	waitFor(t, "leere Liste", func() bool { gmu.Lock(); defer gmu.Unlock(); return len(got) == 2 && len(got[1].Names) == 0 })
}

// Ein anderer Lesefehler leert die Liste erst beim dritten Fehlversuch in Folge (kurze Störungen schließen kein Fenster).
func TestWatchLeertDieListeErstNachDreiFehlern(t *testing.T) {
	var calls atomic.Int32
	read := func() (AppList, error) {
		if calls.Add(1) == 1 {
			return AppList{Names: []string{"a"}}, nil
		}
		return AppList{}, errors.New("gestört")
	}
	emitted := make(chan int32, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Watch(ctx, 2*time.Millisecond, read, func(l AppList) {
		if len(l.Names) == 0 {
			emitted <- calls.Load() - 1 // Zahl der Fehlversuche zum Zeitpunkt der Meldung
		}
	})
	select {
	case n := <-emitted:
		if n < 3 {
			t.Fatalf("Liste nach %d Fehlversuchen geleert, erwartet erst ab 3", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Liste wurde nie geleert")
	}
}

// Die Zahl der gleichzeitig laufenden Nachrichten ist begrenzt (maxInflight): Mehr bleibt unbeantwortet und hängt
// keinen weiteren Aufruf an das Gateway.
func TestHostBegrenztGleichzeitigeNachrichten(t *testing.T) {
	b := newHostBed(t, func(c *HostConfig) { c.MaxGateway = 4 * maxInflight })
	within(t, 5*time.Second, "Receive", func() {
		for i := 0; i < maxInflight+60; i++ {
			b.h.Receive(page, msg(fmt.Sprint("w", i), "werkzeug", `{"name":"x","arguments":{}}`))
		}
	})
	waitFor(t, "Gateway voll bis zur Grenze", func() bool { return b.g.active.Load() == maxInflight })
	time.Sleep(100 * time.Millisecond)
	if b.g.peak.Load() != maxInflight {
		t.Fatalf("gleichzeitig %d Aufrufe, erlaubt sind %d", b.g.peak.Load(), maxInflight)
	}
}
