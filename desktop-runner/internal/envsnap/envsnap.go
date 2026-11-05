// Package envsnap hält die Umgebung des Prozesses fest, wie sie beim Start war. Es hängt nur von os ab und
// sortiert vor github.com/wailsapp/...: Go initialisiert unabhängige Pakete seit 1.21 in der Reihenfolge ihres
// Importpfads, sobald ihre Abhängigkeiten fertig sind. So läuft die Momentaufnahme vor dem Paket-Init des Go-Laders,
// der WEBVIEW2_*-Variablen überschreibt (webviewloader/env_create.go). Dieses Paket darf nichts anderes importieren
// (TestEnvsnapLaeuftVorDemLader).
package envsnap

import "os"

var start = os.Environ()

// Start liefert eine Kopie der Umgebung beim Prozessstart ("NAME=Wert"). Werte gehören nie in Ausgaben.
func Start() []string { return append([]string(nil), start...) }
