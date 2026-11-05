package fenster

import (
	"encoding/json"
	"slices"
	"strconv"
)

// SourceVerdict ist SourceAllowed mit festem Grund für die Diagnose der Sonden: "rahmen" (nicht der oberste
// Rahmen) oder "url" (nicht eine .html-Seite der eigenen App). Der Grund ist nie ein Text der Seite.
func SourceVerdict(app string, s Source) (ok bool, reason string) {
	if !s.MainFrame {
		return false, "rahmen"
	}
	if !NavigationAllowed(app, s.URL) {
		return false, "url"
	}
	return true, ""
}

const diagMaxRunes = 200

// DiagText macht einen Text der Seite für Diagnoseausgaben unschädlich: gequotet (keine Steuerzeichen),
// begrenzt, und alles mit Träger wird durch eine Marke ersetzt. Nur für Sonden und Diagnose.
func DiagText(s string) string {
	if EnthaeltTraeger([]byte(s)) {
		return `"<träger>"`
	}
	runes := []rune(s)
	suffix := ""
	if len(runes) > diagMaxRunes {
		runes, suffix = runes[:diagMaxRunes], "…"
	}
	return strconv.Quote(string(runes)) + suffix
}

// ReplySummary fasst eine Antwort der Brücke für die Diagnose zusammen: "ok" oder "fehler=<fester Code>",
// nie ein Ergebnis oder ein anderer Text.
func ReplySummary(b []byte) string {
	var r struct {
		Ok     *bool  `json:"ok"`
		Fehler string `json:"fehler"`
	}
	if json.Unmarshal(b, &r) != nil {
		return "unlesbar"
	}
	if r.Ok != nil && *r.Ok {
		return "ok"
	}
	if slices.Contains(Codes(), Code(r.Fehler)) {
		return "fehler=" + r.Fehler
	}
	return "fehler=?"
}
