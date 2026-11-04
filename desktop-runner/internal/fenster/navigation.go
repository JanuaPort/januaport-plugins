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

// NavigationAllowed erlaubt genau https auf dem Host dieser App: keine
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
	// Direkt nach dem Host darf nur Pfad, Query oder Fragment folgen: so
	// scheitern Userinfo (@), Port (:), Suffix-Hosts (.evil) und Prozentcodes.
	if rest := raw[len(prefix):]; rest != "" && rest[0] != '/' && rest[0] != '?' && rest[0] != '#' {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == host && u.User == nil && u.Opaque == ""
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
