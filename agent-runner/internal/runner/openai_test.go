package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Inject on the real incoming wire, so unknown fields cannot disappear in a struct.
func protocolEdit(protocol string) func(*http.Request) {
	return func(req *http.Request) {
		var rpc map[string]any
		_ = json.NewDecoder(req.Body).Decode(&rpc)
		model := rpc["params"].(map[string]any)["arguments"].(map[string]any)["model"].(map[string]any)
		model["protocol"] = protocol
		b, _ := json.Marshal(rpc)
		req.Body = http.NoBody
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
}

func openAIReply(w http.ResponseWriter, message, reason string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"choices": []any{map[string]any{"message": json.RawMessage(message), "finish_reason": reason}}, "usage": map[string]int{"prompt_tokens": 2, "completion_tokens": 1}}, "usage": map[string]int{"tokens_in": 2, "tokens_out": 1}})
}

func openAIUse(id, arguments string) string {
	args, _ := json.Marshal(arguments)
	return `{"id":"` + id + `","type":"function","function":{"name":"erp_list_things","arguments":` + string(args) + `}}`
}

func TestOpenAILoopTable(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		uses                    int
		arguments, reason, want string
		calls, steps            int
	}{
		{"text", 0, "", "stop", "ok", 0, 1},
		{"one tool", 1, `{"q":"` + secret + `"}`, "tool_calls", "ok", 1, 2},
		{"two tools", 2, `{"q":"` + secret + `"}`, "tool_calls", "ok", 2, 2},
		{"invalid arguments", 1, `{SECRET`, "tool_calls", "ok", 0, 2},
		{"non-object arguments", 1, `[]`, "tool_calls", "ok", 0, 2},
		{"length", 0, "", "length", "limit", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
				if n == 1 && tc.uses > 0 {
					uses := []string{}
					for i := 0; i < tc.uses; i++ {
						uses = append(uses, openAIUse(fmt.Sprint(i), tc.arguments))
					}
					openAIReply(w, `{"role":"assistant","content":null,"tool_calls":[`+strings.Join(uses, ",")+`]}`, tc.reason)
				} else {
					reason := "stop"
					if tc.uses == 0 {
						reason = tc.reason
					}
					openAIReply(w, `{"role":"assistant","content":"fertig"}`, reason)
				}
			}}
			r, _, logs := setup(t, f)
			_, out := invoke(t, r, context.Background(), requestFixture(t), protocolEdit("openai"))
			end(t, out, tc.want, tc.steps)
			if f.calls != tc.calls {
				t.Fatalf("tool calls: %d", f.calls)
			}
			if tc.reason == "length" && !strings.Contains(string(out), `"detail":"tokens_out"`) {
				t.Fatal(string(out))
			}
			for _, req := range f.requests {
				if _, ok := req["model"]; ok {
					t.Fatal("model sent")
				}
				if _, ok := req["stream"]; ok {
					t.Fatal("stream sent")
				}
				msgs := req["messages"].([]any)
				if msgs[0].(map[string]any)["role"] != "system" {
					t.Fatal("system missing")
				}
				tools := req["tools"].([]any)
				if tools[0].(map[string]any)["type"] != "function" {
					t.Fatal("tool shape")
				}
			}
			if tc.steps == 2 {
				msgs := f.requests[1]["messages"].([]any)
				if len(msgs) != 3+tc.uses {
					t.Fatal("history lost")
				}
				for i := 0; i < tc.uses; i++ {
					m := msgs[3+i].(map[string]any)
					if m["role"] != "tool" || m["tool_call_id"] != fmt.Sprint(i) {
						t.Fatal("tool_call_id missing")
					}
					if tc.calls == 0 && (!strings.Contains(m["content"].(string), "gültiges JSON-Objekt") || strings.Contains(m["content"].(string), "SECRET")) {
						t.Fatal("invalid arguments error missing or leaked")
					}
				}
			}
			if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "SECRET") {
				t.Fatal("log leaked content")
			}
			if tc.calls == 2 && (!strings.Contains(logs.String(), `"tool_calls":2`) || !strings.Contains(logs.String(), `"end":"ok"`)) {
				t.Fatal(logs.String())
			}
		})
	}
}

func TestOpenAIGatewayErrors(t *testing.T) {
	for _, tc := range []struct{ file, want string }{{"model_error_rejected.json", "error"}, {"model_error_limit.json", "limit"}, {"model_error_provider.json", "error"}} {
		t.Run(tc.file, func(t *testing.T) {
			var g struct {
				Status int
				Body   map[string]any
			}
			_ = json.Unmarshal(golden(t, tc.file), &g)
			g.Body["error"].(map[string]any)["message"] = secret
			f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
				w.WriteHeader(g.Status)
				_ = json.NewEncoder(w).Encode(g.Body)
			}}
			r, _, log := setup(t, f)
			_, out := invoke(t, r, context.Background(), requestFixture(t), protocolEdit("openai"))
			end(t, out, tc.want, 1)
			if f.calls != 0 || strings.Contains(string(out)+log.String(), secret) {
				t.Fatal("gateway error leaked or tool called")
			}
		})
	}
}

func TestProtocolDefaultAndUnknownAtAdapter(t *testing.T) {
	for _, p := range []string{"", "anthropic", "UNKNOWN-SECRET", "OPENAI"} {
		t.Run(p, func(t *testing.T) {
			f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
				modelReply(w, `[{"type":"text","text":"fertig"}]`, "end_turn")
			}}
			r, _, logs := setup(t, f)
			var edit func(*http.Request)
			if p != "" {
				edit = protocolEdit(p)
			}
			w, out := invoke(t, r, context.Background(), requestFixture(t), edit)
			if p == "" || p == "anthropic" {
				end(t, out, "ok", 1)
				if f.requests[0]["system"] == nil {
					t.Fatal("default changed")
				}
				return
			}
			if len(f.requests) != 0 || f.calls != 0 || logs.String() != "" || !strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), p) || !strings.Contains(w.Body.String(), errRequest.Error()) {
				t.Fatal("unknown protocol not rejected with fixed error: " + w.Body.String())
			}
		})
	}
}

func TestOpenAIUnsafeResponsesAtAdapter(t *testing.T) {
	for _, body := range []string{`{"choices":[]}`, `{"choices":[{"message":{"role":"assistant","content":{"url":"SECRET"}},"finish_reason":"stop"}]}`, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"a","type":"web_search","function":{"name":"x","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`} {
		f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) { fmt.Fprint(w, `{"response":`+body+`}`) }}
		r, _, _ := setup(t, f)
		_, out := invoke(t, r, context.Background(), requestFixture(t), protocolEdit("openai"))
		end(t, out, "error", 1)
		if f.calls != 0 {
			t.Fatal("unsafe call")
		}
	}
}
