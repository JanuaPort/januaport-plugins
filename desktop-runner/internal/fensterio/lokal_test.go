package fensterio

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

func frame(t *testing.T, m pipe.Message) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type recorder struct {
	sent  []string
	reply []byte
	err   error
}

func (r *recorder) exchange(_ context.Context, m pipe.Message) ([]byte, error) {
	r.sent = append(r.sent, m.Type)
	return r.reply, r.err
}

func TestLokalStatusProjiziertNurZustandBisNetz(t *testing.T) {
	r := &recorder{reply: frame(t, pipe.Message{Type: "zustand", Zustand: "pausiert", Bis: "2026-11-04T12:00:00Z", Netz: true, Test: true, Beschaeftigt: true,
		Setups: []pipe.Setup{{Pin: "geheimer_pin", Setup: "raum", Label: "Raumprofil", Startbar: true}}})}
	st, err := NewLokal(r.exchange).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st != (fenster.Status{Zustand: "pausiert", Bis: "2026-11-04T12:00:00Z", Netz: true}) {
		t.Fatal(st)
	}
	if !reflect.DeepEqual(r.sent, []string{"status"}) {
		t.Fatal(r.sent)
	}
	out, _ := json.Marshal(st)
	if string(out) != `{"zustand":"pausiert","bis":"2026-11-04T12:00:00Z","netz":true}` {
		t.Fatal(string(out))
	}
}

var (
	anhalten                 = []string{"anhalten"}
	anhaltenUndDreiMalStatus = []string{"anhalten", "status", "status", "status"}
)

func TestLokalAnhalten(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply []byte
		err   error
		want  error
		sent  []string
	}{
		{"angehalten", frame(t, pipe.Message{Type: "zustand", Zustand: "angehalten"}), nil, nil, anhalten},
		{"nochArbeitend", frame(t, pipe.Message{Type: "zustand", Zustand: "arbeitet"}), nil, fenster.ErrAbgelehnt, anhaltenUndDreiMalStatus},
		{"pausiertReichtNicht", frame(t, pipe.Message{Type: "zustand", Zustand: "pausiert"}), nil, fenster.ErrAbgelehnt, anhaltenUndDreiMalStatus},
		{"stoerung", frame(t, pipe.Message{Type: "zustand", Zustand: "stoerung"}), nil, fenster.ErrAbgelehnt, anhalten},
		{"abgewiesen", frame(t, pipe.Message{Type: "fehler", Code: "abgewiesen"}), nil, fenster.ErrAbgelehnt, anhalten},
		{"stoerungsFehler", frame(t, pipe.Message{Type: "fehler", Code: "stoerung"}), nil, fenster.ErrAbgelehnt, anhalten},
		{"kaputt", []byte(`{"type":"zustand"}`), nil, fenster.ErrFehler, anhalten},
		{"fremderTyp", []byte(`{"type":"frage"}`), nil, fenster.ErrFehler, anhalten},
		{"leer", nil, nil, fenster.ErrFehler, anhalten},
		{"verbindungsfehler", nil, errors.New("pipe weg: C:\\geheim"), fenster.ErrNichtErreichbar, anhalten},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{reply: tc.reply, err: tc.err}
			err := NewLokal(r.exchange).Anhalten(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v", err)
			}
			if err != nil && (len(err.Error()) > 40 || strings.Contains(err.Error(), "geheim") || strings.Contains(err.Error(), "pipe weg")) {
				t.Fatalf("Fehlertext trägt Rohdaten: %q", err)
			}
			if !reflect.DeepEqual(r.sent, tc.sent) {
				t.Fatal(r.sent)
			}
		})
	}
}

func TestLokalStatusFehler(t *testing.T) {
	for name, r := range map[string]*recorder{
		"verbindung":   {err: errors.New("x")},
		"fehlerRahmen": {reply: frame(t, pipe.Message{Type: "fehler", Code: "stoerung"})},
		"kaputt":       {reply: []byte(`nope`)},
	} {
		if _, err := NewLokal(r.exchange).Status(context.Background()); err == nil {
			t.Errorf("%s: kein Fehler", name)
		}
	}
}

// Plan 5: Der Pfad zur Pipe hat genau zwei Operationen. Die Schnittstelle, der
// Wrapper und die tatsächlich gesendeten Befehle werden alle drei geprüft.
func TestLokalGenauZweiOperationen(t *testing.T) {
	names := func(typ reflect.Type) []string {
		var out []string
		for i := 0; i < typ.NumMethod(); i++ {
			out = append(out, typ.Method(i).Name)
		}
		sort.Strings(out)
		return out
	}
	want := []string{"Anhalten", "Status"}
	if got := names(reflect.TypeOf((*fenster.Lokal)(nil)).Elem()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Schnittstelle: %v", got)
	}
	r := &recorder{reply: frame(t, pipe.Message{Type: "zustand", Zustand: "angehalten"})}
	l := NewLokal(r.exchange)
	if got := names(reflect.TypeOf(l)); !reflect.DeepEqual(got, want) {
		t.Fatalf("Wrapper: %v", got)
	}
	_, _ = l.Status(context.Background())
	_ = l.Anhalten(context.Background())
	_, _ = l.Status(context.Background())
	for _, typ := range r.sent {
		if typ != "status" && typ != "anhalten" {
			t.Fatalf("Wrapper sendet %q", typ)
		}
	}
	if len(r.sent) != 3 {
		t.Fatal(r.sent)
	}
}

// Der Läufer sendet neben der Antwort auf einen Befehl jede Sekunde einen Statusrahmen. Ein veralteter
// Rahmen vor der Antwort darf das Anhalten nicht fälschlich abweisen: Der Wrapper fragt höchstens
// dreimal mit dem erlaubten Befehl "status" nach.
func TestLokalAnhaltenNachVeraltetemRahmen(t *testing.T) {
	replies := [][]byte{
		frame(t, pipe.Message{Type: "zustand", Zustand: "arbeitet"}),
		frame(t, pipe.Message{Type: "zustand", Zustand: "arbeitet"}),
		frame(t, pipe.Message{Type: "zustand", Zustand: "angehalten"}),
	}
	var sent []string
	ex := func(_ context.Context, m pipe.Message) ([]byte, error) {
		sent = append(sent, m.Type)
		r := replies[0]
		if len(replies) > 1 {
			replies = replies[1:]
		}
		return r, nil
	}
	if err := NewLokal(ex).Anhalten(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sent, []string{"anhalten", "status", "status"}) {
		t.Fatal(sent)
	}
}
