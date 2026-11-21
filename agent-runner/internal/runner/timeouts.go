package runner

import (
	"errors"
	"strconv"
	"time"
)

const (
	// ServerReadTimeout begrenzt das Lesen der Anfrage samt Körper.
	ServerReadTimeout = 10 * time.Second
	// CleanupMargin lässt nach Ablauf der Wandzeit Luft für Abbruch,
	// SDK-Aufräumen und das Schreiben der festen Antwort.
	CleanupMargin = 10 * time.Second
)

// ParseWallS liest JNPT_AGENT_MAX_WALL_S und liefert die wirksame Wandzeit.
// Leer und 0 bedeuten den Standard; alles außerhalb 1..300 ist ein Fehler.
func ParseWallS(env string) (int, error) {
	if env == "" {
		return DefaultMaxWallS, nil
	}
	n, err := strconv.Atoi(env)
	if err != nil {
		return 0, errors.New("wandzeit ist keine Zahl")
	}
	return effectiveWallS(n)
}

func effectiveWallS(n int) (int, error) {
	if n == 0 {
		return DefaultMaxWallS, nil
	}
	if n < 1 || n > DefaultMaxWallS {
		return 0, errors.New("wandzeit muss zwischen 1 und 300 Sekunden liegen")
	}
	return n, nil
}

// WriteTimeout ist die Schreibfrist des HTTP-Servers für die wirksame
// Wandzeit. Die Frist läuft ab dem Lesen der Anfrage, deshalb zählt die
// Lesefrist mit.
func WriteTimeout(effectiveWallS int) time.Duration {
	return ServerReadTimeout + time.Duration(effectiveWallS)*time.Second + CleanupMargin
}
