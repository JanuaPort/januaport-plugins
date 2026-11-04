package fenster

// CSP ist die einzige Inhaltsrichtlinie der App-Seiten. Der virtuelle Host des
// Fensters setzt keine Header, deshalb setzt der Läufer sie beim Ablegen als Meta-Element in jede
// HTML-Datei ein (CSPMeta). Die Seite spricht nie selbst mit einem Netz: Alle
// Werkzeugaufrufe laufen über die Brücke, daher connect-src 'none'.
const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'none'; base-uri 'none'; object-src 'none'; form-action 'none'"

// CSPMeta ist das Meta-Element, das der Läufer als erstes Element in jedes <head> einsetzt.
func CSPMeta() string {
	return `<meta http-equiv="Content-Security-Policy" content="` + CSP + `">`
}
