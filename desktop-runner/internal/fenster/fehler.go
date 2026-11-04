package fenster

import (
	"context"
	"errors"
)

// Code ist ein fester Fehlercode der Brücke. Nur diese Werte erreichen die
// Seite; Fehlertexte, Statuszeilen oder Antwortkörper des Kerns nie.
type Code string

const (
	CodeAbgelehnt       Code = "abgelehnt"
	CodeAntwort         Code = "antwort_ungueltig"
	CodeArgs            Code = "args"
	CodeBefehl          Code = "befehl"
	CodeFehler          Code = "fehler"
	CodeNichtAngemeldet Code = "nicht_angemeldet"
	CodeNichtErreichbar Code = "nicht_erreichbar"
	CodeUmschlag        Code = "umschlag"
	CodeZuGross         Code = "zu_gross"
	CodeZuViele         Code = "zu_viele"
)

// Codes listet alle festen Codes alphabetisch.
func Codes() []Code {
	return []Code{CodeAbgelehnt, CodeAntwort, CodeArgs, CodeBefehl, CodeFehler, CodeNichtAngemeldet, CodeNichtErreichbar, CodeUmschlag, CodeZuGross, CodeZuViele}
}

// Fehler trägt ausschließlich einen festen Code. Adapter geben diese Sentinels
// (auch umhüllt) zurück; jeder andere Fehler wird zu CodeFehler.
type Fehler struct{ Code Code }

func (f *Fehler) Error() string { return string(f.Code) }

var (
	ErrAbgelehnt       = &Fehler{CodeAbgelehnt}
	ErrAntwort         = &Fehler{CodeAntwort}
	ErrArgs            = &Fehler{CodeArgs}
	ErrBefehl          = &Fehler{CodeBefehl}
	ErrFehler          = &Fehler{CodeFehler}
	ErrNichtAngemeldet = &Fehler{CodeNichtAngemeldet}
	ErrNichtErreichbar = &Fehler{CodeNichtErreichbar}
	ErrUmschlag        = &Fehler{CodeUmschlag}
	ErrZuGross         = &Fehler{CodeZuGross}
	ErrZuViele         = &Fehler{CodeZuViele}
)

// codeOf bildet jeden Fehler auf einen festen Code ab, nie auf seinen Text.
func codeOf(err error) Code {
	var f *Fehler
	switch {
	case errors.As(err, &f):
		return f.Code
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return CodeNichtErreichbar
	default:
		return CodeFehler
	}
}
