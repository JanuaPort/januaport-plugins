package main

// consolePlan sagt dem GUI-Prozess (-H=windowsgui), ob und für welche Ausgabekanäle er sich an die
// Konsole des Aufrufers hängen muss. Der Läufer hat selbst keine; ohne Anschluss gingen Statusmeldungen
// und feste Fehlercodes von pair/install/status/uninstall verloren.
type consolePlan struct{ Attach, Stdout, Stderr bool }

// planConsole entscheidet rein aus Argumenten und Handle-Zustand. `run` ist der Dauerbetrieb der
// Benutzeraufgabe und hängt sich nie an eine Konsole; gültige Handles (Umleitung in Pipe oder Datei)
// bleiben unangetastet.
func planConsole(args []string, stdoutValid, stderrValid bool) consolePlan {
	if len(args) > 0 && args[0] == "run" {
		return consolePlan{}
	}
	plan := consolePlan{Stdout: !stdoutValid, Stderr: !stderrValid}
	plan.Attach = plan.Stdout || plan.Stderr
	return plan
}
