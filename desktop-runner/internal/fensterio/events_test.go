package fensterio

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

const own = "https://app-demo-pc.jnpt.invalid/"

// SEC K6b-2: NavigationStarting bricht vor der Anfrage ab; auch Redirect und meta refresh kommen hier an,
// weil der Browser sie als eigene Navigation meldet.
func TestNavigationVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, uri string
		want      NavVerdict
	}{
		{"startseite", own + "index.html", NavVerdict{}},
		{"eigeneUnterseite", own + "a/b.html?x=1#f", NavVerdict{}},
		{"fremderHttps", "https://example.org/x?daten=geheim", NavVerdict{Cancel: true, Link: &Link{Host: "example.org", URL: "https://example.org/x?daten=geheim"}}},
		{"eigenesSvg", own + "a.svg", NavVerdict{Cancel: true}},
		{"eigenerPfadOhneHtml", own, NavVerdict{Cancel: true}},
		{"http", "http://example.org/", NavVerdict{Cancel: true}},
		{"file", "file:///C:/Windows/win.ini", NavVerdict{Cancel: true}},
		{"data", "data:text/html,x", NavVerdict{Cancel: true}},
		{"about", "about:blank", NavVerdict{Cancel: true}},
		{"javascript", "javascript:alert(1)", NavVerdict{Cancel: true}},
		{"blob", "blob:https://app-demo-pc.jnpt.invalid/x", NavVerdict{Cancel: true}},
		{"userinfo", "https://user@example.org/", NavVerdict{Cancel: true}},
		{"andereApp", "https://app-andere-pc.jnpt.invalid/index.html", NavVerdict{Cancel: true}},
		{"leer", "", NavVerdict{Cancel: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NavigationVerdict("demo", tc.uri); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%+v, erwartet %+v", got, tc.want)
			}
		})
	}
	if !NavigationVerdict("Demo", own+"index.html").Cancel {
		t.Fatal("ungültiger Appname")
	}
}

// SEC K6b-2: NewWindowRequested ist immer „Handled“; es entsteht nie ein Fenster. Externe Links gehen nur
// über den nativen Ja-Dialog.
func TestNewWindowVerdictIstImmerAbgebrochen(t *testing.T) {
	for _, uri := range []string{own + "index.html", own + "x.svg", "https://example.org/", "http://example.org/", "file:///x", "about:blank", "javascript:alert(1)", "", "data:text/html,x"} {
		if v := NewWindowVerdict("demo", uri); !v.Cancel {
			t.Errorf("Fenster für %q nicht verhindert", uri)
		}
	}
	if v := NewWindowVerdict("demo", "https://example.org/?q=1"); v.Link == nil || v.Link.Host != "example.org" || v.Link.URL != "https://example.org/?q=1" {
		t.Fatalf("externer Link ohne Dialogvorschlag: %+v", v)
	}
	for _, uri := range []string{own + "index.html", "http://example.org/", "file:///x", "javascript:alert(1)", "https://u@example.org/", "https://app-andere-pc.jnpt.invalid/"} {
		if v := NewWindowVerdict("demo", uri); v.Link != nil {
			t.Errorf("Dialog für %q", uri)
		}
	}
}

func TestDownloadVerdictBrichtImmerAb(t *testing.T) {
	if v := DownloadVerdict(); !v.Cancel || v.Link != nil {
		t.Fatalf("%+v", v)
	}
}

// SEC K6b-2: Die Quell-URL der Nachricht geht in MessageAllowed. Eine Nachricht ohne oder mit fremder
// Quelle erreicht die Brücke nie.
func TestMessageSource(t *testing.T) {
	src := MessageSource(own + "index.html")
	if !src.MainFrame || src.URL != own+"index.html" || !fenster.MessageAllowed("demo", src, "status") {
		t.Fatalf("%+v", src)
	}
	for _, u := range []string{"", "about:blank", "https://example.org/", own, own + "x.svg", own + "x.html/../../y"} {
		if fenster.MessageAllowed("demo", MessageSource(u), "status") {
			t.Errorf("Quelle %q zugelassen", u)
		}
	}
}

func TestRunStepsHaeltBeimErstenFehlerAn(t *testing.T) {
	var ran []string
	mk := func(name string, err error) Step {
		return Step{Name: name, Run: func() error { ran = append(ran, name); return err }}
	}
	if err := RunSteps([]Step{mk("a", nil), mk("b", nil)}); err != nil || !reflect.DeepEqual(ran, []string{"a", "b"}) {
		t.Fatal(err, ran)
	}
	ran = nil
	cause := errors.New("QueryInterface C:\\geheim")
	err := RunSteps([]Step{mk("a", nil), mk("DownloadStarting", cause), mk("c", nil)})
	if !errors.Is(err, ErrWebView) || !reflect.DeepEqual(ran, []string{"a", "DownloadStarting"}) {
		t.Fatalf("err=%v ran=%v", err, ran)
	}
	if errors.Is(err, cause) || len(err.Error()) > 40 {
		t.Fatal("Ursache sickert durch")
	}
	var se *StepError
	if !errors.As(err, &se) || se.Step != "DownloadStarting" {
		t.Fatalf("Schritt nicht benannt: %v", err)
	}
}

