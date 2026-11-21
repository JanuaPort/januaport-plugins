package runner

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// JanuaPort/januaport#1438: Alle Werkzeugaufrufe einer Antwort werden geprüft,
// bevor einer läuft. Fail closed: Vor der Abweisung tritt keine Wirkung ein.
func TestInvalidCallRejectsWholeAnswerBeforeAnyExecution(t *testing.T) {
	valid := `{"type":"tool_use","id":"a","name":"erp_list_things","input":{"q":"` + secret + `"}}`
	for _, tc := range []struct{ name, content string }{
		{"gueltig_dann_unbekannt", `[` + valid + `,{"type":"tool_use","id":"b","name":"not_listed","input":{}}]`},
		{"unbekannt_dann_gueltig", `[{"type":"tool_use","id":"b","name":"not_listed","input":{}},` + valid + `]`},
		{"gueltig_dann_doppelte_id", `[` + valid + `,{"type":"tool_use","id":"a","name":"erp_list_things","input":{"q":"` + secret + `"}}]`},
		{"zwei_gueltige_dann_unbekannt", `[` + valid + `,{"type":"tool_use","id":"c","name":"erp_list_things","input":{"q":"` + secret + `"}},{"type":"tool_use","id":"d","name":"x y","input":{}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) { modelReply(w, tc.content, "tool_use") }}
			r, _, logs := setup(t, f)
			_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
			end(t, b, "error", 1)
			f.mu.Lock()
			calls := f.calls
			f.mu.Unlock()
			if calls != 0 {
				t.Fatalf("%d Werkzeugaufruf(e) vor der Abweisung ausgeführt", calls)
			}
			if got := logs.String(); strings.Contains(got, "erp_list_things") {
				t.Fatalf("Protokoll nennt ein nie ausgeführtes Werkzeug: %s", got)
			}
		})
	}
}
