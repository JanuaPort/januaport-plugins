package anthropic

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestGoldenModelRequest(t *testing.T) {
	b, err := os.ReadFile("../../contract/golden/model_request.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Body json.RawMessage }
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if err := Validate(fixture.Body); err != nil {
		t.Fatal(err)
	}
	var r Request
	if err := json.Unmarshal(fixture.Body, &r); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(r)
	var a, c any
	_ = json.Unmarshal(fixture.Body, &a)
	_ = json.Unmarshal(got, &c)
	if !reflect.DeepEqual(a, c) {
		t.Fatalf("golden roundtrip: %s", got)
	}
}

func TestForbiddenModelPayloads(t *testing.T) {
	base := `{"messages":[{"role":"user","content":"hello"}],"max_tokens":1024}`
	cases := []string{
		strings.Replace(base, `"max_tokens":1024`, `"max_tokens":1024,"model":"secret-model"`, 1),
		strings.Replace(base, `"max_tokens":1024`, `"max_tokens":1024,"stream":false`, 1),
		strings.Replace(base, `"max_tokens":1024`, `"max_tokens":1024,"tool_choice":{"type":"tool","name":"x"}`, 1),
		strings.Replace(base, `"max_tokens":1024`, `"max_tokens":1024,"tools":[{"type":"web_search_20250305","name":"web_search"}]`, 1),
		strings.Replace(base, `"content":"hello"`, `"content":"hello","name":"x"`, 1),
		strings.Replace(base, `"hello"`, `[{"type":"image","source":{"type":"url","url":"https://example.invalid/canary"}}]`, 1),
		strings.Replace(base, `"hello"`, `[{"type":"document","source":{"type":"url","url":"https://example.invalid/canary"}}]`, 1),
		strings.Replace(base, `"hello"`, `[{"type":"server_tool_use","name":"web_search"}]`, 1),
		strings.Replace(base, `"hello"`, `[{"type":"text","text":"hello","url":"https://example.invalid"}]`, 1),
		strings.Replace(base, `"hello"`, `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"!invalid!"}}]`, 1),
		strings.Replace(base, `"hello"`, `[{"type":"image","source":{"type":"url","media_type":"image/png","data":"YQ=="}}]`, 1),
		strings.Replace(base, `"hello"`, `[{"type":"tool_use","name":"tool","input":{}}]`, 1),
		strings.Replace(base, `"hello"`, `[{"type":"tool_result","tool_use_id":"x","content":[{"type":"image","source":{"type":"url","url":"https://example.invalid/canary"}}]}]`, 1),
	}
	for i, b := range cases {
		if Validate([]byte(b)) == nil {
			t.Errorf("forbidden case %d accepted: %s", i, b)
		}
	}
}

func TestAllowedBase64Blocks(t *testing.T) {
	for _, tc := range []struct{ kind, media string }{{"image", "image/png"}, {"document", "application/pdf"}} {
		b := json.RawMessage(`[{"type":"` + tc.kind + `","source":{"type":"base64","media_type":"` + tc.media + `","data":"YQ=="}}]`)
		if e := ValidateContent(b); e != nil {
			t.Fatal(e)
		}
	}
}

func TestThinkingAndCache(t *testing.T) {
	raw := json.RawMessage(`[{"type":"thinking","thinking":"private","signature":"  signed\\nvalue  "},{"type":"redacted_thinking","data":"opaque=="},{"type":"tool_use","id":"one","name":"erp_list_things","input":{}}]`)
	req := NewRequest("instructions", "input", []Tool{{Name: "erp_list_things", InputSchema: json.RawMessage(`{"type":"object"}`)}}, 1024)
	req.Messages = append(req.Messages, Message{Role: "assistant", Content: raw})
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if err = Validate(b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(req.Messages[1].Content, raw) {
		t.Fatal("thinking changed")
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["model"] != nil || m["stream"] != nil || m["tool_choice"] != nil {
		t.Fatal("unexpected provider fields")
	}
	if !strings.Contains(string(req.System), `"cache_control":{"type":"ephemeral"}`) || req.Tools[0].CacheControl["type"] != "ephemeral" {
		t.Fatal("cache missing")
	}
}

func TestToolResultSanitization(t *testing.T) {
	for _, kind := range []string{"image", "document", "resource", "server_tool_use"} {
		raw := json.RawMessage(`{"content":[{"type":"` + kind + `","source":{"type":"url","url":"https://example.invalid/SECRET"}}]}`)
		b := ToolResult("one", raw)
		if strings.Contains(string(b), "SECRET") || !strings.Contains(string(b), Placeholder) {
			t.Fatalf("unsafe result: %s", b)
		}
		r := NewRequest("s", "i", nil, 1024)
		r.Messages = append(r.Messages, Message{Role: "user", Content: json.RawMessage("[" + string(b) + "]")})
		encoded, _ := json.Marshal(r)
		if err := Validate(encoded); err != nil {
			t.Fatal(err)
		}
	}
}
