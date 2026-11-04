package fenster

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var appNamePattern = regexp.MustCompile(`^[a-z0-9_]{1,48}$`)

// befehle ist die geschlossene Menge der Brückenbefehle, alphabetisch.
var befehle = []string{"anhalten", "status", "werkzeug", "werkzeuge"}

// Befehle liefert eine Kopie der erlaubten Brückenbefehle.
func Befehle() []string { return slices.Clone(befehle) }

// ValidAppName gilt für Namen wie im Kern (apps.namePattern).
func ValidAppName(name string) bool { return appNamePattern.MatchString(name) }

// AppHost bildet den Appnamen auf den virtuellen Host ab. Der Unterstrich ist
// im Hostnamen unzulässig und wird zu "-"; weil "-" kein Namenszeichen ist,
// kollidieren zwei Apps nie. Die TLD .invalid löst nie auf.
func AppHost(app string) (string, bool) {
	if !ValidAppName(app) {
		return "", false
	}
	return "app-" + strings.ReplaceAll(app, "_", "-") + "-pc.jnpt.invalid", true
}

// NavigationAllowed erlaubt genau https auf dem Host dieser App und nur .html-Seiten: keine
// Normalisierung, kein Port, keine Userinfo, keine Großschreibung, keine
// Steuer- oder Nicht-ASCII-Zeichen. Alles andere (fremde Hosts, file:, data:,
// about:, blob:, javascript:) ist verboten.
func NavigationAllowed(app, raw string) bool {
	host, ok := AppHost(app)
	if !ok {
		return false
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
		// Direkt nach dem Host darf nur ein Pfad folgen: so scheitern Userinfo (@), Port (:),
		// Suffix-Hosts (.evil) und Prozentcodes. Ohne Pfad gibt es keine .html-Seite.
		return false
	}
	if !htmlPath(rest) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == host && u.User == nil && u.Opaque == ""
}

// htmlPath verlangt eine Seite mit der Endung .html (SEC-R4 F2): Nur HTML trägt das CSP-Meta, SVG, JS,
// JSON und alles andere liefe auf der App-Origin ohne Richtlinie. Gemessen wird der Pfad vor ? und #,
// die Endung exakt kleingeschrieben, davor ein nichtleerer Dateiname. Kodierte Trenner (%2f, %5c, %00,
// %3f, %23) verschleiern das Ende des Pfads und werden nicht zugelassen.
func htmlPath(rest string) bool {
	path, _, _ := strings.Cut(rest, "?")
	path, _, _ = strings.Cut(path, "#")
	lower := strings.ToLower(path)
	for _, enc := range []string{"%2f", "%5c", "%00", "%3f", "%23"} {
		if strings.Contains(lower, enc) {
			return false
		}
	}
	base := path[strings.LastIndex(path, "/")+1:]
	return strings.HasSuffix(base, ".html") && len(base) > len(".html")
}

// StartURL ist der Einstieg des Fensters: immer /index.html.
func StartURL(app string) string {
	host, ok := AppHost(app)
	if !ok {
		return ""
	}
	return "https://" + host + "/index.html"
}

// Source beschreibt den Absender einer Seitennachricht.
type Source struct {
	MainFrame bool
	URL       string
}

// SourceAllowed lässt nur den obersten Rahmen auf der Origin der eigenen App zu.
func SourceAllowed(app string, s Source) bool {
	return s.MainFrame && NavigationAllowed(app, s.URL)
}

// MessageAllowed prüft Rahmen, Origin und Befehl.
func MessageAllowed(app string, s Source, befehl string) bool {
	return SourceAllowed(app, s) && slices.Contains(befehle, befehl)
}
