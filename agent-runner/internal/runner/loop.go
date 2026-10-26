package runner

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/januaport/januaport-plugins/agent-runner/internal/anthropic"
	"github.com/januaport/januaport-plugins/agent-runner/internal/runlog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var toolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func (r *Runner) run(parent context.Context, a Arguments, token string) (out *mcp.CallToolResult) {
	start := time.Now()
	record := runlog.Record{Tools: []string{}}
	ctx, cancel := context.WithTimeout(parent, time.Duration(min(a.Limits.WallS, r.cfg.MaxWallS))*time.Second)
	defer cancel()
	var revoked atomic.Bool
	finish := func(end, detail, text string) *mcp.CallToolResult {
		if revoked.Load() {
			end, detail, text = "revoked", "", ""
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			end, detail, text = "limit", "wall", ""
		} else if ctx.Err() != nil {
			end, detail, text = "cancelled", "", ""
		}
		record.End = end
		return result(end, detail, text, record.Steps)
	}
	defer func() { record.DurationMS = time.Since(start).Milliseconds(); r.log.Write(record) }()
	client := r.client(token)
	client.Transport = bearer{token: token, base: r.transport, revoked: &revoked, scope: ctx}
	mc := mcp.NewClient(&mcp.Implementation{Name: "agent-runner", Version: "0.2.0"}, nil)
	session, e := mc.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: r.cfg.GatewayURL + MCPPath, HTTPClient: client, DisableStandaloneSSE: true}, nil)
	if e != nil {
		return finish("error", "gateway", "")
	}
	// The SDK detaches connection cleanup from Connect's context. Cancel the
	// transport scope before Close so a session DELETE cannot extend the run.
	defer func() { cancel(); _ = session.Close() }()
	tools := []anthropic.Tool{}
	allowed := map[string]bool{}
	cursor := ""
	seen := map[string]bool{}
	for {
		listed, e := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if e != nil {
			return finish("error", "gateway", "")
		}
		for _, t := range listed.Tools {
			if !toolName.MatchString(t.Name) || allowed[t.Name] || len(tools) >= 1024 {
				return finish("error", "rejected", "")
			}
			schema, e := json.Marshal(t.InputSchema)
			if e != nil {
				return finish("error", "rejected", "")
			}
			tools = append(tools, anthropic.Tool{Name: t.Name, Description: t.Description, InputSchema: schema})
			allowed[t.Name] = true
		}
		cursor = listed.NextCursor
		if cursor == "" {
			break
		}
		if seen[cursor] || len(seen) >= 1024 {
			return finish("error", "gateway", "")
		}
		seen[cursor] = true
	}
	history := newConversation(a, tools)
	for record.Steps < a.Limits.Steps {
		if ctx.Err() != nil {
			return finish("cancelled", "", "")
		}
		record.Steps++
		response, end, detail := history.next(ctx, r, client)
		if end != "" {
			return finish(end, detail, "")
		}
		if response.Stop == "end_turn" && len(response.Calls) == 0 {
			return finish("ok", "", response.Text)
		}
		if response.Stop == "max_tokens" {
			return finish("limit", "tokens_out", "")
		}
		if response.Stop != "tool_use" || len(response.Calls) == 0 {
			return finish("error", "gateway", "")
		}
		results := []toolResult{}
		ids := map[string]bool{}
		for _, b := range response.Calls {
			if !allowed[b.Name] || ids[b.ID] {
				return finish("error", "rejected", "")
			}
			ids[b.ID] = true
			if record.ToolCalls >= a.Limits.ToolCalls {
				return finish("limit", "tool_calls", "")
			}
			if ctx.Err() != nil {
				return finish("cancelled", "", "")
			}
			record.ToolCalls++
			record.Tools = append(record.Tools, b.Name)
			var input map[string]json.RawMessage
			var raw json.RawMessage
			if json.Unmarshal(b.Input, &input) != nil || input == nil {
				// OpenAI arguments are a JSON string. Give the model a fixed
				// MCP-style error result instead of sending invalid tool input.
				raw = json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"Werkzeug-Eingabe ist kein gültiges JSON-Objekt."}]}`)
			} else {
				res, e := session.CallTool(ctx, &mcp.CallToolParams{Name: b.Name, Arguments: b.Input})
				if e != nil {
					return finish("error", "gateway", "")
				}
				raw, e = json.Marshal(res)
				if e != nil {
					return finish("error", "gateway", "")
				}
			}
			results = append(results, toolResult{ID: b.ID, Raw: raw})
		}
		history.appendResults(results)
	}
	return finish("limit", "steps", "")
}
