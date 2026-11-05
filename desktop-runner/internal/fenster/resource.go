package fenster

import (
	"net/url"
	"strings"
)

// NetworkLockArguments sind Schicht 2 gegen Ausleitung (SEC): ein totes Proxy-Ziel, das kein Port ist, den ein
// anderes Konto binden kann (0.0.0.0:0 gibt es nicht), Loopback geht ausdrücklich nicht am Proxy vorbei
// (<-loopback), DNS ist aus, und die Richtlinie für WebRTC lässt nur Verkehr über den Proxy zu.
const NetworkLockArguments = `--proxy-server=http://0.0.0.0:0 --proxy-bypass-list=<-loopback> --host-resolver-rules="MAP * ~NOTFOUND" --webrtc-ip-handling-policy=disable_non_proxied_udp`

// BrowserArguments sind alle zusätzlichen Argumente des Browserprozesses: die Netzsperre, sonst nichts. Der
// Kommandozeilenwert ist keine harte Grenze: Umgebungsvariablen und Richtlinien können ihn übersteuern. Darum liest der
// Adapter nach dem Start die Kommandozeile des Browserprozesses und verlangt diese Argumente exakt
// (BrowserCommandLineProblems), und Schicht 1 (ResourceAllowed im Ereignis WebResourceRequested) ist Pflicht daneben.
// Feature-Schalter gegen spekulatives Laden (Prerender2, NoStatePrefetch) wirkten in Runtime 156 nachweislich nicht
// (Sondenlauf 7: prerender holte ein GET), und für --disable-background-networking und --dns-prefetch-disable trägt keine
// Messung; beides ist entfernt. Eine Übersteuerung, die nicht in der Kommandozeile sichtbar wird (Richtlinie,
// Registry), erkennt die Prüfung nicht. Kein Träger und kein Pfad gehören hierher.
const BrowserArguments = NetworkLockArguments

// NetworkLockArgs liefert NetworkLockArguments als einzelne Argumente.
func NetworkLockArgs() []string {
	return []string{`--proxy-server=http://0.0.0.0:0`, `--proxy-bypass-list=<-loopback>`, `--host-resolver-rules="MAP * ~NOTFOUND"`, `--webrtc-ip-handling-policy=disable_non_proxied_udp`}
}

// BrowserArgs liefert BrowserArguments als einzelne Argumente.
func BrowserArgs() []string { return NetworkLockArgs() }

// BrowserCommandLineProblems prüft die Kommandozeile des Browserprozesses nach Wirkung: Die Proxy-, Umgehungslisten- und
// Resolver-Argumente müssen genau einmal und mit genau unserem Wert vorkommen, widersprechende Proxy-Argumente sind
// verboten. Die Meldungen nennen nur feste Argumentnamen, nie Inhalt der Zeile.
func BrowserCommandLineProblems(line string) []string {
	values := map[string][]string{}
	args := splitCommandLine(line)
	if len(args) > 0 {
		args = args[1:] // das Programm selbst
	}
	for _, a := range args {
		// Chromium nimmt unter Windows --x, -x und /x an und unterscheidet Groß- und Kleinschreibung nicht.
		var rest string
		switch {
		case strings.HasPrefix(a, "--"):
			rest = a[2:]
		case strings.HasPrefix(a, "-"), strings.HasPrefix(a, "/"):
			rest = a[1:]
		default:
			continue
		}
		name, value, _ := strings.Cut(rest, "=")
		key := "--" + strings.ToLower(name)
		values[key] = append(values[key], value)
	}
	var problems []string
	for _, w := range []struct{ key, value string }{
		{"--proxy-server", "http://0.0.0.0:0"},
		{"--proxy-bypass-list", "<-loopback>"},
		{"--host-resolver-rules", "MAP * ~NOTFOUND"},
		{"--webrtc-ip-handling-policy", "disable_non_proxied_udp"},
	} {
		switch got := values[w.key]; {
		case len(got) == 0:
			problems = append(problems, "fehlt "+w.key)
		case len(got) > 1:
			problems = append(problems, "doppelt "+w.key)
		case got[0] != w.value:
			problems = append(problems, "abweichend "+w.key)
		}
	}
	for _, key := range []string{"--proxy-pac-url", "--no-proxy-server", "--proxy-auto-detect"} {
		if len(values[key]) != 0 {
			problems = append(problems, "verboten "+key)
		}
	}
	return problems
}

// splitCommandLine zerlegt eine Windows-Kommandozeile wie CommandLineToArgvW: Leerzeichen trennen außerhalb von
// Anführungszeichen, Anführungszeichen schalten um, Backslashes vor einem Anführungszeichen werden halbiert.
func splitCommandLine(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote, started := false, false
	flush := func() {
		if started {
			args = append(args, cur.String())
			cur.Reset()
			started = false
		}
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			n := 0
			for i < len(s) && s[i] == '\\' {
				n++
				i++
			}
			if i < len(s) && s[i] == '"' {
				cur.WriteString(strings.Repeat("\\", n/2))
				if n%2 == 1 {
					cur.WriteByte('"')
				} else {
					inQuote = !inQuote
				}
			} else {
				cur.WriteString(strings.Repeat("\\", n))
				i--
			}
			started = true
		case c == '"':
			inQuote = !inQuote
			started = true
		case (c == ' ' || c == '\t') && !inQuote:
			flush()
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	flush()
	return args
}

const maxResourceLen = 2048

// ResourceAllowed ist die Entscheidung für jede Anfrage des Browsers (Schicht 1): nur https auf dem eigenen
// virtuellen Host der App, ohne Port, Userinfo, Großschreibung, Steuer- oder Nicht-ASCII-Zeichen, ohne
// kodierte Trenner und ohne ".." im Pfad. Unterressourcen brauchen nur den Host; Dokumente (document) laufen
// zusätzlich über NavigationAllowed und damit die .html-Regel. Alles andere bekommt eine lokale Fehlerantwort.
func ResourceAllowed(app, raw string, document bool) bool {
	host, ok := AppHost(app)
	if !ok || len(raw) > maxResourceLen {
		return false
	}
	if document {
		return NavigationAllowed(app, raw)
	}
	prefix := "https://" + host
	if !strings.HasPrefix(raw, prefix) {
		return false
	}
	for _, r := range raw {
		if r <= 0x20 || r >= 0x7f || r == '\\' {
			return false
		}
	}
	rest := raw[len(prefix):]
	if rest == "" || rest[0] != '/' {
		return false
	}
	path, _, _ := strings.Cut(rest, "?")
	path, _, _ = strings.Cut(path, "#")
	lower := strings.ToLower(path)
	for _, enc := range []string{"%2f", "%5c", "%00", "%3f", "%23"} {
		if strings.Contains(lower, enc) {
			return false
		}
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return false
		}
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == host && u.User == nil && u.Opaque == ""
}
