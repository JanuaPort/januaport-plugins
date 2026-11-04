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

// CharsetMeta legt die Dekodierung fest, bevor irgendetwas anderes im Dokument gelesen wird. Ohne sie könnte
// eine Datei per ESC-Folge und späterer Zeichensatz-Deklaration (etwa iso-2022-jp) die eingesetzte CSP-Meta
// für den Browser unsichtbar machen (SEC-R4 F4). Beim Vorab-Scan gewinnt die erste Deklaration.
func CharsetMeta() string { return `<meta charset="utf-8">` }

// HeadPrefix ist, was der Läufer direkt hinter das <head>-Starttag setzt: erst der Zeichensatz, dann die CSP.
func HeadPrefix() string { return CharsetMeta() + CSPMeta() }
