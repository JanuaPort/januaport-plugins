package fensterio

import (
	"os"
	"strings"
	"testing"
)

func TestLoginBrowserPruefungVorShell(t *testing.T) {
	b, e := os.ReadFile("webview_windows.go")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "if _, ok := fenster.ExternalLink(raw); !ok {") {
		t.Fatal("ShellExecuteW ohne wirksame URL-Pruefung")
	}
}

func TestLoginNativVerdrahtetUndSperrenBleiben(t *testing.T) {
	for file, fragments := range map[string][]string{
		"login_windows.go":         {"context.WithTimeout(ctx, loginLifetime)", "newLoopback(ctx, flow)", "defer listener.Close()", "OpenInBrowser)", "api.AppSession"},
		"webview_windows.go":       {"o.login = NewLogin", "case loginControlID:", "s.o.login.Start()", "case logoutControlID:", "s.o.login.SignOut(s)", "s.o.login.Pump(s)", "s.resetting || s.closed", "source == \"about:blank\"", "s.quietDone = nil", "initial browser data"},
		"login_surface_windows.go": {"s.view.Navigate(\"about:blank\")", "s.quietDone = done", "s.authTimer = time.AfterFunc", "wmLoginTimeout", "s.resetting = false", "s.view.Navigate(fenster.StartURL(s.o.Name))"},
	} {
		b, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		for _, s := range fragments {
			if !strings.Contains(string(b), s) {
				t.Errorf("%s fehlt %s", file, s)
			}
		}
	}
	b, _ := os.ReadFile("webview_windows.go")
	s := string(b)
	start := strings.Index(s, "func OpenInBrowser(")
	end := strings.Index(s[start:], "\n}") + start
	open := s[start:end]
	if !strings.Contains(open, "fenster.ExternalLink(raw)") || !strings.Contains(open, "windows.ShellExecute(") {
		t.Fatal("Browserstart nicht geprüft/ShellExecuteW")
	}
	b, _ = os.ReadFile("loopback_windows.go")
	if !strings.Contains(string(b), "const loginLifetime = 5 * time.Minute") {
		t.Fatal("Frist")
	}
}
