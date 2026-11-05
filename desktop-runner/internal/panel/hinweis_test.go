package panel

import (
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

func TestHinweisFesterText(t *testing.T) {
	// Der Hinweis steht sichtbar auf dem Bildschirm: fester Text, nie Inhalt der Rückfrage.
	if NoticeTitle != "JanuaPort" || NoticeText != "JanuaPort braucht eine Antwort" {
		t.Fatalf("Hinweistext geändert: %q / %q", NoticeTitle, NoticeText)
	}
}

func TestHinweisEinmalJeNeuerFrage(t *testing.T) {
	second := question()
	second.ID = "q2"
	reuse := question() // Skripte zählen ihre Fragen je Prozess ab "1": gleiche ID, neue Frage.
	taken := func(t *testing.T, m *Model) {
		t.Helper()
		if !m.TakeNotice() {
			t.Fatal("erste Frage ohne Hinweis")
		}
	}
	for _, tc := range []struct {
		name  string
		steps func(t *testing.T, m *Model)
		want  bool // Hinweis steht danach noch an
	}{
		{"neue Frage meldet", func(t *testing.T, m *Model) {
			receive(t, m, question())
		}, true},
		{"View und Tick verbrauchen den Hinweis nicht", func(t *testing.T, m *Model) {
			receive(t, m, question())
			for range 5 {
				m.View()
			}
			m.Tick(now)
		}, true},
		{"zweite Frage wird nicht angezeigt und nicht gemeldet", func(t *testing.T, m *Model) {
			receive(t, m, question())
			taken(t, m)
			receive(t, m, second)
		}, false},
		{"Klick auf Rückfrage anzeigen meldet nicht erneut", func(t *testing.T, m *Model) {
			receive(t, m, question())
			taken(t, m)
			m.Choose("frage", now)
			m.HideDialog()
		}, false},
		{"gleiche ID nach Antwort ist eine neue Frage", func(t *testing.T, m *Model) {
			receive(t, m, question())
			taken(t, m)
			m.Choose("frage", now)
			m.Answer(0, now)
			receive(t, m, reuse)
		}, true},
		{"nach frage_weg ohne Abholen kein Hinweis", func(t *testing.T, m *Model) {
			receive(t, m, question())
			receive(t, m, pipe.Message{Type: "frage_weg", ID: "q1"})
		}, false},
		{"nach Fristende ohne Abholen kein Hinweis", func(t *testing.T, m *Model) {
			receive(t, m, question())
			m.Tick(now.Add(5 * time.Second))
		}, false},
		{"nach Anhalten ohne Abholen kein Hinweis", func(t *testing.T, m *Model) {
			receive(t, m, question())
			receive(t, m, pipe.Message{Type: "zustand", Zustand: "angehalten"})
		}, false},
		{"nach Verbindungsverlust ohne Abholen kein Hinweis", func(t *testing.T, m *Model) {
			receive(t, m, question())
			m.Connection(Disconnected)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := ready(t, "arbeitet")
			tc.steps(t, m)
			if got := m.TakeNotice(); got != tc.want {
				t.Fatalf("TakeNotice = %t, erwartet %t", got, tc.want)
			}
			if m.TakeNotice() {
				t.Fatal("Hinweis zweimal abholbar")
			}
		})
	}
}

func TestHinweisNurFuerAngezeigteFrage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model func(t *testing.T) *Model
	}{
		{"nicht verbunden", func(t *testing.T) *Model { return New() }},
		{"verbunden ohne Zustand", func(t *testing.T) *Model {
			m := New()
			m.Connection(Connected)
			return m
		}},
		{"angehalten", func(t *testing.T) *Model { return ready(t, "angehalten") }},
		{"Störung", func(t *testing.T) *Model { return ready(t, "stoerung") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model(t)
			receive(t, m, question())
			if m.TakeNotice() {
				t.Fatal("Hinweis für nicht angezeigte Frage")
			}
		})
	}
}
