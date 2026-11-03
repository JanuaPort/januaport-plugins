package panel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

var now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func receive(t *testing.T, m *Model, v pipe.Message) Effects {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return m.Receive(b, now)
}
func ready(t *testing.T, state string) *Model {
	m := New()
	m.Connection(Connected)
	receive(t, m, pipe.Message{Type: "zustand", Zustand: state, Netz: true})
	return m
}
func item(t *testing.T, m *Model, id string) Item {
	t.Helper()
	for _, i := range m.View().Menu {
		if i.ID == id {
			return i
		}
	}
	t.Fatalf("Menüeintrag fehlt: %s", id)
	return Item{}
}
func question() pipe.Message {
	return pipe.Message{Type: "frage", ID: "q1", Titel: "Manifest", Text: "Skripttext", Optionen: []string{"Ja", "Nein"}, FristS: 5}
}

func TestPanelZustaende(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("test-local", 2*60*60)
	t.Cleanup(func() { time.Local = local })
	for _, tc := range []struct {
		state, color, text        string
		pause, next, halt, resume bool
	}{
		{"arbeitet", "green", "Arbeitet", true, false, true, false},
		{"pausiert", "yellow", "Pausiert", true, true, true, false},
		{"angehalten", "red", "Angehalten", false, false, false, true},
		{"stoerung", "purple", FaultText, false, false, false, false},
	} {
		t.Run(tc.state, func(t *testing.T) {
			m := ready(t, tc.state)
			v := m.View()
			for _, i := range v.Menu {
				if i.ID == "test" {
					t.Fatal("Test auf normalem Gerät")
				}
			}
			if v.Color != tc.color || !strings.Contains(v.Tooltip, tc.text) {
				t.Fatal(v)
			}
			for id, want := range map[string]bool{"pause15": tc.pause, "pause60": tc.pause, "pause0": tc.pause, "weiter": tc.next, "anhalten": tc.halt, "fortsetzen": tc.resume, "status": false, "exit": true} {
				if item(t, m, id).Enabled != want {
					t.Fatal(id, want, v.Menu)
				}
			}
		})
	}
	m := ready(t, "pausiert")
	receive(t, m, pipe.Message{Type: "zustand", Zustand: "pausiert", Bis: "2026-11-03T13:00:00Z", FristAbgelaufen: true, Test: true})
	if !strings.Contains(m.View().Tooltip, "offline, Frist abgelaufen") || !strings.Contains(m.View().Tooltip, now.Add(time.Hour).Local().Format("15:04")) || item(t, m, "test").Enabled {
		t.Fatal(m.View())
	}
}

func TestPanelStoerungBleibtBisNeuGeprueftemZustand(t *testing.T) {
	if FaultText != "Störung. Anhalten: abmelden oder über den Admin halten lassen." {
		t.Fatal(FaultText)
	}
	for _, cause := range []string{"zustand", "fehler", "getrennt", "pruefung", "ungueltig"} {
		t.Run(cause, func(t *testing.T) {
			m := ready(t, "arbeitet")
			switch cause {
			case "zustand":
				receive(t, m, pipe.Message{Type: "zustand", Zustand: "stoerung"})
			case "fehler":
				receive(t, m, pipe.Message{Type: "fehler", Code: "stoerung"})
			case "getrennt":
				m.Connection(Disconnected)
			case "pruefung":
				m.Connection(VerificationFailed)
			case "ungueltig":
				m.Receive([]byte(`{"type":"zustand"}`), now)
			}
			for _, v := range []pipe.Message{{Type: "fehler", Code: "abgewiesen"}, {Type: "zustand", Zustand: "arbeitet", Netz: true}} {
				receive(t, m, v)
				if m.View().Color != "purple" || m.View().Tooltip != FaultText {
					t.Fatal("Störung gelöscht", m.View())
				}
			}
			m.Connection(Connected)
			receive(t, m, pipe.Message{Type: "fehler", Code: "abgewiesen"})
			if m.View().Tooltip != FaultText {
				t.Fatal("Fehler löscht Störung")
			}
			receive(t, m, pipe.Message{Type: "zustand", Zustand: "arbeitet", Netz: true})
			if m.View().Color != "green" {
				t.Fatal(m.View())
			}
		})
	}
}

func TestPanelServerpruefungSperrtBefehle(t *testing.T) {
	for _, conn := range []ConnectionState{Disconnected, VerificationFailed} {
		m := ready(t, "arbeitet")
		receive(t, m, question())
		m.Connection(conn)
		for _, id := range []string{"status", "pause15", "pause60", "pause0", "weiter", "anhalten", "fortsetzen", "setup:demo/a", "frage"} {
			if e := m.Choose(id, now); len(e.Commands) != 0 {
				t.Fatal(id, e)
			}
		}
		if len(m.Answer(0, now).Commands) != 0 || m.View().Dialog != nil {
			t.Fatal("Antwort nach Disconnect")
		}
	}
	for n := 0; n < 100; n++ {
		d := RetryDelay(n)
		if d < 5*time.Second || d > 60*time.Second {
			t.Fatal(d)
		}
	}
}

