//go:build !windows

package main

// Nur der Windows-Läufer ist ein GUI-Programm; Linux-Server bleibt unverändert.
func attachConsole() {}
