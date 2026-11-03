package client

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestPanelEingabeAusVerifiziertemManifest(t *testing.T) {
	for _, free := range []bool{false, true} {
		b := pipeBed(t)
		m, _ := ParseManifest(b.engine.bundle.Manifest.Envelope.Manifest)
		if free {
			m.Pins[0].Setups[0].Frei = []FreeField{{Name: "titel", Typ: "text", MaxLaenge: 40}}
		}
		signed, cred := l1Sign(t, encode(m), m.DeviceID)
		b.engine.Credential = cred
		b.engine.bundle.Manifest.Envelope = signed
		got := panelRaw(t, b.engine, `{"type":"status"}`)
		if len(got.Setups) != 1 || got.Setups[0].BrauchtEingabe != free || (free && got.Setups[0].Startbar) {
			t.Fatal(got)
		}
		b.engine.bundle.Manifest.Envelope.Sig = "broken"
		got = panelRaw(t, b.engine, `{"type":"status"}`)
		if len(got.Setups) != 0 {
			t.Fatal("unverifiziert projiziert", got)
		}
	}
}

// Actual Execute stdout adapter: no helper, two raw frames while the first
// question is pending. Both title provenance and the one-question gate live in Go.
func TestH4RoheFragenAmLaeufer(t *testing.T) {
	b := pipeBed(t)
	events, detach, err := b.engine.Questions.Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer detach()
	observed := make(chan string, 1)
	b.exit = func(w *io.PipeWriter) {
		for _, id := range []string{"q1", "q2"} {
			raw := `{"type":"frage","id":"` + id + `","text":"Skript darf keinen Titel setzen","optionen":["Ja","Nein"],"frist_s":5}`
			var h [4]byte
			binary.BigEndian.PutUint32(h[:], uint32(len(raw)))
			w.Write(h[:])
			w.Write([]byte(raw))
			if id == "q1" {
				select {
				case q := <-events:
					observed <- q.Titel
				case <-time.After(2 * time.Second):
					observed <- "keine Frage"
				}
			}
		}
	}
	r := b.execute()
	if r.End != "internal" {
		t.Fatal("zweite rohe Frage nicht abgewiesen", r)
	}
	select {
	case title := <-observed:
		if title != b.env.Origin.Pin {
			t.Fatal("Titel nicht aus Pin", title)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("keine Frage am echten Adapter")
	}
	for {
		select {
		case q := <-events:
			if q.Type == "frage" {
				t.Fatal("zweite offene Frage", q)
			}
		default:
			return
		}
	}
}

func TestH4SkriptTitelAbgewiesen(t *testing.T) {
	b := pipeBed(t)
	b.exit = func(w *io.PipeWriter) {
		WriteFrame(w, map[string]any{"type": "frage", "id": "q1", "titel": "gefälscht", "text": "x", "optionen": []string{"Ja", "Nein"}, "frist_s": 5})
	}
	if r := b.execute(); r.End != "internal" {
		t.Fatal(r)
	}
}

func TestH4FrageOhnePanelUndAbbruch(t *testing.T) {
	// The runner-owned raw adapter is also exercised independently for null and
	// cancellation; Execute coverage above binds it to the real script stream.
	b := pipeBed(t)
	q := newRunQuestions(&b.engine.Questions, b.env.Origin)
	raw := []byte(`{"type":"frage","id":"q1","text":"x","optionen":["Ja","Nein"],"frist_s":5}`)
	ch := make(chan any, 1)
	if err := q.start(context.Background(), raw, func(v any) error { ch <- v; return nil }); err != nil {
		t.Fatal(err)
	}
	q.wait()
	v := <-ch
	out, _ := json.Marshal(v)
	if string(out) != `{"type":"antwort","id":"q1","wahl":null}` {
		t.Fatal(string(out))
	}
}
