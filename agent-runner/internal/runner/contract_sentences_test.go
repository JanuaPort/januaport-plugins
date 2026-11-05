package runner

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const (
	genericLimitSentence = "Der Agenten-Lauf hat eine seiner Grenzen erreicht und ist beendet."
	genericErrorSentence = "Der Agenten-Lauf ist wegen eines Fehlers beendet."
)

// JanuaPort/januaport#1335: Die Meldung nennt, an welcher Grenze der Lauf endete.
// Ein unbekanntes Detail behält den bisherigen Satz; das Detail wird nie ins Wort übernommen.
func TestFixedMessagePerDetail(t *testing.T) {
	for _, tc := range []struct{ end, detail, want string }{
		{"limit", "steps", "Der Agenten-Lauf hat die Grenze für Schritte erreicht und ist beendet."},
		{"limit", "tool_calls", "Der Agenten-Lauf hat die Grenze für Werkzeug-Aufrufe erreicht und ist beendet."},
		{"limit", "tokens_in", "Der Agenten-Lauf hat die Grenze für Eingabe-Tokens erreicht und ist beendet."},
		{"limit", "tokens_out", "Der Agenten-Lauf hat die Grenze für Ausgabe-Tokens erreicht und ist beendet."},
		{"limit", "wall", "Der Agenten-Lauf hat die Grenze für die Laufzeit erreicht und ist beendet."},
		{"limit", "content_filter", "Der Modellanbieter hat die Antwort gefiltert; der Agenten-Lauf ist beendet."},
		{"limit", "", genericLimitSentence},
		{"limit", "erfunden", genericLimitSentence},
		{"limit", "tools", genericLimitSentence},
		{"limit", "gateway", genericLimitSentence},
		{"error", "tools", toolsSentence},
		{"error", "provider", "Der Modellanbieter hat den Aufruf mit einem Fehler beantwortet; sein Text wird nicht weitergegeben."},
		{"error", "rejected", "Der Modell-Durchgang hat die Anfrage des Agenten-Läufers abgewiesen."},
		{"error", "gateway", genericErrorSentence},
		{"error", "", genericErrorSentence},
		{"error", "erfunden", genericErrorSentence},
		{"error", "steps", genericErrorSentence},
		{"error", "content_filter", genericErrorSentence},
		{"cancelled", "wall", "Der Agenten-Lauf wurde abgebrochen."},
		{"revoked", "tools", "Die Freigabe für den Agenten-Lauf wurde entzogen."},
	} {
		t.Run(tc.end+"/"+tc.detail, func(t *testing.T) {
			if got := fixedMessage(tc.end, tc.detail); got != tc.want {
				t.Fatalf("fixedMessage(%q,%q) = %q, erwartet %q", tc.end, tc.detail, got, tc.want)
			}
			if strings.Contains(fixedMessage(tc.end, tc.detail), tc.detail) && tc.detail != "" {
				t.Fatal("das Detail steht wörtlich im Satz")
			}
		})
	}
}

// Die Sätze kommen über den echten Weg an: Grenze und Meldung stimmen überein.
func TestLimitSentencesAtAdapter(t *testing.T) {
	for _, tc := range []struct {
		name                string
		steps, calls        int
		detail, wantSentenc string
	}{
		{"steps", 2, 50, "steps", "Der Agenten-Lauf hat die Grenze für Schritte erreicht und ist beendet."},
		{"tool_calls", 20, 1, "tool_calls", "Der Agenten-Lauf hat die Grenze für Werkzeug-Aufrufe erreicht und ist beendet."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCore{model: func(w http.ResponseWriter, _ *http.Request, _ int) {
				modelReply(w, `[{"type":"tool_use","id":"x","name":"erp_list_things","input":{"q":"`+secret+`"}}]`, "tool_use")
			}}
			r, _, _ := setup(t, f)
			q := requestFixture(t)
			q.Arguments.Limits.Steps = tc.steps
			q.Arguments.Limits.ToolCalls = tc.calls
			_, out := invoke(t, r, context.Background(), q, nil)
			if !strings.Contains(string(out), `"detail":"`+tc.detail+`"`) || !strings.Contains(string(out), tc.wantSentenc) {
				t.Fatal(string(out))
			}
		})
	}
}
