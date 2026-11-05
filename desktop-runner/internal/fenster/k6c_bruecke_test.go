package fenster

import (
	"context"
	"strings"
	"testing"
)

func TestBrueckeKeinAnmeldeBefehl(t *testing.T) {
	b, v, g, _ := setup(t)
	for _, cmd := range []string{"anmelden", "abmelden", "login", "session"} {
		if _, err := ParseUmschlag([]byte(`{"id":"1","befehl":"` + cmd + `","args":{}}`)); err == nil {
			t.Fatal(cmd)
		}
		b.Empfangen(context.Background(), mainFrame, []byte(`{"id":"1","befehl":"`+cmd+`","args":{}}`))
	}
	if g.lists != 0 || len(g.calls) != 0 || strings.Contains(v.all(), "jnptpc_") {
		t.Fatal("Login ueber Bruecke")
	}
}
