// Package runner implements the stateless MCP agent sidecar.
package runner

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/januaport/januaport-plugins/agent-runner/internal/runlog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Config struct {
	GatewayURL string
	KeyFile    string
	MaxWallS   int
	Log        io.Writer
}
type Runner struct {
	cfg       Config
	log       *runlog.Writer
	transport *http.Transport
}

func New(cfg Config) (*Runner, error) {
	u, e := url.Parse(cfg.GatewayURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("gateway-Adresse ist ungültig")
	}
	cfg.GatewayURL = strings.TrimSuffix(cfg.GatewayURL, "/")
	if cfg.MaxWallS == 0 {
		cfg.MaxWallS = DefaultMaxWallS
	}
	if cfg.MaxWallS < 1 || cfg.MaxWallS > DefaultMaxWallS {
		return nil, errors.New("wandzeit muss zwischen 1 und 300 Sekunden liegen")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &Runner{cfg: cfg, log: runlog.New(cfg.Log), transport: tr}, nil
}
func (r *Runner) key() string {
	f, e := os.Open(r.cfg.KeyFile)
	if e != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil || len(b) > 4096 {
		return ""
	}
	key := strings.TrimSpace(string(b))
	if key == "" || strings.ContainsAny(key, "\r\n \t") {
		return ""
	}
	return key
}
func (r *Runner) Handler() http.Handler {
	s := mcp.NewServer(&mcp.Implementation{Name: "agent-runner", Version: "0.2.1"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "tools/list":
				return &mcp.ListToolsResult{Meta: mcp.Meta{"januaport.ai/runner": map[string]any{"contract": 1, "max_wall_s": r.cfg.MaxWallS, "protocols": []string{"anthropic", "openai"}}}, Tools: []*mcp.Tool{{Name: "run_agent", Description: "Führt ein freigegebenes Agenten-Profil über den Kern aus.", InputSchema: map[string]any{"type": "object", "required": []string{"contract", "gateway", "input", "instructions", "limits", "model", "run"}}}}}, nil
			case "tools/call":
				call, ok := req.(*mcp.CallToolRequest)
				if !ok || call.Params.Name != "run_agent" || call.Extra == nil {
					return nil, errRequest
				}
				a, token, e := parse(call.Params.Arguments, call.Extra.Header)
				if e != nil {
					return nil, e
				}
				return r.run(ctx, a, token), nil
			}
			return next(ctx, method, req)
		}
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true, MaxRequestBodyBytes: maxBody})
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		if r.key() == "" {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, req *http.Request) {
		key := r.key()
		auth := req.Header.Values("Authorization")
		if key == "" || len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+key)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, maxBody)
		if req.Method == http.MethodPost {
			body, e := io.ReadAll(req.Body)
			if e != nil || !json.Valid(body) {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		handler.ServeHTTP(w, req)
	})
	return mux
}
