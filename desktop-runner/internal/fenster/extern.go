package fenster

import (
	"net/url"
	"strconv"
	"strings"
)

// Decision ist die Entscheidung über ein Navigationsziel der Seite.
type Decision int

const (
	// DecisionBlock: nichts passiert, die Seite bleibt.
	DecisionBlock Decision = iota
	// DecisionAllow: Ziel ist eine .html-Seite dieser App.
	DecisionAllow
	// DecisionPrompt: https-Ziel außerhalb; nur nach einem nativen Ja-Dialog mit Host im Browser öffnen.
	DecisionPrompt
)

const maxLinkLen = 2048

// ExternalLink prüft ein Ziel außerhalb der App: ausschließlich https, ohne Userinfo, ohne Steuer-,
// Nicht-ASCII- und Leerzeichen, mit einfachem DNS-Namen (Buchstaben, Ziffern, Bindestrich, Punkt) und
// optionalem numerischen Port. Das Ergebnis ist der Host (mit Port), den der Dialog dem Menschen zeigt.
func ExternalLink(raw string) (host string, ok bool) {
	if len(raw) > maxLinkLen || !strings.HasPrefix(raw, "https://") {
		return "", false
	}
	for _, r := range raw {
		if r <= 0x20 || r >= 0x7f || r == '\\' {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Host == "" {
		return "", false
	}
	// Direkt nach der Authority darf nur Pfad, Query oder Fragment folgen (kein @ im Host).
	rest := strings.TrimPrefix(raw, "https://")
	authority := rest
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority = rest[:i]
	}
	if authority != u.Host || strings.ContainsAny(authority, "@%[]") {
		return "", false
	}
	name, port, hasPort := strings.Cut(authority, ":")
	if hasPort {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", false
		}
	}
	if !dnsName(name) {
		return "", false
	}
	return authority, true
}

func dnsName(s string) bool {
	if s == "" || len(s) > 253 || s[0] == '.' || s[len(s)-1] == '.' {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

// Decide ordnet ein Navigationsziel ein. Eigene .html-Seiten sind erlaubt, andere https-Ziele brauchen
// den Dialog, alles Übrige (http, file, data, about, blob, javascript, Userinfo, die virtuellen Hosts
// anderer Apps, eigene Nicht-HTML-Pfade) wird blockiert.
func Decide(app, raw string) (Decision, string) {
	if NavigationAllowed(app, raw) {
		return DecisionAllow, ""
	}
	if !ValidAppName(app) {
		return DecisionBlock, ""
	}
	host, ok := ExternalLink(raw)
	if !ok || strings.HasSuffix(strings.SplitN(host, ":", 2)[0], ".invalid") {
		return DecisionBlock, ""
	}
	return DecisionPrompt, host
}

// WindowTitle ist der Fenstertitel.
func WindowTitle(app string) string { return "JanuaPort – " + app }

// TestBarText steht in der nativen Leiste auf Testgeräten.
const TestBarText = "Test – Testgerät"

// LinkPromptText fragt, ob ein Link im Standardbrowser geöffnet werden soll; sichtbar ist nur der Host.
func LinkPromptText(host string) string {
	return "Diesen Link im Browser öffnen?\n\n" + host
}

// BarHeight ist die Höhe der nativen Leiste in Pixeln.
const BarHeight = 28

// Rect ist ein Rechteck in Fensterkoordinaten.
type Rect struct{ X, Y, W, H int }

// Layout teilt die Arbeitsfläche: Die WebView bekommt einen Bereich, nicht fest die ganze Fläche. Mit
// Leiste liegt sie oben, die WebView darunter.
func Layout(w, h int, bar bool) (view, barBox Rect) {
	if w <= 0 || h <= 0 {
		return Rect{}, Rect{}
	}
	if !bar {
		return Rect{0, 0, w, h}, Rect{}
	}
	bh := min(BarHeight, h)
	barBox = Rect{0, 0, w, bh}
	if h > bh {
		view = Rect{0, bh, w, h - bh}
	}
	return view, barBox
}