func TestPanelMenueBefehleUndSetupEingabe(t *testing.T) {
	for _, tc := range []struct{ id, wire, state string }{
		{"pause15", `{"minuten":15,"type":"pause"}`, "arbeitet"},
		{"pause60", `{"minuten":60,"type":"pause"}`, "arbeitet"},
		{"pause0", `{"minuten":0,"type":"pause"}`, "arbeitet"},
		{"weiter", `{"type":"weiter"}`, "pausiert"},
		{"anhalten", `{"type":"anhalten"}`, "arbeitet"},
		{"fortsetzen", `{"type":"fortsetzen"}`, "angehalten"},
	} {
		m := ready(t, tc.state)
		e := m.Choose(tc.id, now)
		if len(e.Commands) != 1 {
			t.Fatal(tc, e)
		}
		b, _ := json.Marshal(e.Commands[0])
		if string(b) != tc.wire {
			t.Fatal(string(b), tc.wire)
		}
	}
	m := ready(t, "arbeitet")
	receive(t, m, pipe.Message{Type: "zustand", Zustand: "arbeitet", Netz: true, Setups: []pipe.Setup{
		{Pin: "demo", Setup: "a", Label: "A", Startbar: true},
		{Pin: "demo", Setup: "b", Label: "B", Startbar: true, BrauchtEingabe: true},
		{Pin: "demo", Setup: "c", Label: "C", Startbar: false},
	}})
	if !item(t, m, "setup:demo/a").Enabled || item(t, m, "setup:demo/b").Enabled || item(t, m, "setup:demo/c").Enabled {
		t.Fatal(m.View())
	}
	if !strings.Contains(item(t, m, "setup:demo/b").Text, "braucht Eingabe") || strings.Contains(item(t, m, "setup:demo/c").Text, "braucht Eingabe") {
		t.Fatal(m.View())
	}
	if len(m.Choose("setup:demo/b", now).Commands) != 0 || len(m.Choose("setup:demo/c", now).Commands) != 0 || len(m.Choose("setup:99", now).Commands) != 0 {
		t.Fatal("grau startet")
	}
	e := m.Choose("setup:demo/a", now)
	b, _ := json.Marshal(e.Commands)
	if string(b) != `[{"pin":"demo","setup":"a","type":"setup_start"}]` {
		t.Fatal(string(b))
	}
	e = m.Choose("exit", now)
	if !e.Quit || len(e.Commands) != 0 {
		t.Fatal("Beenden steuert Läufer", e)
	}
}

func TestPanelFrageNurLokal(t *testing.T) {
	for _, action := range []string{"frage_weg", "anhalten", "zustand_halt", "frist", "antwort", "ungültige_antwort"} {
		t.Run(action, func(t *testing.T) {
			m := ready(t, "arbeitet")
			receive(t, m, question())
			d := m.View().Dialog
			if d == nil || d.Visible || d.Title != "JanuaPort – Manifest" {
				t.Fatal(d)
			}
			if len(m.Answer(0, now).Commands) != 0 {
				t.Fatal("unsichtbare Frage beantwortet")
			}
			m.Choose("frage", now)
			if !m.View().Dialog.Visible {
				t.Fatal("Klick öffnet nicht")
			}
			q := question()
			q.ID = "q2"
			receive(t, m, q)
			if m.View().Dialog.ID != "q1" {
				t.Fatal("zweiter Dialog")
			}
			var e Effects
			switch action {
			case "frage_weg":
				receive(t, m, pipe.Message{Type: "frage_weg", ID: "fremd"})
				if m.View().Dialog == nil {
					t.Fatal("fremdes id")
				}
				e = receive(t, m, pipe.Message{Type: "frage_weg", ID: "q1"})
			case "anhalten":
				e = m.Choose("anhalten", now)
			case "zustand_halt":
				e = receive(t, m, pipe.Message{Type: "zustand", Zustand: "angehalten"})
			case "frist":
				e = m.Tick(now.Add(5 * time.Second))
			case "antwort":
				e = m.Answer(1, now)
				b, _ := json.Marshal(e.Commands)
				if string(b) != `[{"id":"q1","type":"antwort","wahl":1}]` {
					t.Fatal(string(b))
				}
			case "ungültige_antwort":
				for _, n := range []int{-1, 2, 4} {
					if len(m.Answer(n, now).Commands) != 0 {
						t.Fatal("ungültige Wahl")
					}
				}
				e = m.Answer(0, now.Add(5*time.Second))
			}
			if m.View().Dialog != nil {
				t.Fatal("Dialog bleibt offen")
			}
			if action != "antwort" {
				for _, c := range e.Commands {
					if c.Type == "antwort" {
						t.Fatal("Abbruch antwortet", e)
					}
				}
			}
			if len(m.Answer(0, now).Commands) != 0 {
				t.Fatal("zweite Antwort")
			}
		})
	}
}

func TestPanelSetupsBleibenAnPinUndNameGebunden(t *testing.T) {
	m := ready(t, "arbeitet")
	a := pipe.Setup{Pin: "demo", Setup: "a", Label: "Gleich", Startbar: true}
	b := pipe.Setup{Pin: "demo", Setup: "b", Label: "Gleich", Startbar: true}
	receive(t, m, pipe.Message{Type: "zustand", Zustand: "arbeitet", Netz: true, Setups: []pipe.Setup{a, b}})
	old := item(t, m, "setup:demo/a").ID
	receive(t, m, pipe.Message{Type: "zustand", Zustand: "arbeitet", Netz: true, Setups: []pipe.Setup{b, a}})
	e := m.Choose(old, now)
	if len(e.Commands) != 1 || e.Commands[0].Setup != "a" {
		t.Fatal("umgeleiteter Menü-Klick", e)
	}
	receive(t, m, pipe.Message{Type: "zustand", Zustand: "arbeitet", Netz: true, Setups: []pipe.Setup{b}})
	if len(m.Choose(old, now).Commands) != 0 {
		t.Fatal("entzogenes Setup gewählt")
	}
}
