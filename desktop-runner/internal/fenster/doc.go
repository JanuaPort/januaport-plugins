// Package fenster ist die reine Entscheidungsschicht des PC-Fensters (K6b,
// JanuaPort/januaport#1048): CSP-Literal, Navigations- und Nachrichtenpolicy,
// Fenster-Einstellungen, strenger Befehlsumschlag und die Brücke zwischen
// Seite, Gateway und lokaler Pipe.
//
// Das Paket ist absichtlich reines Go ohne Netz-, Prozess-, Datei- oder
// Windows-Zugriff (TestFensterImportWaechter). Der WebView2-Adapter, der echte
// HTTP-Adapter und die Pipe hängen als austauschbare Details außen an.
package fenster
