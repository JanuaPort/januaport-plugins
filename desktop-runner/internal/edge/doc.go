//go:build windows

// Package edge ist eine angepasste Kopie von github.com/wailsapp/go-webview2 (pkg/edge und internal/w32), MIT,
// Lizenztext in LICENSE, Urhebervermerke in NOTICE. Sie ist Sicherheitskern des Läufers: Wer sie ändert, ändert
// ein Stück, das fremde Seiten berührt.
//
// Herkunft (festgenagelt, in go.mod und go.sum dieselbe Fassung):
//
//	Repository  https://github.com/wailsapp/go-webview2
//	Tag         v1.0.19
//	Commit      11c6e567b916645d5fe6df006681f988ac52c13d
//	go.sum      h1:7U3QcDj1PrBPaxJNCui2k1SkWml+Q5kvFUFyTImA6NU=
//
// Abweichungen vom Original zeigt upstream-diff.sh reproduzierbar (nur Lesen):
//
//	docker run --rm -v "$PWD:/w" -w /w golang:1.26.10 sh internal/edge/upstream-diff.sh
//
// Sie sind gewollt und in desktop-runner/CLAUDE.md beschrieben: Importpfade, //go:build windows, gofmt; kein
// os.Exit und kein log.Fatal; Begin/Ready/Cancel/Close statt Embed (begrenzter Start, Aufrufer wartet);
// errorCallback merkt den ersten Fehler; der Controller-Rückruf kehrt bei jedem Fehler zurück und registriert
// nur Berechtigungen und Prozessabsturz; kein panic; ungenutzte Antwort-Erzeugung entfernt. Die Wächter
// stehen in internal/fensterio/wait_test.go und edge_test.go. Bei jedem Läufer-Release wird geprüft, ob
// upstream Sicherheitsfixes veröffentlicht hat (Prüfpunkt in desktop-runner/CLAUDE.md).
package edge
