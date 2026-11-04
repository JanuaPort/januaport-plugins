package fenster

import (
	"net/url"
	"strings"
)

// BrowserArguments sind die zusätzlichen Argumente des Browserprozesses (Schicht 2 gegen Ausleitung, SEC):
// ein totes Proxy-Ziel, das kein Port ist, den ein anderes Konto binden kann (0.0.0.0:0 gibt es nicht),
// Loopback geht ausdrücklich nicht am Proxy vorbei (<-loopback>), und DNS ist aus. Der Kommandozeilenwert ist
// keine harte Grenze: Umgebungsvariablen und Richtlinien können ihn übersteuern. Deshalb ist Schicht 1
// (ResourceAllowed im Ereignis WebResourceRequested) Pflicht und NavigationStarting die dritte Schicht.
// Kein Träger und kein Pfad gehören hierher.
const BrowserArguments = `--proxy-server=http://0.0.0.0:0 --proxy-bypass-list=<-loopback> --host-resolver-rules="MAP * ~NOTFOUND"`

// BrowserArgs liefert BrowserArguments als einzelne Argumente.
func BrowserArgs() []string {
	return []string{`--proxy-server=http://0.0.0.0:0`, `--proxy-bypass-list=<-loopback>`, `--host-resolver-rules="MAP * ~NOTFOUND"`}
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
