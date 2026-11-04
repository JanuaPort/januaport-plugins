package fenster

import (
	"errors"
	"strings"
	"testing"
)

func TestUmschlagStrikt(t *testing.T) {
	big := `{"id":"1","befehl":"werkzeug","args":{"name":"x","arguments":{"t":"` + strings.Repeat("a", MaxUmschlag) + `"}}}`
	for _, tc := range []struct {
		name, raw string
		ok        bool
	}{
		{"status", `{"id":"1","befehl":"status","args":{}}`, true},
		{"anhalten", `{"id":"a-b_c.9","befehl":"anhalten","args":{}}`, true},
		{"werkzeuge", `{"id":"1","befehl":"werkzeuge","args":{}}`, true},
		{"werkzeug", `{"id":"1","befehl":"werkzeug","args":{"name":"demo_wawi_get_order","arguments":{"id":7}}}`, true},
		{"werkzeugLeereArgumente", `{"id":"1","befehl":"werkzeug","args":{"name":"ping","arguments":{}}}`, true},
		{"leerzeichenAussen", ` {"id":"1","befehl":"status","args":{}} `, true},
		{"unbekanntesFeld", `{"id":"1","befehl":"status","args":{},"x":1}`, false},
		{"idFehlt", `{"befehl":"status","args":{}}`, false},
		{"befehlFehlt", `{"id":"1","args":{}}`, false},
		{"argsFehlt", `{"id":"1","befehl":"status"}`, false},
		{"argsNull", `{"id":"1","befehl":"status","args":null}`, false},
		{"argsListe", `{"id":"1","befehl":"status","args":[]}`, false},
		{"statusMitArgument", `{"id":"1","befehl":"status","args":{"x":1}}`, false},
		{"idZahl", `{"id":1,"befehl":"status","args":{}}`, false},
		{"idLeer", `{"id":"","befehl":"status","args":{}}`, false},
		{"idZuLang", `{"id":"` + strings.Repeat("a", 65) + `","befehl":"status","args":{}}`, false},
		{"idSonderzeichen", `{"id":"a b","befehl":"status","args":{}}`, false},
		{"idHtml", `{"id":"<b>","befehl":"status","args":{}}`, false},
		{"befehlUnbekannt", `{"id":"1","befehl":"pause","args":{}}`, false},
		{"befehlGross", `{"id":"1","befehl":"Status","args":{}}`, false},
		{"befehlZahl", `{"id":"1","befehl":1,"args":{}}`, false},
		{"werkzeugNameFehlt", `{"id":"1","befehl":"werkzeug","args":{"arguments":{}}}`, false},
		{"werkzeugArgumentsFehlen", `{"id":"1","befehl":"werkzeug","args":{"name":"x"}}`, false},
		{"werkzeugArgumentsListe", `{"id":"1","befehl":"werkzeug","args":{"name":"x","arguments":[]}}`, false},
		{"werkzeugArgumentsNull", `{"id":"1","befehl":"werkzeug","args":{"name":"x","arguments":null}}`, false},
		{"werkzeugNameLeer", `{"id":"1","befehl":"werkzeug","args":{"name":"","arguments":{}}}`, false},
		{"werkzeugNameSlash", `{"id":"1","befehl":"werkzeug","args":{"name":"a/b","arguments":{}}}`, false},
		{"werkzeugNameZuLang", `{"id":"1","befehl":"werkzeug","args":{"name":"` + strings.Repeat("a", 129) + `","arguments":{}}}`, false},
		{"werkzeugFremdesFeld", `{"id":"1","befehl":"werkzeug","args":{"name":"x","arguments":{},"token":"y"}}`, false},
		{"doppelterSchluessel", `{"id":"1","id":"2","befehl":"status","args":{}}`, false},
		{"doppelterSchluesselTief", `{"id":"1","befehl":"werkzeug","args":{"name":"x","arguments":{"a":1,"a":2}}}`, false},
		{"doppelterBefehl", `{"id":"1","befehl":"status","befehl":"anhalten","args":{}}`, false},
		{"folgedokument", `{"id":"1","befehl":"status","args":{}}{}`, false},
		{"folgetext", `{"id":"1","befehl":"status","args":{}} x`, false},
		{"kaputt", `{"id":"1","befehl":`, false},
		{"kein_objekt", `[]`, false},
		{"leer", ``, false},
		{"ungueltigesUTF8", "{\"id\":\"1\xff\",\"befehl\":\"status\",\"args\":{}}", false},
		{"bom", "\xef\xbb\xbf" + `{"id":"1","befehl":"status","args":{}}`, false},
		{"zuGross", big, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := ParseUmschlag([]byte(tc.raw))
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v", err)
			}
			if err != nil {
				var f *Fehler
				if !errors.As(err, &f) || (f.Code != CodeUmschlag && f.Code != CodeArgs && f.Code != CodeBefehl && f.Code != CodeZuGross) {
					t.Fatalf("kein fester Code: %v", err)
				}
				return
			}
			if u.ID == "" || u.Befehl == "" {
				t.Fatal("leerer Umschlag", u)
			}
		})
	}
}

func TestUmschlagGrenzeGenau256KiB(t *testing.T) {
	head := `{"id":"1","befehl":"werkzeug","args":{"name":"x","arguments":{"t":"`
	tail := `"}}}`
	pad := MaxUmschlag - len(head) - len(tail)
	exakt := head + strings.Repeat("a", pad) + tail
	if len(exakt) != MaxUmschlag {
		t.Fatal(len(exakt))
	}
	if _, err := ParseUmschlag([]byte(exakt)); err != nil {
		t.Fatal("genau 256 KiB muss zulässig sein", err)
	}
	_, err := ParseUmschlag([]byte(exakt + " "))
	var f *Fehler
	if !errors.As(err, &f) || f.Code != CodeZuGross {
		t.Fatalf("256 KiB + 1 Byte: %v", err)
	}
}

func TestUmschlagWerkzeugFelder(t *testing.T) {
	u, err := ParseUmschlag([]byte(`{"id":"7","befehl":"werkzeug","args":{"name":"demo_wawi_get_order","arguments":{"id":7,"n":[1,2]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	name, args, err := u.Werkzeug()
	if err != nil || name != "demo_wawi_get_order" || string(args) != `{"id":7,"n":[1,2]}` {
		t.Fatal(name, string(args), err)
	}
	status, _ := ParseUmschlag([]byte(`{"id":"7","befehl":"status","args":{}}`))
	if _, _, err := status.Werkzeug(); err == nil {
		t.Fatal("Werkzeug() nur für den Befehl werkzeug")
	}
}

func TestFesteCodes(t *testing.T) {
	want := []Code{"abgelehnt", "antwort_ungueltig", "args", "befehl", "fehler", "nicht_angemeldet", "nicht_erreichbar", "umschlag", "zu_gross", "zu_viele"}
	got := Codes()
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(got)
		}
	}
	for _, c := range got {
		for _, r := range string(c) {
			if (r < 'a' || r > 'z') && r != '_' {
				t.Fatalf("Code %q enthält freie Zeichen", c)
			}
		}
	}
}
