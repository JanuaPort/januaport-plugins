package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
)

// Hotkey ist ein Tastenkürzel einer App (#1240 F-T): Strg+Alt+F1 bis F12 startet das Setup des Pins, wie der
// Klick im Menü des Bedienfelds. Es steht im Feld tasten der jnpt.app.json des Pakets. Der Läufer prüft die
// Grammatik des Kerns noch einmal selbst, und das Bedienfeld ein drittes Mal.
type Hotkey struct {
	Key   string `json:"taste"`
	Pin   string `json:"pin"`
	Setup string `json:"setup"`
}

var (
	hotkeyName    = regexp.MustCompile(`^[a-z0-9_]{1,48}$`)
	errHotkeyForm = errors.New("tasten: ungültig")
)

// validHotkeys prüft die Liste wie der Kern: nur Strg+Alt+F1 bis F12, jede Taste einmal, Namen im Muster.
func validHotkeys(list []Hotkey) bool {
	var seen []string
	for _, h := range list {
		if !slices.Contains(allowedHotkeyKeys(), h.Key) || slices.Contains(seen, h.Key) || !hotkeyName.MatchString(h.Pin) || !hotkeyName.MatchString(h.Setup) {
			return false
		}
		seen = append(seen, h.Key)
	}
	return true
}

func allowedHotkeyKeys() []string {
	keys := make([]string, 0, 12)
	for n := 1; n <= 12; n++ {
		keys = append(keys, "Strg+Alt+F"+strconv.Itoa(n))
	}
	return keys
}

// parseHotkeys liest das Feld tasten aus den Bytes einer jnpt.app.json. Ein fehlendes Feld oder null ergibt
// keine Kürzel ohne Fehler. Alles Ungültige ist ein Fehler und ergibt nie eine Teilmenge.
func parseHotkeys(manifest []byte) ([]Hotkey, error) {
	if _, err := canonical(manifest); err != nil {
		return nil, errHotkeyForm
	}
	var top struct {
		Tasten json.RawMessage `json:"tasten"`
	}
	if json.Unmarshal(manifest, &top) != nil {
		return nil, errHotkeyForm
	}
	if len(top.Tasten) == 0 || string(bytes.TrimSpace(top.Tasten)) == "null" {
		return nil, nil
	}
	var list []Hotkey
	d := json.NewDecoder(bytes.NewReader(top.Tasten))
	d.DisallowUnknownFields()
	if d.Decode(&list) != nil || !validHotkeys(list) {
		return nil, errHotkeyForm
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list, nil
}