// Ohne eine der Schnittstellen öffnet das Fenster nicht: Der Adapter nennt jedes Pflichtereignis als Schritt.
func TestAdapterBindetAlleEreignisse(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "webview_windows.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	literals := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, _ := strconv.Unquote(lit.Value)
			literals[s] = true
		}
		return true
	})
	want := RequiredEvents()
	sort.Strings(want)
	if !reflect.DeepEqual(want, []string{"DownloadStarting", "FrameNavigationStarting", "NavigationStarting", "NewWindowRequested", "PermissionRequested", "ProcessFailed", "WebMessageReceived", "WebResourceRequested"}) {
		t.Fatalf("Pflichtereignisse: %v", want)
	}
	for _, ev := range want {
		if !literals["bind "+ev] {
			t.Errorf("webview_windows.go bindet %s nicht als Schritt", ev)
		}
	}
	// Kein stilles Weglaufen: Der Adapter kennt keinen „best effort“-Pfad.
	for _, bad := range []string{"best effort", "ignore", "optional"} {
		if literals[bad] {
			t.Errorf("Literal %q im Adapter", bad)
		}
	}
	_ = filepath.Join
}

// Jede Einstellung der Sollliste hat im Adapter einen Setter; fehlt einer, wäre das Fenster offener als
// vorgesehen. (Der Adapter fällt zusätzlich zur Laufzeit zu, wenn ein Setter fehlt.)
func TestAdapterWendetAlleSettingsAn(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "webview_windows.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	literals := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, _ := strconv.Unquote(lit.Value)
			literals[s] = true
		}
		return true
	})
	for _, set := range fenster.Settings() {
		if !literals[set.Name] {
			t.Errorf("Adapter kennt die Einstellung %s nicht", set.Name)
		}
	}
}

// SEC Fremdserver-Befund: Schicht 1 (WebResourceRequested für alles) und Schicht 2 (Netz im Browserprozess aus)
// sind im Adapter verdrahtet, mit den Entscheidungen aus internal/fenster.
func TestAdapterSperrtRessourcenUndNetz(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "webview_windows.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			calls[callName(c.Fun)] = true
		}
		return true
	})
	for _, want := range []string{"fenster.ResourceAllowed", "fenster.NetworkLockArgs", "wv2com.AddWebResourceRequestedFilter", "wv2com.BlockedResponse",
		"webview2Findings", "envsnap.Start", "wv2com.BrowserProcessID", "processCommandLine", "fenster.BrowserCommandLineProblems"} {
		if !calls[want] {
			t.Errorf("webview_windows.go ruft %s nicht auf", want)
		}
	}
}

// Lauf 10: Der Fokus gehört der Seite. Ohne MoveFocus nach dem Aktivieren des Fensters landet die Tastatureingabe nicht
// in der Seite, bis jemand hineinklickt, und document.hasFocus() bleibt false. Das Fenster gibt ihn beim Anzeigen
// und bei WM_SETFOCUS an die WebView.
func TestSurfaceGibtDerSeiteDenFokus(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "webview_windows.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var onSetFocus, onShow, onShowForeground bool
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			if x.Name.Name == "surfaceProc" {
				ast.Inspect(x.Body, func(m ast.Node) bool {
					if cc, ok := m.(*ast.CaseClause); ok {
						for _, e := range cc.List {
							if id, ok := e.(*ast.Ident); ok && id.Name == "wmSetFocus" {
								ast.Inspect(&ast.BlockStmt{List: cc.Body}, func(k ast.Node) bool {
									if c, ok := k.(*ast.CallExpr); ok && callName(c.Fun) == "s.chromium.Focus" {
										onSetFocus = true
									}
									return true
								})
							}
						}
					}
					return true
				})
			}
		case *ast.CompositeLit:
			// Schritt "show": {"show", func() error { ... s.chromium.Focus() ... }}
			if len(x.Elts) == 2 {
				if lit, ok := x.Elts[0].(*ast.BasicLit); ok && lit.Value == `"show"` {
					ast.Inspect(x.Elts[1], func(k ast.Node) bool {
						if c, ok := k.(*ast.CallExpr); ok && callName(c.Fun) == "s.chromium.Focus" {
							onShow = true
						}
						if c, ok := k.(*ast.CallExpr); ok && callName(c.Fun) == "pForeground.Call" {
							onShowForeground = true
						}
						return true
					})
				}
			}
		}
		return true
	})
	if !onSetFocus {
		t.Error("surfaceProc gibt der WebView bei WM_SETFOCUS den Fokus nicht (s.chromium.Focus)")
	}
	if !onShow {
		t.Error("der Schritt show gibt der WebView den Fokus nicht")
	}
	if !onShowForeground {
		t.Error("der Schritt show holt das neue Fenster nicht in den Vordergrund (SetForegroundWindow): Ohne aktives Fenster hat die Seite keinen Fokus")
	}
}
