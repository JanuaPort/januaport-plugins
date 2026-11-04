package fenster

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Gateway ist, was die Brücke vom Kern braucht: nur tools/list und tools/call.
// Träger (Gerätetoken, App-Token) halten allein die Adapter und geben sie nie her.
type Gateway interface {
	Werkzeuge(ctx context.Context) (json.RawMessage, error)
	Werkzeug(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error)
}

// Lokal hat genau zwei Operationen: Status lesen und Anhalten. Mehr bietet die
// Seite am lokalen Läufer nie an (Pause, Setups, Fortsetzen bleiben dem Menschen).
type Lokal interface {
	Status(ctx context.Context) (Status, error)
	Anhalten(ctx context.Context) error
}

// Status ist die kleine Projektion für die Seite. Sie enthält weder Pins noch
// Setups, Konto, Testmerkmal noch die Information, ob gerade ein Lauf arbeitet.
type Status struct {
	Zustand string `json:"zustand"`
	Bis     string `json:"bis,omitempty"`
	Netz    bool   `json:"netz"`
}

// View ist das einzige, was die Brücke vom Fenster benötigt.
type View interface {
	Senden(antwort []byte) error
}

// Bruecke verbindet eine App-Seite mit Gateway und lokalem Läufer.
type Bruecke struct {
	// Frist begrenzt jeden Befehl; Standard 60 Sekunden.
	Frist time.Duration
	app   string
	g     Gateway
	l     Lokal
	v     View
}

// NewBruecke verlangt einen gültigen Appnamen und alle drei Gegenstellen.
func NewBruecke(app string, g Gateway, l Lokal, v View) (*Bruecke, error) {
	if !ValidAppName(app) || g == nil || l == nil || v == nil {
		return nil, ErrArgs
	}
	return &Bruecke{Frist: 60 * time.Second, app: app, g: g, l: l, v: v}, nil
}

type antwort struct {
	ID       *string         `json:"id"`
	OK       bool            `json:"ok"`
	Ergebnis json.RawMessage `json:"ergebnis,omitempty"`
	Fehler   Code            `json:"fehler,omitempty"`
}

// Empfangen verarbeitet eine Seitennachricht und antwortet über die View.
// Nachrichten aus fremden Rahmen oder von fremden Origins bleiben unbeantwortet.
func (b *Bruecke) Empfangen(ctx context.Context, src Source, raw []byte) {
	if !SourceAllowed(b.app, src) {
		return
	}
	u, err := ParseUmschlag(raw)
	if err != nil {
		b.fehler(u.ID, err)
		return
	}
	if !MessageAllowed(b.app, src, u.Befehl) {
		b.fehler(u.ID, ErrBefehl)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, b.Frist)
	defer cancel()
	ergebnis, err := b.ausfuehren(ctx, u)
	if err != nil {
		b.fehler(u.ID, err)
		return
	}
	if !ergebnisOK(ergebnis) {
		b.fehler(u.ID, ErrAntwort)
		return
	}
	b.senden(antwort{ID: &u.ID, OK: true, Ergebnis: ergebnis})
}

func (b *Bruecke) ausfuehren(ctx context.Context, u Umschlag) (json.RawMessage, error) {
	switch u.Befehl {
	case "status":
		st, err := b.l.Status(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(st)
	case "anhalten":
		if err := b.l.Anhalten(ctx); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "werkzeuge":
		return b.g.Werkzeuge(ctx)
	case "werkzeug":
		name, arguments, err := u.Werkzeug()
		if err != nil {
			return nil, err
		}
		return b.g.Werkzeug(ctx, name, arguments)
	}
	return nil, ErrBefehl
}

func (b *Bruecke) fehler(id string, err error) {
	a := antwort{OK: false, Fehler: codeOf(err)}
	if id != "" {
		a.ID = &id
	}
	b.senden(a)
}

func (b *Bruecke) senden(a antwort) {
	out, err := json.Marshal(a)
	if err != nil {
		// Ein Ergebnis, das sich nicht kodieren lässt, geht nie roh weiter.
		out, _ = json.Marshal(antwort{ID: a.ID, Fehler: CodeAntwort})
	}
	_ = b.v.Senden(out)
}

func ergebnisOK(r json.RawMessage) bool {
	t := bytes.TrimSpace(r)
	return len(t) > 0 && !bytes.Equal(t, []byte("null")) && json.Valid(t) && !EnthaeltTraeger(t)
}

// traegerMuster erkennt Token der Form jnpt…_<Zufall>. Es ist absichtlich
// grob: Im Zweifel wird die Antwort verworfen, nie ausgeliefert.
var traegerMuster = regexp.MustCompile(`jnpt[a-z]*_[A-Za-z0-9_.-]{20,}`)

// EnthaeltTraeger meldet, ob die Bytes (auch nach JSON-Unescaping) ein Token
// oder die Präfixe der Träger enthalten.
func EnthaeltTraeger(b []byte) bool {
	if traegerText(string(b)) {
		return true
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return false
	}
	return traegerWert(v)
}

func traegerText(s string) bool {
	return strings.Contains(s, "jnptpc_") || strings.Contains(s, "jnptapp_") || traegerMuster.MatchString(s)
}

func traegerWert(v any) bool {
	switch x := v.(type) {
	case string:
		return traegerText(x)
	case []any:
		for _, e := range x {
			if traegerWert(e) {
				return true
			}
		}
	case map[string]any:
		for k, e := range x {
			if traegerText(k) || traegerWert(e) {
				return true
			}
		}
	}
	return false
}
