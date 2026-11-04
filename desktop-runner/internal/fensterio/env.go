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
