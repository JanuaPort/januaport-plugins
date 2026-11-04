package fenster

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"unicode/utf8"
)

// MaxUmschlag begrenzt eine Seitennachricht.
const MaxUmschlag = 256 << 10

const maxTiefe = 64

var (
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	namePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
)

// Umschlag ist die einzige Nachrichtenform der Seite: {id, befehl, args}.
type Umschlag struct {
	ID        string
	Befehl    string
	Args      json.RawMessage
	name      string
	arguments json.RawMessage
}

// ParseUmschlag prüft streng: höchstens 256 KiB, gültiges UTF-8, ein einziges
// JSON-Objekt ohne doppelte Schlüssel, genau die Felder id, befehl und args.
// Bei Befehls- und Argumentfehlern ist die ID gesetzt, damit die Antwort sie
// spiegeln kann; bei Umschlagfehlern bleibt sie leer.
func ParseUmschlag(raw []byte) (Umschlag, error) {
	if len(raw) > MaxUmschlag {
		return Umschlag{}, ErrZuGross
	}
	if !utf8.Valid(raw) || !einzigartig(raw) {
		return Umschlag{}, ErrUmschlag
	}
	var wire struct {
		ID     *string         `json:"id"`
		Befehl *string         `json:"befehl"`
		Args   json.RawMessage `json:"args"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&wire) != nil || wire.ID == nil || wire.Befehl == nil || !idPattern.MatchString(*wire.ID) || !istObjekt(wire.Args) {
		return Umschlag{}, ErrUmschlag
	}
	u := Umschlag{ID: *wire.ID, Befehl: *wire.Befehl, Args: wire.Args}
	if !slices.Contains(befehle, u.Befehl) {
		return u, ErrBefehl
	}
	if u.Befehl != "werkzeug" {
		if !leeresObjekt(u.Args) {
			return u, ErrArgs
		}
		return u, nil
	}
	var args struct {
		Name      *string         `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	dec = json.NewDecoder(bytes.NewReader(u.Args))
	dec.DisallowUnknownFields()
	if dec.Decode(&args) != nil || args.Name == nil || !namePattern.MatchString(*args.Name) || !istObjekt(args.Arguments) {
		return u, ErrArgs
	}
	u.name, u.arguments = *args.Name, args.Arguments
	return u, nil
}

// Werkzeug liefert Name und Argumente des Befehls "werkzeug".
func (u Umschlag) Werkzeug() (string, json.RawMessage, error) {
	if u.Befehl != "werkzeug" || u.name == "" {
		return "", nil, ErrArgs
	}
	return u.name, u.arguments, nil
}

func istObjekt(b json.RawMessage) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && t[0] == '{'
}

func leeresObjekt(b json.RawMessage) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(b, &m) == nil && m != nil && len(m) == 0
}

// einzigartig verlangt genau ein JSON-Dokument ohne doppelte Objektschlüssel
// auf jeder Ebene; encoding/json nähme sonst stillschweigend den letzten.
func einzigartig(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if !wert(dec, 0) {
		return false
	}
	_, err := dec.Token()
	return err == io.EOF
}

func wert(dec *json.Decoder, tiefe int) bool {
	if tiefe > maxTiefe {
		return false
	}
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		gesehen := map[string]bool{}
		for dec.More() {
			k, err := dec.Token()
			key, isString := k.(string)
			if err != nil || !isString || gesehen[key] {
				return false
			}
			gesehen[key] = true
			if !wert(dec, tiefe+1) {
				return false
			}
		}
	case '[':
		for dec.More() {
			if !wert(dec, tiefe+1) {
				return false
			}
		}
	}
	end, err := dec.Token()
	return err == nil && end != nil
}
