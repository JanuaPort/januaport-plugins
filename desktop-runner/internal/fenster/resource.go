package fenster

import (
	"net/url"
	"strings"
)

// NetworkLockArguments sind Schicht 2 gegen Ausleitung (SEC): ein totes Proxy-Ziel, das kein Port ist, den ein
// anderes Konto binden kann (0.0.0.0:0 gibt es nicht), Loopback geht ausdrücklich nicht am Proxy vorbei
// (<-loopback), und DNS ist aus.
const NetworkLockArguments = `--proxy-server=http://0.0.0.0:0 --proxy-bypass-list=<-loopback> --host-resolver-rules="MAP * ~NOTFOUND"`

// SpeculationArguments schalten das spekulative Laden des Browsers aus (SEC Lauf 5: Prerender holte ein
// echtes GET): Prerender und No-State-Prefetch, das Hintergrundnetz und das DNS-Vorabholen. Ob ein Name in der
// eingesetzten Runtime wirkt, belegt die native Sonde, nicht diese Liste.
const SpeculationArguments = `--disable-features=Prerender2,NoStatePrefetch --disable-background-networking --dns-prefetch-disable`

// BrowserArguments sind alle zusätzlichen Argumente des Browserprozesses. Der Kommandozeilenwert ist keine
// harte Grenze: Umgebungsvariablen und Richtlinien können ihn übersteuern. Darum liest der Adapter nach dem
// Start die Kommandozeile des Browserprozesses und verlangt diese Argumente exakt (BrowserCommandLineProblems),
// jede WEBVIEW2_*-Variable ist ein Eingriff, und Schicht 1 (ResourceAllowed im Ereignis WebResourceRequested)
// ist Pflicht daneben. Kein Träger und kein Pfad gehören hierher.
const BrowserArguments = NetworkLockArguments + " " + SpeculationArguments

// NetworkLockArgs liefert NetworkLockArguments als einzelne Argumente.
func NetworkLockArgs() []string {
	return []string{`--proxy-server=http://0.0.0.0:0`, `--proxy-bypass-list=<-loopback>`, `--host-resolver-rules="MAP * ~NOTFOUND"`}
}

// SpeculationArgs liefert SpeculationArguments als einzelne Argumente.
func SpeculationArgs() []string {
	return []string{`--disable-features=Prerender2,NoStatePrefetch`, `--disable-background-networking`, `--dns-prefetch-disable`}
}

// BrowserArgs liefert BrowserArguments als einzelne Argumente.
func BrowserArgs() []string { return append(NetworkLockArgs(), SpeculationArgs()...) }

// BrowserCommandLineProblems prüft die Kommandozeile des Browserprozesses nach Wirkung: Die Proxy-, Umleitungs- und
// Resolver-Argumente müssen genau einmal und mit genau unserem Wert vorkommen, die Schalter für Hintergrundnetz und
// DNS-Vorabholen müssen da sein, das letzte --disable-features muss Prerender2 und NoStatePrefetch enthalten, und
// widersprechende Argumente sind verboten. Die Meldungen nennen nur feste Argumentnamen, nie Inhalt der Zeile.
func BrowserCommandLineProblems(line string) []string {
	values := map[string][]string{}
	args := splitCommandLine(line)
	if len(args) > 0 {
		args = args[1:] // das Programm selbst
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			continue
		}
		key, value, _ := strings.Cut(a, "=")
		values[key] = append(values[key], value)
	}
	var problems []string
	for _, w := range []struct{ key, value string }{
		{"--proxy-server", "http://0.0.0.0:0"},
		{"--proxy-bypass-list", "<-loopback>"},
		{"--host-resolver-rules", "MAP * ~NOTFOUND"},
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
	for _, key := range []string{"--disable-background-networking", "--dns-prefetch-disable"} {
		if len(values[key]) == 0 {
			problems = append(problems, "fehlt "+key)
		}
	}
	for _, key := range []string{"--proxy-pac-url", "--no-proxy-server", "--proxy-auto-detect"} {
		if len(values[key]) != 0 {
			problems = append(problems, "verboten "+key)
		}
	}
	have := map[string]bool{}
	if got := values["--disable-features"]; len(got) > 0 {
		for _, f := range strings.Split(got[len(got)-1], ",") {
			have[strings.TrimSpace(f)] = true // der letzte Wert gewinnt im Browser
		}
	}
	for _, f := range []string{"Prerender2", "NoStatePrefetch"} {
		if !have[f] {
			problems = append(problems, "fehlt "+f+" in --disable-features")
		}
	}
	for _, v := range values["--enable-features"] {
		for _, f := range strings.Split(v, ",") {
			name, _, _ := strings.Cut(strings.TrimSpace(f), ":")
			if name == "Prerender2" || name == "NoStatePrefetch" {
				problems = append(problems, "verboten "+name+" in --enable-features")
			}
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
