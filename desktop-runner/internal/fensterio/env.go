package fensterio

import (
	"fmt"
	"strings"
)

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

// loaderNeutral ist der Zustand, den der Go-Lader im Paket-Init herstellt (webviewloader/env_create.go
// preventEnvAndRegistryOverrides): Diese Variablen sind leer (RELEASE_CHANNEL_PREFERENCE ist 0), damit weder die
// Umgebung noch die Registry etwas überschreiben, weil WebView2 auf Vorhandensein prüft, nicht auf den Wert. Der
// Zustand ist erwartet; die Wirkung prüft zusätzlich die Kommandozeile des Browserprozesses.
var loaderNeutral = map[string]string{
	"WEBVIEW2_PIPE_FOR_SCRIPT_DEBUGGER":     "",
	"WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS": "",
	"WEBVIEW2_RELEASE_CHANNEL_PREFERENCE":   "0",
	"WEBVIEW2_BROWSER_EXECUTABLE_FOLDER":    "",
	"WEBVIEW2_USER_DATA_FOLDER":             "",
}

// webview2Findings nennt die Eingriffe: jedes WEBVIEW2_* in der Momentaufnahme vor dem Lader-Init (start),
// danach (current) jeden anderen Namen und jeden Wert, der nicht der neutrale des Laders ist. Nur Name und
// Wertlänge, nie der Wert.
func webview2Findings(start, current []string) []string {
	var out []string
	for _, kv := range start {
		name, value, _ := strings.Cut(kv, "=")
		if isWebView2(name) {
			out = append(out, fmt.Sprintf("%s(laenge=%d,start)", name, len(value)))
		}
	}
	for _, kv := range current {
		name, value, _ := strings.Cut(kv, "=")
		if !isWebView2(name) {
			continue
		}
		if want, ok := loaderNeutral[strings.ToUpper(name)]; ok && value == want {
			continue
		}
		out = append(out, fmt.Sprintf("%s(laenge=%d,laufend)", name, len(value)))
	}
	return out
}

func isWebView2(name string) bool { return len(name) >= 9 && strings.EqualFold(name[:9], "webview2_") }
