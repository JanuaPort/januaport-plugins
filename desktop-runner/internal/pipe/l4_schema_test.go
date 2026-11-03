package pipe

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestL4SchemaPflichtfelder(t *testing.T) {
	q := `{"type":"frage","id":"q1","titel":"Pin","text":"Skript","optionen":["Ja","Nein"],"frist_s":5}`
	for _, raw := range []string{q, strings.Replace(q, `"Pin"`, `"`+strings.Repeat("ü", 40)+`"`, 1)} {
		if _, err := DecodeServer([]byte(raw)); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{strings.Replace(q, `"titel":"Pin",`, "", 1), strings.Replace(q, `"Pin"`, `""`, 1), strings.Replace(q, `"Pin"`, `null`, 1), strings.Replace(q, `"Pin"`, `"`+strings.Repeat("ü", 41)+`"`, 1)} {
		if _, err := DecodeServer([]byte(raw)); err == nil {
			t.Fatal("Titel akzeptiert", raw)
		}
	}
	s := `{"type":"zustand","zustand":"arbeitet","test":false,"netz":true,"beschaeftigt":false,"setups":[{"pin":"demo","setup":"a","label":"A","startbar":false,"braucht_eingabe":true}]}`
	for _, v := range []string{"true", "false"} {
		raw := strings.Replace(s, `"braucht_eingabe":true`, `"braucht_eingabe":`+v, 1)
		m, err := DecodeServer([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(m)
		if !strings.Contains(string(b), `"braucht_eingabe":`+v) {
			t.Fatal(string(b))
		}
	}
	for _, raw := range []string{strings.Replace(s, `,"braucht_eingabe":true`, "", 1), strings.Replace(s, `"braucht_eingabe":true`, `"braucht_eingabe":null`, 1), strings.Replace(s, `"braucht_eingabe":true`, `"braucht_eingabe":1`, 1)} {
		if _, err := DecodeServer([]byte(raw)); err == nil {
			t.Fatal("Setup akzeptiert", raw)
		}
	}
}
