package client

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// SEC-R4 F5: Das Bedienfeld liest die ganze Datei credential samt Gerätetoken in den Speicher (Reader.identity entschlüsselt
// sie alle 15 s). Der Klartext-Puffer wird nach dem Dekodieren gelöscht, auch im Fehlerfall; die Aussage "das Token liegt
// in keinem Go-Feld" bleibt, die Aussage "es wird nicht gelesen" entfällt.
func TestStrictUndLoeschenNullt(t *testing.T) {
	type doc struct {
		A string `json:"a"`
	}
	for name, in := range map[string]string{"gueltig": `{"a":"geheim-synthetisch"}`, "ungueltig": `{"a":`, "fremdesFeld": `{"a":"x","b":"geheim-synthetisch"}`} {
		t.Run(name, func(t *testing.T) {
			b := []byte(in)
			var d doc
			_ = strictUndLoeschen(b, &d)
			if !bytes.Equal(b, make([]byte, len(in))) {
				t.Fatalf("Puffer nicht gelöscht: %q", b)
			}
		})
	}
	b := []byte(`{"a":"x"}`)
	var d doc
	if err := strictUndLoeschen(b, &d); err != nil || d.A != "x" {
		t.Fatalf("Ergebnis geht nicht verloren: %+v %v", d, err)
	}
}

// Die Datei credential wird beim Lesen für die Liste der Apps nicht länger als nötig im Klartext gehalten: identity nullt
// den Puffer, das Ergebnis trägt das Token trotzdem nicht.
func TestReaderIdentityLoeschtDenKlartext(t *testing.T) {
	b := newReaderBed(t, false)
	spy := &spyCipher{testCipher: testCipher{}}
	r, err := NewReader(b.root, spy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apps(); err != nil {
		t.Fatal(err)
	}
	if len(spy.opened) == 0 {
		t.Fatal("kein Klartext gesehen: Wächter blind")
	}
	for i, plain := range spy.opened {
		if bytes.Contains(plain, []byte(syntheticToken())) {
			t.Errorf("Klartext %d trägt nach dem Lesen noch das Gerätetoken", i)
		}
	}
}

// spyCipher behält die Klartext-Puffer, die Open liefert, damit der Test prüfen kann, ob sie danach gelöscht sind.
type spyCipher struct {
	testCipher
	opened [][]byte
}

func (s *spyCipher) Open(raw []byte) ([]byte, error) {
	plain, err := s.testCipher.Open(raw)
	if err == nil {
		s.opened = append(s.opened, plain)
	}
	return plain, err
}

// SEC-R4 (Mutantenlücke rd-size): Der Reader liest höchstens 20 MiB. Genau 20 MiB gehen, ein Byte mehr nicht.
func TestReaderGroessenGrenze(t *testing.T) {
	root := t.TempDir()
	r, err := NewReader(root, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte("TEST-CIPHER:")
	for name, size := range map[string]int{"genau": 20 << 20, "eins_mehr": 20<<20 + len(prefix) - len(prefix) + 1} {
		body := bytes.Repeat([]byte("x"), size-len(prefix))
		if err := os.WriteFile(filepath.Join(root, name), append(append([]byte(nil), prefix...), body...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.plain("genau"); err != nil {
		t.Fatalf("20 MiB abgelehnt: %v", err)
	}
	if _, err := r.plain("eins_mehr"); !errors.Is(err, ErrProtocol) {
		t.Fatalf("20 MiB + 1 Byte: %v", err)
	}
}
