package fensterio

import "strings"

// jnptNames nennt die Umgebungsvariablen, deren Name mit JNPT beginnt (Groß- und Kleinschreibung egal). Nur
// Namen, nie Werte: Sie können Geheimnisse tragen und dürfen nirgends ausgegeben werden.
func jnptNames(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if len(name) >= 4 && strings.EqualFold(name[:4], "jnpt") {
			out = append(out, name)
		}
	}
	return out
}

// webview2Names nennt die Umgebungsvariablen mit Namen WEBVIEW2_*: Sie steuern Browserordner, Argumente und
// Datenordner des WebView2-Laders. Jede gesetzte gilt als Eingriff, das Fenster öffnet dann nicht. Nur Namen.
func webview2Names(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if len(name) >= 9 && strings.EqualFold(name[:9], "webview2_") {
			out = append(out, name)
		}
	}
	return out
}
