package fenster

import (
	"strings"
	"testing"
)

// Fail-closed (SEC Lauf 5): Nach dem Start wird die Kommandozeile des Browserprozesses gelesen und unsere
// Argumente werden exakt verlangt. Geprüft wird die Wirkung (was der Browser wirklich bekam), nicht eine Liste.
const goodLine = `"C:/Programme/Edge/msedgewebview2.exe" --type=browser --disable-features=msWebOOUI,Foo ` +
	`--proxy-server=http://0.0.0.0:0 --proxy-bypass-list=<-loopback> "--host-resolver-rules=MAP * ~NOTFOUND" ` +
	`--webrtc-ip-handling-policy=disable_non_proxied_udp --embedded-browser-webview=1`

func TestBrowserCommandLineProblems(t *testing.T) {
	if p := BrowserCommandLineProblems(goodLine); len(p) != 0 {
		t.Fatalf("gute Zeile: %v", p)
	}
	// Auch die Form mit dem Anführungszeichen mitten im Argument ist dasselbe Argument.
	if p := BrowserCommandLineProblems(strings.Replace(goodLine, `"--host-resolver-rules=MAP * ~NOTFOUND"`, `--host-resolver-rules="MAP * ~NOTFOUND"`, 1)); len(p) != 0 {
		t.Fatalf("Anführungszeichen im Argument: %v", p)
	}
	for _, tc := range []struct {
		name string
		line string
		want string
	}{
		{"proxyFehlt", strings.Replace(goodLine, "--proxy-server=http://0.0.0.0:0 ", "", 1), "fehlt --proxy-server"},
		{"proxyAnders", strings.Replace(goodLine, "http://0.0.0.0:0", "http://127.0.0.1:1", 1), "abweichend --proxy-server"},
		{"proxyDoppelt", goodLine + ` --proxy-server=direct://`, "doppelt --proxy-server"},
		{"bypassFehlt", strings.Replace(goodLine, "--proxy-bypass-list=<-loopback> ", "", 1), "fehlt --proxy-bypass-list"},
		{"bypassAnders", strings.Replace(goodLine, "<-loopback>", "localhost", 1), "abweichend --proxy-bypass-list"},
		{"resolverFehlt", strings.Replace(goodLine, `"--host-resolver-rules=MAP * ~NOTFOUND" `, "", 1), "fehlt --host-resolver-rules"},
		{"resolverZerrissen", strings.Replace(goodLine, `"--host-resolver-rules=MAP * ~NOTFOUND"`, `--host-resolver-rules=MAP`, 1), "abweichend --host-resolver-rules"},
		{"pacUrl", goodLine + ` --proxy-pac-url=http://x/pac`, "verboten --proxy-pac-url"},
		{"keinProxy", goodLine + ` --no-proxy-server`, "verboten --no-proxy-server"},
		{"autoErkennung", goodLine + ` --proxy-auto-detect`, "verboten --proxy-auto-detect"},
		{"leer", ``, "fehlt --proxy-server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := BrowserCommandLineProblems(tc.line)
			found := false
			for _, got := range p {
				if got == tc.want {
					found = true
				}
				if EnthaeltTraeger([]byte(got)) || strings.ContainsAny(got, `"\`) {
					t.Fatalf("Problemtext trägt Inhalt der Zeile: %q", got)
				}
			}
			if !found {
				t.Fatalf("%q fehlt in %v", tc.want, p)
			}
		})
	}
}

// Die Browserargumente sind genau die Netzsperre. Feature-Schalter gegen spekulatives Laden (Prerender2,
// NoStatePrefetch) wirkten in Runtime 156 nachweislich nicht (Sondenlauf 7) und sind entfernt, ebenso Schalter, die
// keine Messung trägt.
func TestBrowserArgumentsNurNetzsperre(t *testing.T) {
	const net = `--proxy-server=http://0.0.0.0:0 --proxy-bypass-list=<-loopback> --host-resolver-rules="MAP * ~NOTFOUND" --webrtc-ip-handling-policy=disable_non_proxied_udp`
	if strings.Join(NetworkLockArgs(), " ") != net || BrowserArguments != net || strings.Join(BrowserArgs(), " ") != net {
		t.Fatalf("BrowserArguments = %q", BrowserArguments)
	}
	for _, bad := range []string{"disable-features", "enable-features", "Prerender", "NoStatePrefetch", "disable-background-networking", "dns-prefetch-disable"} {
		if strings.Contains(BrowserArguments, bad) {
			t.Fatalf("Browserargumente tragen %q, das keine Messung belegt", bad)
		}
	}
}
