// Package anthropic builds the Messages body accepted by the core gateway.
package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

const Placeholder = "[Werkzeug-Inhalt in diesem Modell-Durchgang nicht darstellbar.]"

var errPayload = errors.New("Modell-Nachricht ist nicht zulässig")

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
type Tool struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	InputSchema  json.RawMessage   `json:"input_schema"`
	CacheControl map[string]string `json:"cache_control,omitempty"`
}
type Request struct {
	Messages  []Message       `json:"messages"`
	System    json.RawMessage `json:"system,omitempty"`
	Tools     []Tool          `json:"tools,omitempty"`
	MaxTokens int             `json:"max_tokens"`
}

func NewRequest(instructions, input string, tools []Tool, maxTokens int) Request {
	system, _ := json.Marshal([]any{map[string]any{"type": "text", "text": instructions, "cache_control": map[string]string{"type": "ephemeral"}}})
	content, _ := json.Marshal(input)
	// Anthropic permits at most four cache breakpoints. The last tool caches
	// the entire tool prefix; the system is the second breakpoint.
	if len(tools) > 0 {
		tools[len(tools)-1].CacheControl = map[string]string{"type": "ephemeral"}
	}
	return Request{Messages: []Message{{Role: "user", Content: content}}, System: system, Tools: tools, MaxTokens: maxTokens}
}

func only(m map[string]json.RawMessage, names string) bool {
	for k := range m {
		if !strings.Contains(" "+names+" ", " "+k+" ") {
			return false
		}
	}
	return true
}
func str(b json.RawMessage) string { var s string; _ = json.Unmarshal(b, &s); return s }
func Validate(b []byte) error {
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil || !only(m, "messages system tools tool_choice max_tokens temperature top_p top_k stop_sequences thinking metadata") {
		return errPayload
	}
	if choice, ok := m["tool_choice"]; ok {
		var c map[string]json.RawMessage
		if json.Unmarshal(choice, &c) != nil || str(c["type"]) != "auto" || !only(c, "type") {
			return errPayload
		}
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(m["messages"], &messages) != nil || len(messages) == 0 {
		return errPayload
	}
	for _, msg := range messages {
		if !only(msg, "role content") || (str(msg["role"]) != "user" && str(msg["role"]) != "assistant") || ValidateContent(msg["content"]) != nil {
			return errPayload
		}
	}
	if s, ok := m["system"]; ok {
		if ValidateContent(s) != nil {
			return errPayload
		}
	}
	if raw, ok := m["tools"]; ok {
		var tools []map[string]json.RawMessage
		if json.Unmarshal(raw, &tools) != nil {
			return errPayload
		}
		for _, tool := range tools {
			if !only(tool, "name description input_schema cache_control") || str(tool["name"]) == "" || len(tool["input_schema"]) == 0 {
				return errPayload
			}
		}
	}
	return nil
}

// ValidateContent also checks nested tool results, before any HTTP send.
func ValidateContent(raw json.RawMessage) error {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return nil
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil || blocks == nil {
		return errPayload
	}
	for _, b := range blocks {
		var fields string
		switch str(b["type"]) {
		case "text":
			fields = "type text cache_control"
			if _, ok := b["text"]; !ok {
				return errPayload
			}
		case "thinking":
			fields = "type thinking signature"
		case "redacted_thinking":
			fields = "type data"
		case "tool_use":
			fields = "type id name input"
			var input map[string]json.RawMessage
			if str(b["id"]) == "" || str(b["name"]) == "" || json.Unmarshal(b["input"], &input) != nil || input == nil {
				return errPayload
			}
		case "tool_result":
			fields = "type tool_use_id content is_error cache_control"
			if str(b["tool_use_id"]) == "" || ValidateContent(b["content"]) != nil {
				return errPayload
			}
		case "image", "document":
			fields = "type source cache_control title context"
			var source map[string]json.RawMessage
			if json.Unmarshal(b["source"], &source) != nil || !only(source, "type media_type data") || str(source["type"]) != "base64" || str(source["media_type"]) == "" {
				return errPayload
			}
			// Match the core's image allowlist on every path: model responses,
			// outgoing history and MCP tool-result projection all use this check.
			if str(b["type"]) == "image" {
				switch str(source["media_type"]) {
				case "image/png", "image/jpeg", "image/gif", "image/webp":
				default:
					return errPayload
				}
			}
			if _, e := base64.StdEncoding.DecodeString(str(source["data"])); e != nil || str(source["data"]) == "" {
				return errPayload
			}
		default:
			return errPayload
		}
		if !only(b, fields) {
			return errPayload
		}
	}
	return nil
}

// ToolResult projects MCP content into the restricted Messages vocabulary.
// Unsupported blocks become a fixed text, without retaining their URLs/data.
func ToolResult(id string, raw json.RawMessage) json.RawMessage {
	var result struct {
		Content           []map[string]json.RawMessage `json:"content"`
		StructuredContent json.RawMessage              `json:"structuredContent"`
		IsError           bool                         `json:"isError"`
	}
	if json.Unmarshal(raw, &result) != nil {
		result.IsError = true
	}
	blocks := make([]any, 0, len(result.Content)+1)
	for _, b := range result.Content {
		var projected any = map[string]string{"type": "text", "text": Placeholder}
		switch str(b["type"]) {
		case "text":
			projected = map[string]string{"type": "text", "text": str(b["text"])}
		case "image":
			candidate := map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": str(b["mimeType"]), "data": str(b["data"])}}
			encoded, _ := json.Marshal([]any{candidate})
			if ValidateContent(encoded) == nil {
				projected = candidate
			}
		}
		blocks = append(blocks, projected)
	}
	if len(result.StructuredContent) > 0 {
		blocks = append(blocks, map[string]string{"type": "text", "text": string(result.StructuredContent)})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]string{"type": "text", "text": Placeholder})
	}
	b, _ := json.Marshal(map[string]any{"type": "tool_result", "tool_use_id": id, "content": blocks, "is_error": result.IsError})
	return b
}
