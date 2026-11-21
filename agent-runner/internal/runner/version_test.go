package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// JanuaPort/januaport#1436: Die Version steht an einer Stelle. serverInfo des
// Läufers und clientInfo gegenüber dem Kern lesen dieselbe Konstante.
func TestVersionIsSemver(t *testing.T) {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(Version) {
		t.Fatalf("Version %q ist kein X.Y.Z", Version)
	}
}

func TestServerInfoReportsVersion(t *testing.T) {
	r, _, _ := setup(t, &fakeCore{})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	req := httptest.NewRequest("POST", "http://runner/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-runtime-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/list")
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, req)
	var out struct {
		Result struct {
			Meta map[string]json.RawMessage `json:"_meta"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var info struct{ Name, Version string }
	if err := json.Unmarshal(out.Result.Meta["io.modelcontextprotocol/serverInfo"], &info); err != nil {
		t.Fatalf("serverInfo fehlt: %s", rec.Body.String())
	}
	if info.Name != "agent-runner" || info.Version != Version {
		t.Fatalf("serverInfo %+v, erwartet Version %s", info, Version)
	}
}

func TestClientInfoReportsVersion(t *testing.T) {
	var mu sync.Mutex
	seen := ""
	f := &fakeCore{
		model: func(w http.ResponseWriter, r *http.Request, n int) {
			modelReply(w, `[{"type":"text","text":"ok"}]`, "end_turn")
		},
		before: func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != MCPPath {
				return false
			}
			b, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(b))
			var rpc struct {
				Method string
				Params struct {
					ClientInfo struct{ Name, Version string }
				}
			}
			if json.Unmarshal(b, &rpc) == nil && rpc.Method == "initialize" {
				mu.Lock()
				seen = rpc.Params.ClientInfo.Version
				mu.Unlock()
			}
			return false
		},
	}
	r, _, _ := setup(t, f)
	_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
	end(t, b, "ok", 1)
	mu.Lock()
	defer mu.Unlock()
	if seen != Version {
		t.Fatalf("clientInfo.version = %q, erwartet %q", seen, Version)
	}
}

// Keine zweite Quelle: Ein Literal im Quelltext würde wieder auseinanderlaufen.
func TestNoVersionLiteralOutsideConstant(t *testing.T) {
	literal := regexp.MustCompile(`Version:\s*"`)
	for _, name := range []string{"server.go", "loop.go", "gateway.go", "conversation.go", "contract.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if literal.Match(b) {
			t.Errorf("%s enthält ein Versionsliteral", name)
		}
	}
}
