package fenster

import (
	"reflect"
	"strings"
	"testing"
)

// SEC-R4 F2: Chromium nimmt unter Windows -x, /x und beliebige Groß- und Kleinschreibung als Schalter an. Die Prüfung
// normalisiert den Schlüssel (Präfix --, - oder / abschneiden, klein schreiben), bevor sie zählt und vergleicht.
func TestBrowserCommandLineAndereSchreibweisen(t *testing.T) {
	for _, tc := range []struct {
		name string
		add  string
		want string
	}{
		{"einBindestrichKeinProxy", ` -no-proxy-server`, "verboten --no-proxy-server"},
		{"schraegstrichKeinProxy", ` /no-proxy-server`, "verboten --no-proxy-server"},
		{"grossKeinProxy", ` --NO-PROXY-SERVER`, "verboten --no-proxy-server"},
		{"grossProxyDirekt", ` --PROXY-SERVER=direct://`, "doppelt --proxy-server"},
		{"einBindestrichProxyDirekt", ` -proxy-server=direct://`, "doppelt --proxy-server"},
		{"schraegstrichProxyDirekt", ` /proxy-server=direct://`, "doppelt --proxy-server"},
		{"einBindestrichResolver", ` -host-resolver-rules=MAP`, "doppelt --host-resolver-rules"},
		{"grossResolver", ` --HOST-RESOLVER-RULES=MAP`, "doppelt --host-resolver-rules"},
		{"grossPac", ` --Proxy-Pac-Url=http://x/p`, "verboten --proxy-pac-url"},
		{"grossAutoErkennung", ` /PROXY-AUTO-DETECT`, "verboten --proxy-auto-detect"},
		{"einBindestrichWebrtc", ` -webrtc-ip-handling-policy=default`, "doppelt --webrtc-ip-handling-policy"},
		{"grossWebrtc", ` --WEBRTC-IP-HANDLING-POLICY=default`, "doppelt --webrtc-ip-handling-policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found := false
			for _, p := range BrowserCommandLineProblems(goodLine + tc.add) {
				if p == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("%q fehlt: %v", tc.want, BrowserCommandLineProblems(goodLine+tc.add))
			}
		})
	}
	// Unsere eigenen Argumente in anderer Schreibweise zählen als vorhanden (Schlüssel normalisiert, Wert exakt).
	alt := strings.Replace(goodLine, "--proxy-server=", "-PROXY-SERVER=", 1)
	if p := BrowserCommandLineProblems(alt); len(p) != 0 {
		t.Fatalf("Schreibweise des eigenen Arguments: %v", p)
	}
	// Positionsargumente (kein Schalter) werden nicht als Schalter gelesen.
	if p := BrowserCommandLineProblems(goodLine + ` datei.html C:/x/y --nicht-unser`); len(p) != 0 {
		t.Fatalf("Positionsargument oder fremder Schalter meldet: %v", p)
	}
}

// Die neue Pflicht zu WebRTC: genau einmal, genau mit unserem Wert.
func TestBrowserCommandLineWebrtcPflicht(t *testing.T) {
	without := strings.Replace(goodLine, "--webrtc-ip-handling-policy=disable_non_proxied_udp ", "", 1)
	if without == goodLine {
		t.Fatal("goodLine trägt das WebRTC-Argument nicht")
	}
	for name, tc := range map[string]struct{ line, want string }{
		"fehlt":      {without, "fehlt --webrtc-ip-handling-policy"},
		"abweichend": {strings.Replace(goodLine, "disable_non_proxied_udp", "default", 1), "abweichend --webrtc-ip-handling-policy"},
		"doppelt":    {goodLine + ` --webrtc-ip-handling-policy=default`, "doppelt --webrtc-ip-handling-policy"},
	} {
		t.Run(name, func(t *testing.T) {
			ok := false
			for _, p := range BrowserCommandLineProblems(tc.line) {
				ok = ok || p == tc.want
			}
			if !ok {
				t.Fatalf("%q fehlt: %v", tc.want, BrowserCommandLineProblems(tc.line))
			}
		})
	}
}

// Zerlegung nach den Regeln von CommandLineToArgvW; Backslashes sind nur vor einem Anführungszeichen besonders.
func TestSplitCommandLine(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{`a b  c`, []string{"a", "b", "c"}},
		{`"a b" c`, []string{"a b", "c"}},
		{`a\b c`, []string{`a\b`, "c"}},
		{`C:\Programme\x.exe --a=b`, []string{`C:\Programme\x.exe`, "--a=b"}},
		{`"C:\Programme x\y.exe" z`, []string{`C:\Programme x\y.exe`, "z"}},
		{`"a\"b"`, []string{`a"b`}},
		{`"a\\"`, []string{`a\`}},
		{`a\\\"b`, []string{`a\"b`}},
		{`"a\\" b`, []string{`a\`, "b"}},
		{`--x="MAP * ~N"`, []string{`--x=MAP * ~N`}},
		{`""`, []string{""}},
		{``, nil},
	} {
		if got := splitCommandLine(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: %q, erwartet %q", tc.in, got, tc.want)
		}
	}
}
