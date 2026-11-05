package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// JanuaPort/januaport#1338: Endet das Modell, und im letzten Werkzeug-Schritt scheiterte
// mindestens ein Aufruf, endet der Lauf error/tools. Gescheitert heißt isError im
// Werkzeug-Ergebnis oder der feste Fehler bei ungültigem JSON. Ein Fehler, nach dem ein
// Schritt ohne Fehler folgt, ist Erholung und bleibt ok.

const toolsSentence = "Der Agenten-Lauf ist beendet, weil im letzten Werkzeug-Schritt ein Aufruf gescheitert ist."

const (
	callOK      = "ok"
	callErr     = "err"
	callBadJSON = "badjson"
)

// configErrorResult ist der Fall aus dem Ticket: das Werkzeug meldet isError.
const configErrorResult = `{"isError":true,"content":[{"type":"text","text":"config_error"}]}`

func toolFailureModel(protocol string, script [][]string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, _ *http.Request, n int) {
		if n > len(script) {
			if protocol == "openai" {
				openAIReply(w, `{"role":"assistant","content":"fertig"}`, "stop")
				return
			}
			modelReply(w, `[{"type":"text","text":"fertig"}]`, "end_turn")
			return
		}
		var blocks []string
		for i, kind := range script[n-1] {
			id := fmt.Sprintf("c%d_%d", n, i)
			switch {
			case protocol == "openai" && kind == callBadJSON:
				blocks = append(blocks, openAIUse(id, `{SECRET`))
			case protocol == "openai":
				blocks = append(blocks, openAIUse(id, `{"q":"`+secret+`"}`))
			default:
				blocks = append(blocks, `{"type":"tool_use","id":"`+id+`","name":"erp_list_things","input":{"q":"`+secret+`"}}`)
			}
		}
		if protocol == "openai" {
			openAIReply(w, `{"role":"assistant","content":null,"tool_calls":[`+strings.Join(blocks, ",")+`]}`, "tool_calls")
			return
		}
		modelReply(w, `[`+strings.Join(blocks, ",")+`]`, "tool_use")
	}
}

// reached sind die Ergebnisse der Aufrufe, die den Kern erreichen (ungültiges JSON nicht).
func reached(script [][]string) []string {
	var out []string
	for _, step := range script {
		for _, kind := range step {
			if kind != callBadJSON {
				out = append(out, kind)
			}
		}
	}
	return out
}

func TestToolFailureEndsRunAtAdapter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		script     [][]string
		wantErr    bool
		openAIOnly bool
	}{
		{"alle Aufrufe scheitern (Ticket-Fall)", [][]string{{callErr}, {callErr}}, true, false},
		{"einziger Aufruf scheitert", [][]string{{callErr}}, true, false},
		{"letzter scheitert nach Erfolg", [][]string{{callOK}, {callErr}}, true, false},
		{"Fehler, dann Erfolg ist Erholung", [][]string{{callErr}, {callOK}}, false, false},
		{"Erfolg", [][]string{{callOK}}, false, false},
		{"Schritt mit zwei Aufrufen, der erste scheitert", [][]string{{callErr, callOK}}, true, false},
		{"Schritt mit zwei Aufrufen, der zweite scheitert", [][]string{{callOK, callErr}}, true, false},
		{"Schritt mit zwei Aufrufen ohne Fehler", [][]string{{callOK, callOK}}, false, false},
		{"Fehlerschritt mit zwei Aufrufen, dann Erfolg", [][]string{{callErr, callOK}, {callOK}}, false, false},
		{"Erfolg, dann Fehlerschritt mit zwei Aufrufen", [][]string{{callOK}, {callErr, callErr}}, true, false},
		{"Lauf ohne Aufruf", nil, false, false},
		{"ungültiges JSON", [][]string{{callBadJSON}}, true, true},
		{"ungültiges JSON, dann Erfolg ist Erholung", [][]string{{callBadJSON}, {callOK}}, false, true},
		{"Erfolg, dann ungültiges JSON", [][]string{{callOK}, {callBadJSON}}, true, true},
		{"ungültiges JSON neben Erfolg im selben Schritt", [][]string{{callBadJSON, callOK}}, true, true},
	} {
		for _, protocol := range []string{"anthropic", "openai"} {
			if tc.openAIOnly && protocol != "openai" {
				continue
			}
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				results := reached(tc.script)
				f := &fakeCore{model: toolFailureModel(protocol, tc.script), toolFor: func(n int) json.RawMessage {
					if results[n-1] == callErr {
						return json.RawMessage(configErrorResult)
					}
					return json.RawMessage(`{"content":[{"type":"text","text":"` + secret + `"}]}`)
				}}
				r, _, logs := setup(t, f)
				edit := protocolEdit(protocol)
				_, out := invoke(t, r, context.Background(), requestFixture(t), edit)
				steps := len(tc.script) + 1
				if f.calls != len(results) || len(f.requests) != steps {
					t.Fatalf("Werkzeugaufrufe %d (erwartet %d), Modell-Durchgänge %d (erwartet %d)", f.calls, len(results), len(f.requests), steps)
				}
				if !tc.wantErr {
					end(t, out, "ok", steps)
					if !strings.Contains(string(out), `"text":"fertig"`) || strings.Contains(string(out), `"detail"`) {
						t.Fatal("Erfolg ohne Modelltext oder mit Detail: " + string(out))
					}
					return
				}
				end(t, out, "error", steps)
				if !strings.Contains(string(out), `"detail":"tools"`) || !strings.Contains(string(out), toolsSentence) {
					t.Fatal("Detail oder fester Satz fehlt: " + string(out))
				}
				for _, leak := range []string{"fertig", "config_error", secret, "SECRET"} {
					if strings.Contains(string(out), leak) || strings.Contains(logs.String(), leak) {
						t.Fatalf("Inhalt %q in Antwort oder Protokoll: %s %s", leak, out, logs.String())
					}
				}
				if !strings.Contains(logs.String(), `"end":"error"`) || !strings.Contains(logs.String(), `"detail":"tools"`) {
					t.Fatal(logs.String())
				}
			})
		}
	}
}

// Die Wertung gilt nur, wenn das Modell endet. Eine Grenze im selben Lauf behält ihre Klasse.
func TestToolFailureDoesNotOverrideLimit(t *testing.T) {
	f := &fakeCore{
		model: func(w http.ResponseWriter, _ *http.Request, n int) {
			modelReply(w, `[{"type":"tool_use","id":"x`+fmt.Sprint(n)+`","name":"erp_list_things","input":{"q":"`+secret+`"}}]`, "tool_use")
		},
		tool: json.RawMessage(configErrorResult),
	}
	r, _, _ := setup(t, f)
	q := requestFixture(t)
	q.Arguments.Limits.Steps = 2
	_, out := invoke(t, r, context.Background(), q, nil)
	end(t, out, "limit", 2)
	if !strings.Contains(string(out), `"detail":"steps"`) {
		t.Fatal(string(out))
	}
}
