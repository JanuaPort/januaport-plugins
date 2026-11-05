package fenster

import "testing"

func TestBrueckeKeinAnmeldeBefehl(t *testing.T) {
	for _, cmd := range []string{"anmelden", "abmelden", "login", "session"} {
		if _, err := ParseUmschlag([]byte(`{"id":"1","befehl":"` + cmd + `","args":{}}`)); err == nil {
			t.Fatal(cmd)
		}
	}
}
