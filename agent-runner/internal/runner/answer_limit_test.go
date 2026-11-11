package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Provider usage deliberately differs from the gateway's authoritative envelope.
func budgetReply(w http.ResponseWriter, protocol string, tool bool, stop string, used int) {
	var response any
	if protocol == "openai" {
		message := map[string]any{"role": "assistant", "content": secret}
		if tool {
			message["tool_calls"] = json.RawMessage(`[` + openAIUse("a", `{"q":"`+secret+`"}`) + `]`)
		}
		if stop == "" {
			stop = "stop"
			if tool {
				stop = "tool_calls"
			}
		}
		response = map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": stop}}, "usage": map[string]int{"completion_tokens": 77}}
	} else {
		content := `[{"type":"text","text":"` + secret + `"}]`
		if tool {
			content = `[{"type":"tool_use","id":"a","name":"erp_list_things","input":{"q":"` + secret + `"}}]`
		}
		if stop == "" {
			stop = "end_turn"
			if tool {
				stop = "tool_use"
			}
		}
		response = map[string]any{"role": "assistant", "content": json.RawMessage(content), "stop_reason": stop, "usage": map[string]int{"output_tokens": 77}}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"response": response, "usage": map[string]int{"tokens_in": 2, "tokens_out": used}})
}

func TestAnswerLimitStopAtAdapter(t *testing.T) {
	for _, tc := range []struct{ protocol, stop string }{{"anthropic", "max_tokens"}, {"openai", "length"}} {
		t.Run(tc.protocol, func(t *testing.T) {
			core := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
				// Truncated tool calls must not execute or cause a continuation.
				budgetReply(w, tc.protocol, true, tc.stop, 8192)
			}}
			r, _, logs := setup(t, core)
			q := requestFixture(t)
			q.Arguments.Model.Protocol = modelProtocol(tc.protocol)
			q.Arguments.Limits.TokensOut = 60000
			_, out := invoke(t, r, context.Background(), q, nil)
			end(t, out, "limit", 1)
			if !strings.Contains(string(out), `"detail":"tokens_out"`) || !strings.Contains(string(out), `"isError":true`) || !strings.Contains(string(out), fixedMessage("limit", "tokens_out")) {
				t.Fatalf("wrong answer limit result: %s", out)
			}
			if core.calls != 0 || len(core.requests) != 1 || strings.Contains(string(out)+logs.String(), secret) {
				t.Fatalf("truncated answer executed, continued or leaked: %s; log=%s", out, logs.String())
			}
			var record struct {
				End, Detail      string
				Steps, ToolCalls int
			}
			if json.Unmarshal(logs.Bytes(), &record) != nil || record.End != "limit" || record.Detail != "tokens_out" || record.Steps != 1 || record.ToolCalls != 0 {
				t.Fatal(logs.String())
			}
		})
	}
}
