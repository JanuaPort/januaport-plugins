package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ModelPath       = "/api/plugin/agent-runner/model"
	MCPPath         = "/api/plugin/agent-runner/mcp"
	DefaultMaxWallS = 300
	maxBody         = 4 << 20
)

type Arguments struct {
	Contract int `json:"contract"`
	Gateway  struct {
		MCPPath   string `json:"mcp_path"`
		ModelPath string `json:"model_path"`
	} `json:"gateway"`
	Input        string `json:"input"`
	Instructions string `json:"instructions"`
	Limits       struct {
		Steps     int `json:"steps"`
		TokensIn  int `json:"tokens_in"`
		TokensOut int `json:"tokens_out"`
		ToolCalls int `json:"tool_calls"`
		WallS     int `json:"wall_s"`
	} `json:"limits"`
	Model struct {
		Binding string `json:"binding"`
		ModelID string `json:"model_id"`
	} `json:"model"`
	Run struct {
		ID       string `json:"id"`
		Profile  string `json:"profile"`
		Revision int    `json:"revision"`
	} `json:"run"`
}

var errRequest = errors.New("Agenten-Anfrage ist nicht zulässig")

func parse(raw []byte, h http.Header) (Arguments, string, error) {
	var a Arguments
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > maxBody || d.Decode(&a) != nil || d.Decode(new(any)) != io.EOF {
		return a, "", errRequest
	}
	token := h.Get("X-Jnpt-Run-Token")
	if !singleHeader(h, "X-Jnpt-Run-Token") || !singleHeader(h, "X-Jnpt-Run-Id") || h.Get("X-Jnpt-Run-Id") != a.Run.ID {
		return a, "", errRequest
	}
	if a.Contract != 1 || a.Gateway.ModelPath != ModelPath || a.Gateway.MCPPath != MCPPath {
		return a, "", errRequest
	}
	if a.Limits.Steps <= 0 || a.Limits.ToolCalls < 0 || a.Limits.WallS <= 0 || a.Limits.TokensIn <= 0 || a.Limits.TokensOut <= 0 || a.Model.Binding == "" || a.Model.ModelID == "" || a.Run.Profile == "" || a.Run.Revision <= 0 {
		return a, "", errRequest
	}
	return a, token, nil
}
func singleHeader(h http.Header, key string) bool {
	values := h.Values(key)
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 512 {
		return false
	}
	return strings.IndexFunc(values[0], func(r rune) bool { return r < 33 || r > 126 }) < 0
}

func fixedMessage(end, detail string) string {
	switch end {
	case "limit":
		return "Der Agenten-Lauf hat eine seiner Grenzen erreicht und ist beendet."
	case "cancelled":
		return "Der Agenten-Lauf wurde abgebrochen."
	case "revoked":
		return "Die Freigabe für den Agenten-Lauf wurde entzogen."
	}
	switch detail {
	case "provider":
		return "Der Modellanbieter hat den Aufruf mit einem Fehler beantwortet; sein Text wird nicht weitergegeben."
	case "rejected":
		return "Der Modell-Durchgang hat die Anfrage des Agenten-Läufers abgewiesen."
	default:
		return "Der Agenten-Lauf ist wegen eines Fehlers beendet."
	}
}
func result(end, detail, text string, steps int) *mcp.CallToolResult {
	meta := map[string]any{"end": end, "steps": steps}
	if detail != "" {
		meta["detail"] = detail
	}
	if end != "ok" {
		text = fixedMessage(end, detail)
	}
	return &mcp.CallToolResult{Meta: mcp.Meta{"januaport.ai/run": meta}, Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: end != "ok"}
}
