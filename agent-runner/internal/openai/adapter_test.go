package openai

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRequestAndGolden(t *testing.T) {
	b, err := os.ReadFile("../../contract/proposed/model_request_openai.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Body json.RawMessage }
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	r := NewRequest("Prüfe Rechnungen.", "Prüfe die Rechnungen von heute.", []Tool{{Type: "function", Function: Definition{Name: "erp_list_things", Description: "Lists things.", Parameters: json.RawMessage(`{"type":"object"}`)}}}, 1024)
	got, err := json.Marshal(r)
	if err != nil || Validate(got) != nil {
		t.Fatalf("request: %s %v", got, err)
	}
	var x, y any
	_ = json.Unmarshal(got, &x)
	_ = json.Unmarshal(fixture.Body, &y)
	if !reflect.DeepEqual(x, y) {
		t.Fatalf("golden: %s", got)
	}
	for _, key := range []string{"model", "stream", "tool_choice", "system"} {
		if _, exists := x.(map[string]any)[key]; exists {
			t.Fatalf("forbidden generated field: %s", key)
		}
	}
}

func TestCompletionTokenField(t *testing.T) {
	r := NewRequest("system", "user", nil, 37)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	if string(body["max_completion_tokens"]) != "37" || body["max_tokens"] != nil {
		t.Fatalf("completion token field: %s", b)
	}
	if err := Validate(b); err != nil {
		t.Fatalf("generated request rejected: %v", err)
	}
	for _, raw := range []string{
		`{"messages":[{"role":"user","content":"x"}],"max_tokens":37}`,
		`{"messages":[{"role":"user","content":"x"}],"max_completion_tokens":37,"max_tokens":37}`,
	} {
		if Validate([]byte(raw)) == nil {
			t.Fatalf("legacy token field accepted: %s", raw)
		}
	}
}

func TestResponseTable(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason, text string
		calls                    int
		bad                      bool
	}{
		{"text", `{"choices":[{"message":{"role":"assistant","content":"fertig"},"finish_reason":"stop"}]}`, "stop", "fertig", 0, false},
		{"tool", `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"erp_list_things","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, "tool_calls", "", 1, false},
		{"multiple", `{"choices":[{"message":{"role":"assistant","content":"suche","tool_calls":[{"id":"a","type":"function","function":{"name":"erp_list_things","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"erp_list_things","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, "tool_calls", "suche", 2, false},
		{"invalid arguments retained for error reply", `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"erp_list_things","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`, "tool_calls", "", 1, false},
		{"length", `{"choices":[{"message":{"role":"assistant","content":"halb"},"finish_reason":"length"}]}`, "length", "halb", 0, false},
		{"empty choices", `{"choices":[]}`, "", "", 0, true},
		{"bad content", `{"choices":[{"message":{"role":"assistant","content":{"url":"SECRET"}},"finish_reason":"stop"}]}`, "", "", 0, true},
		{"missing id", `{"choices":[{"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"x","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, "", "", 0, true},
		{"provider tool", `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"a","type":"web_search","function":{"name":"x","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, "", "", 0, true},
		{"first choice only", `{"choices":[{"message":{"role":"assistant","content":"erste","refusal":null},"finish_reason":"stop"},{"message":{"role":"assistant","content":"zweite"},"finish_reason":"length"}]}`, "stop", "erste", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := ParseResponse([]byte(tc.body))
			if (err != nil) != tc.bad {
				t.Fatalf("error: %v", err)
			}
			if tc.bad {
				return
			}
			var text string
			_ = json.Unmarshal(r.Message.Content, &text)
			if r.FinishReason != tc.reason || text != tc.text || len(r.Message.ToolCalls) != tc.calls {
				t.Fatalf("response: %+v", r)
			}
		})
	}
}

func TestForbiddenRequests(t *testing.T) {
	base := `{"messages":[{"role":"user","content":"x"}],"max_completion_tokens":1}`
	for _, extra := range []string{`"model":"secret"`, `"stream":false`, `"tool_choice":"required"`, `"tools":[{"type":"web_search"}]`, `"tools":[{"type":"function","function":{"name":"x","parameters":{},"url":"secret"}}]`, `"system":"x"`} {
		t.Run(extra, func(t *testing.T) {
			if Validate([]byte(strings.Replace(base, `"max_completion_tokens":1`, `"max_completion_tokens":1,`+extra, 1))) == nil {
				t.Fatal("forbidden request accepted")
			}
		})
	}
	for _, msg := range []string{`{"role":"tool","content":"x"}`, `{"role":"tool","tool_call_id":"","content":"x"}`, `{"role":"user","content":{"url":"secret"}}`, `{"role":"admin","content":"x"}`, `{"role":"user","content":"x","url":"secret"}`} {
		if Validate([]byte(`{"messages":[`+msg+`],"max_completion_tokens":1}`)) == nil {
			t.Fatalf("forbidden message accepted: %s", msg)
		}
	}
	for _, raw := range []string{
		`null`, `[]`, `{"messages":[]}`, `{"messages":null}`,
		`{"messages":[{"role":"user","content":"x"}],"tools":null}`,
		`{"messages":[{"role":"user","content":"x"}],"tools":[{"type":"function","function":{"name":"x","parameters":null}}]}`,
	} {
		if Validate([]byte(raw)) == nil {
			t.Fatalf("invalid shape accepted: %s", raw)
		}
	}
	for _, call := range []string{
		`null`, `[{"id":"a","type":"function","function":{"name":"x","arguments":null}}]`,
		`[{"id":"a","type":"function","function":{"name":"x","arguments":{}}}]`,
		`[{"id":"a","type":"function","function":{"name":"x","arguments":"{}","url":"SECRET"}}]`,
		`[{"id":"a","type":"function","function":{"name":"","arguments":"{}"}}]`,
		`[{"id":"a","type":"function","function":{"name":"x","arguments":"{}"},"url":"SECRET"}]`,
	} {
		if Validate([]byte(`{"messages":[{"role":"assistant","tool_calls":`+call+`}]}`)) == nil {
			t.Fatalf("invalid call accepted: %s", call)
		}
	}
}

func TestAllowedRequestVocabulary(t *testing.T) {
	for _, role := range []string{"system", "user", "assistant", "tool"} {
		raw := `{"messages":[{"role":"` + role + `","content":"x","tool_call_id":"a"}],"max_completion_tokens":1}`
		if Validate([]byte(raw)) != nil {
			t.Fatalf("allowed request rejected: %s", raw)
		}
	}
}

func TestRunnerVocabularyRejected(t *testing.T) {
	for _, tc := range []struct{ name, fields string }{
		{"developer", `"messages":[{"role":"developer","content":"x"}]`},
		{"image parts", `"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,YQ=="}}]}]`},
		{"text parts", `"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}]`},
		{"provider tool", `"messages":[{"role":"user","content":"x"}],"tools":[{"type":"web_search"}]`},
		{"message name", `"messages":[{"role":"user","content":"x","name":"x"}]`},
		{"legacy max tokens", `"messages":[{"role":"user","content":"x"}],"max_tokens":1`},
		{"temperature", `"messages":[{"role":"user","content":"x"}],"temperature":0.2`},
		{"top p", `"messages":[{"role":"user","content":"x"}],"top_p":1`},
		{"stop", `"messages":[{"role":"user","content":"x"}],"stop":["END"]`},
		{"choice auto", `"messages":[{"role":"user","content":"x"}],"tool_choice":"auto"`},
		{"choice none", `"messages":[{"role":"user","content":"x"}],"tool_choice":"none"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if Validate([]byte(`{"max_completion_tokens":1,`+tc.fields+`}`)) == nil {
				t.Fatal("runner accepted vocabulary it does not generate")
			}
		})
	}
}

func TestToolResultTextProjection(t *testing.T) {
	for _, raw := range []string{`{"content":[{"type":"text","text":"ok"}],"structuredContent":{"n":1}}`, `{"content":[{"type":"image","url":"SECRET"}]}`, `{"isError":true,"content":[{"type":"text","text":"Fehler"}]}`} {
		m := ToolResult("a", json.RawMessage(raw))
		r := NewRequest("", "", nil, 1)
		r.Messages = []Message{m}
		b, _ := json.Marshal(r)
		if Validate(b) != nil || m.Role != "tool" || m.ToolCallID != "a" || strings.Contains(string(b), "SECRET") {
			t.Fatalf("tool projection: %s", b)
		}
		if strings.Contains(raw, `"n"`) && !strings.Contains(string(m.Content), `\"n\":1`) {
			t.Fatal("structured result lost")
		}
		if strings.Contains(raw, "SECRET") && !strings.Contains(string(m.Content), Placeholder) {
			t.Fatal("missing placeholder")
		}
	}
}
