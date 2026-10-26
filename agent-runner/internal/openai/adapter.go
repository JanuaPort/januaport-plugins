// Package openai builds Chat Completions bodies accepted by the core gateway.
package openai

import (
	"encoding/json"
	"errors"
	"strings"
)

const Placeholder = "[Werkzeug-Inhalt in diesem Modell-Durchgang nicht darstellbar.]"

var errPayload = errors.New("Modell-Nachricht ist nicht zulässig")

type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Definition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Tool struct {
	Type     string     `json:"type"`
	Function Definition `json:"function"`
}

type Request struct {
	Messages  []Message `json:"messages"`
	Tools     []Tool    `json:"tools,omitempty"`
	MaxTokens int       `json:"max_tokens"`
}

type Response struct {
	Message      Message
	FinishReason string
}

func NewRequest(instructions, input string, tools []Tool, maxTokens int) Request {
	system, _ := json.Marshal(instructions)
	user, _ := json.Marshal(input)
	return Request{Messages: []Message{{Role: "system", Content: system}, {Role: "user", Content: user}}, Tools: tools, MaxTokens: maxTokens}
}

func object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	err := json.Unmarshal(raw, &m)
	return m, err == nil && m != nil
}

func only(m map[string]json.RawMessage, names string) bool {
	for k := range m {
		if !strings.Contains(" "+names+" ", " "+k+" ") {
			return false
		}
	}
	return true
}

func str(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }

// Validate protects every outgoing history, including correlated tool results.
// The runner emits text only; the core's richer image vocabulary is unnecessary.
func Validate(raw []byte) error {
	m, ok := object(raw)
	if !ok || !only(m, "messages tools max_tokens") {
		return errPayload
	}
	var messages []json.RawMessage
	if json.Unmarshal(m["messages"], &messages) != nil || len(messages) == 0 {
		return errPayload
	}
	for _, raw := range messages {
		if validateMessage(raw) != nil {
			return errPayload
		}
	}
	if raw, has := m["tools"]; has {
		var tools []json.RawMessage
		if json.Unmarshal(raw, &tools) != nil || tools == nil {
			return errPayload
		}
		for _, raw := range tools {
			t, ok := object(raw)
			fn, fnOK := object(t["function"])
			_, schemaOK := object(fn["parameters"])
			if !ok || !only(t, "type function") || str(t["type"]) != "function" || !fnOK || !only(fn, "name description parameters") || str(fn["name"]) == "" || !schemaOK {
				return errPayload
			}
		}
	}
	return nil
}

func validateMessage(raw json.RawMessage) error {
	m, ok := object(raw)
	if !ok || !only(m, "role content tool_calls tool_call_id") {
		return errPayload
	}
	switch str(m["role"]) {
	case "system", "user", "assistant":
	case "tool":
		if str(m["tool_call_id"]) == "" {
			return errPayload
		}
	default:
		return errPayload
	}
	if content, has := m["content"]; has && string(content) != "null" {
		var text string
		if json.Unmarshal(content, &text) != nil {
			return errPayload
		}
	}
	if raw, has := m["tool_calls"]; has {
		var calls []json.RawMessage
		if json.Unmarshal(raw, &calls) != nil || calls == nil {
			return errPayload
		}
		for _, raw := range calls {
			c, ok := object(raw)
			fn, fnOK := object(c["function"])
			var args string
			if !ok || !only(c, "id type function") || str(c["id"]) == "" || str(c["type"]) != "function" || !fnOK || !only(fn, "name arguments") || str(fn["name"]) == "" || string(fn["arguments"]) == "null" || json.Unmarshal(fn["arguments"], &args) != nil {
				return errPayload
			}
		}
	}
	return nil
}

func ParseResponse(raw []byte) (Response, error) {
	var envelope struct {
		Choices []struct {
			Message      json.RawMessage `json:"message"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Choices) == 0 {
		return Response{}, errPayload
	}
	first := envelope.Choices[0]
	m, ok := object(first.Message)
	if !ok || str(m["role"]) != "assistant" {
		return Response{}, errPayload
	}
	// Provider metadata (e.g. refusal or reasoning) is not forwarded to the core.
	projected := map[string]json.RawMessage{}
	for _, key := range []string{"role", "content", "tool_calls"} {
		if v, has := m[key]; has {
			projected[key] = v
		}
	}
	b, _ := json.Marshal(projected)
	if validateMessage(b) != nil {
		return Response{}, errPayload
	}
	var message Message
	if json.Unmarshal(b, &message) != nil {
		return Response{}, errPayload
	}
	return Response{Message: message, FinishReason: first.FinishReason}, nil
}

// ToolResult projects text and structured MCP results into a tool message.
// Other content gets the same fixed placeholder as the Messages adapter.
func ToolResult(id string, raw json.RawMessage) Message {
	var result struct {
		Content           []map[string]json.RawMessage `json:"content"`
		StructuredContent json.RawMessage              `json:"structuredContent"`
	}
	_ = json.Unmarshal(raw, &result)
	parts := []string{}
	for _, block := range result.Content {
		if str(block["type"]) == "text" {
			parts = append(parts, str(block["text"]))
		} else {
			parts = append(parts, Placeholder)
		}
	}
	if len(result.StructuredContent) > 0 {
		parts = append(parts, string(result.StructuredContent))
	}
	if len(parts) == 0 {
		parts = append(parts, Placeholder)
	}
	content, _ := json.Marshal(strings.Join(parts, "\n"))
	return Message{Role: "tool", ToolCallID: id, Content: content}
}
