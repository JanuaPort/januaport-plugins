package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/januaport/januaport-plugins/agent-runner/internal/anthropic"
	"github.com/januaport/januaport-plugins/agent-runner/internal/openai"
)

type toolCall struct {
	ID, Name string
	Input    json.RawMessage
}

type turn struct {
	Text, Stop string
	Calls      []toolCall
}

type toolResult struct {
	ID  string
	Raw json.RawMessage
}

// The loop owns limits and tool authorization; conversations own wire history.
type conversation interface {
	next(context.Context, *Runner, *http.Client) (turn, string, string)
	appendResults([]toolResult)
}

func newConversation(a Arguments, tools []anthropic.Tool) conversation {
	if a.Model.Protocol == "openai" {
		projected := make([]openai.Tool, 0, len(tools))
		for _, t := range tools {
			projected = append(projected, openai.Tool{Type: "function", Function: openai.Definition{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}})
		}
		return &openAIConversation{request: openai.NewRequest(a.Instructions, a.Input, projected, min(1024, a.Limits.TokensOut))}
	}
	return &anthropicConversation{request: anthropic.NewRequest(a.Instructions, a.Input, tools, min(1024, a.Limits.TokensOut))}
}

type anthropicConversation struct{ request anthropic.Request }

func (c *anthropicConversation) next(ctx context.Context, r *Runner, client *http.Client) (turn, string, string) {
	response, end, detail := r.model(ctx, client, c.request)
	if end != "" {
		return turn{}, end, detail
	}
	// Keep the complete assistant content, including signed opaque thinking.
	c.request.Messages = append(c.request.Messages, anthropic.Message{Role: "assistant", Content: response.Content})
	var blocks []struct {
		Type, Text, ID, Name string
		Input                json.RawMessage
	}
	if json.Unmarshal(response.Content, &blocks) != nil {
		return turn{}, "error", "gateway"
	}
	out := turn{Stop: response.StopReason}
	var text strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
		if b.Type == "tool_use" {
			out.Calls = append(out.Calls, toolCall{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	out.Text = text.String()
	return out, "", ""
}

func (c *anthropicConversation) appendResults(results []toolResult) {
	blocks := make([]json.RawMessage, 0, len(results))
	for _, res := range results {
		blocks = append(blocks, anthropic.ToolResult(res.ID, res.Raw))
	}
	raw, _ := json.Marshal(blocks)
	c.request.Messages = append(c.request.Messages, anthropic.Message{Role: "user", Content: raw})
}

type openAIConversation struct{ request openai.Request }

func (c *openAIConversation) next(ctx context.Context, r *Runner, client *http.Client) (turn, string, string) {
	response, end, detail := r.openAIModel(ctx, client, c.request)
	if end != "" {
		return turn{}, end, detail
	}
	c.request.Messages = append(c.request.Messages, response.Message)
	out := turn{}
	_ = json.Unmarshal(response.Message.Content, &out.Text)
	// Shared stop reasons; the OpenAI request uses max_completion_tokens.
	switch response.FinishReason {
	case "stop":
		out.Stop = "end_turn"
	case "length":
		out.Stop = "max_tokens"
	case "content_filter":
		return turn{}, "limit", "content_filter"
	case "tool_calls":
		out.Stop = "tool_use"
	}
	for _, call := range response.Message.ToolCalls {
		out.Calls = append(out.Calls, toolCall{ID: call.ID, Name: call.Function.Name, Input: json.RawMessage(call.Function.Arguments)})
	}
	return out, "", ""
}

func (c *openAIConversation) appendResults(results []toolResult) {
	for _, res := range results {
		c.request.Messages = append(c.request.Messages, openai.ToolResult(res.ID, res.Raw))
	}
}
