package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/agent-runner/internal/anthropic"
)

func TestModelGoldenResponseAtAdapter(t *testing.T) {
	var g struct {
		Status int
		Body   json.RawMessage
	}
	_ = json.Unmarshal(golden(t, "model_response_ok.json"), &g)
	f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) { w.WriteHeader(g.Status); _, _ = w.Write(g.Body) }}
	r, _, _ := setup(t, f)
	_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
	end(t, b, "ok", 1)
	if !strings.Contains(string(b), "Drei Rechnungen geprüft.") {
		t.Fatal(string(b))
	}
}

func TestRevocationAtBothGateways(t *testing.T) {
	for _, path := range []string{ModelPath, MCPPath} {
		for _, status := range []int{401, 403} {
			t.Run(path+http.StatusText(status), func(t *testing.T) {
				f := &fakeCore{before: func(w http.ResponseWriter, r *http.Request) bool {
					if r.URL.Path == path {
						w.WriteHeader(status)
						_, _ = io.WriteString(w, secret)
						return true
					}
					return false
				}}
				r, _, log := setup(t, f)
				_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
				steps := 0
				if path == ModelPath {
					steps = 1
				}
				end(t, b, "revoked", steps)
				if strings.Contains(string(b)+log.String(), secret) {
					t.Fatal("error text escaped")
				}
			})
		}
	}
}

func TestNoRedirectsOrProxyAtEitherGateway(t *testing.T) {
	var hits atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) }))
	defer destination.Close()
	t.Setenv("HTTP_PROXY", destination.URL)
	t.Setenv("HTTPS_PROXY", destination.URL)
	for _, path := range []string{ModelPath, MCPPath} {
		t.Run(path, func(t *testing.T) {
			f := &fakeCore{before: func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == path {
					http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
					return true
				}
				return false
			}}
			r, _, _ := setup(t, f)
			if r.transport.Proxy != nil {
				t.Fatal("environment proxy enabled")
			}
			_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
			steps := 0
			if path == ModelPath {
				steps = 1
			}
			end(t, b, "error", steps)
		})
	}
	if hits.Load() != 0 {
		t.Fatal("redirect destination received token")
	}
}

func TestURLImageToolResultAtAdapter(t *testing.T) {
	f := &fakeCore{tool: json.RawMessage(`{"content":[{"type":"image","source":{"type":"url","url":"https://example.invalid/URL-CANARY"}}]}`), model: func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			modelReply(w, `[{"type":"tool_use","id":"x","name":"erp_list_things","input":{"q":"`+secret+`"}}]`, "tool_use")
		} else {
			modelReply(w, `[{"type":"text","text":"ok"}]`, "end_turn")
		}
	}}
	r, _, _ := setup(t, f)
	_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
	end(t, b, "ok", 2)
	encoded, _ := json.Marshal(f.requests[1])
	if strings.Contains(string(encoded), "URL-CANARY") || !strings.Contains(string(encoded), anthropic.Placeholder) {
		t.Fatal(string(encoded))
	}
	if e := anthropic.Validate(encoded); e != nil {
		t.Fatal(e)
	}
}

func TestMalformedArgumentsAtAdapter(t *testing.T) {
	f := &fakeCore{}
	r, _, _ := setup(t, f)
	for _, change := range []func(string) string{
		func(s string) string { return strings.Replace(s, `"contract":1`, `"contract":1,"stream":true`, 1) },
		func(s string) string { return strings.Replace(s, `"steps":20`, `"steps":-1`, 1) },
		func(s string) string { return strings.Replace(s, `"tool_calls":50`, `"tool_calls":-1`, 1) },
		func(s string) string { return strings.Replace(s, `"tokens_in":200000`, `"tokens_in":0`, 1) },
		func(s string) string { return strings.Replace(s, `"tokens_out":20000`, `"tokens_out":0`, 1) },
		func(s string) string { return strings.Replace(s, `"revision":1`, `"revision":0`, 1) },
		func(s string) string { return s + `{}` },
	} {
		w, _ := invoke(t, r, context.Background(), requestFixture(t), func(req *http.Request) {
			b, _ := io.ReadAll(req.Body)
			req.Body = io.NopCloser(strings.NewReader(change(string(b))))
		})
		if w.Code < 400 && !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatal(w.Body.String())
		}
	}
	for _, key := range []string{"X-Jnpt-Run-Token", "X-Jnpt-Run-Id"} {
		w, _ := invoke(t, r, context.Background(), requestFixture(t), func(req *http.Request) { req.Header.Add(key, req.Header.Get(key)) })
		if !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatal("duplicate accepted")
		}
	}
}

func TestSizeBoundsAndMalformedModelAtAdapter(t *testing.T) {
	for _, body := range []string{`{`, `{"response":{"content":[]}} {}`, strings.Repeat(" ", maxBody+1), `{"response":{"content":[{"type":"text","text":"` + strings.Repeat("x", maxBody) + `"}],"stop_reason":"end_turn"}}`, `{"response":{"content":[{"type":"text","text":"ok"}],"stop_reason":"unknown"}}`} {
		f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) { _, _ = io.WriteString(w, body) }}
		r, _, log := setup(t, f)
		_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
		end(t, b, "error", 1)
		if strings.Contains(log.String(), secret) {
			t.Fatal(log.String())
		}
	}
	r, _, _ := setup(t, &fakeCore{})
	q := requestFixture(t)
	q.Arguments.Input = strings.Repeat("x", maxBody+1)
	w, _ := invoke(t, r, context.Background(), q, nil)
	if w.Code < 400 && !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatal("oversize accepted")
	}
}

func TestConfigAndKeyFailClosed(t *testing.T) {
	for _, url := range []string{"file:///tmp/key", "http://user:pass@host", "http://host/path", "http://host?x=1", "http://host#x", "//host"} {
		if _, e := New(Config{GatewayURL: url}); e == nil {
			t.Fatal(url)
		}
	}
	for _, wall := range []int{-1, 301} {
		if _, e := New(Config{GatewayURL: "http://jnpt:8484", MaxWallS: wall}); e == nil {
			t.Fatal(wall)
		}
	}
	r, key, _ := setup(t, &fakeCore{})
	for _, bad := range []string{"", strings.Repeat("x", 4097), "bad\nkey"} {
		if e := os.WriteFile(key, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		w, _ := invoke(t, r, context.Background(), requestFixture(t), nil)
		if w.Code != 401 {
			t.Fatal("bad key accepted")
		}
		health := httptest.NewRecorder()
		r.Handler().ServeHTTP(health, httptest.NewRequest("GET", "/healthz", nil))
		if health.Code != 503 {
			t.Fatal("invalid key marked ready")
		}
	}
}

func TestAdvertisedWallCapEnforcedAtAdapter(t *testing.T) {
	f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) { <-r.Context().Done() }}
	r, _, _ := setup(t, f)
	r.cfg.MaxWallS = 1
	start := time.Now()
	_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
	end(t, b, "limit", 1)
	if time.Since(start) > 3*time.Second {
		t.Fatal("own wall cap ignored")
	}
}

func TestToolListAndMultiToolGuards(t *testing.T) {
	for _, list := range []string{`{"tools":[{"name":"bad name","inputSchema":{}}]}`, `{"tools":[{"name":"erp_list_things","inputSchema":{}},{"name":"erp_list_things","inputSchema":{}}]}`, `{"tools":[],"nextCursor":"same"}`} {
		f := &fakeCore{list: json.RawMessage(list)}
		r, _, _ := setup(t, f)
		_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
		end(t, b, "error", 0)
	}
	for _, duplicate := range []bool{false, true} {
		f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
			second := "y"
			if duplicate {
				second = "x"
			}
			modelReply(w, `[{"type":"tool_use","id":"x","name":"erp_list_things","input":{"q":"`+secret+`"}},{"type":"tool_use","id":"`+second+`","name":"erp_list_things","input":{"q":"`+secret+`"}}]`, "tool_use")
		}}
		r, _, _ := setup(t, f)
		q := requestFixture(t)
		q.Arguments.Limits.ToolCalls = 1
		_, b := invoke(t, r, context.Background(), q, nil)
		want := "limit"
		if duplicate {
			want = "error"
		}
		end(t, b, want, 1)
		if f.calls != 1 {
			t.Fatal(f.calls)
		}
	}
}

func TestModelWireGuards(t *testing.T) {
	// Exercise the sender itself with a poisoned history/tool, not a mock validator.
	for _, request := range []anthropic.Request{
		{Messages: []anthropic.Message{{Role: "user", Content: json.RawMessage(`[{"type":"server_tool_use"}]`)}}, MaxTokens: 1024},
		{Messages: []anthropic.Message{{Role: "user", Content: json.RawMessage(`[{"type":"image","source":{"type":"url","url":"https://example.invalid"}}]`)}}, MaxTokens: 1024},
	} {
		r, _, _ := setup(t, &fakeCore{})
		_, end, detail := r.model(context.Background(), r.client("<lauf-token>"), request)
		if end != "error" || detail != "rejected" {
			t.Fatal(end, detail)
		}
	}
}

func TestLogErrorContentAbsent(t *testing.T) {
	f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
		w.WriteHeader(500)
		_, _ = io.WriteString(w, secret)
	}}
	r, _, logs := setup(t, f)
	q := requestFixture(t)
	q.Arguments.Input = secret
	q.Arguments.Instructions = secret
	_, b := invoke(t, r, context.Background(), q, nil)
	end(t, b, "error", 1)
	if bytes.Contains(logs.Bytes(), []byte(secret)) {
		t.Fatal(logs.String())
	}
}

func TestAllResultGoldens(t *testing.T) {
	for _, tc := range []struct{ file, end, detail string }{{"limit", "limit", "steps"}, {"provider", "error", "provider"}, {"rejected", "error", "rejected"}, {"error", "error", "gateway"}, {"cancelled", "cancelled", ""}, {"revoked", "revoked", ""}} {
		b, e := os.ReadFile("../../contract/proposed/run_agent_response_" + tc.file + ".json")
		if e != nil {
			t.Fatal(e)
		}
		got, _ := json.Marshal(result(tc.end, tc.detail, secret, 1))
		equalJSON(t, got, b)
	}
	got, _ := json.Marshal(result("ok", "", "Drei Rechnungen geprüft; eine weicht von der Bestellung ab.", 4))
	equalJSON(t, got, golden(t, "run_agent_response_ok.json"))
}

func TestToolListSizeLimit(t *testing.T) {
	var tools []map[string]any
	for i := 0; i < 1025; i++ {
		tools = append(tools, map[string]any{"name": fmt.Sprintf("tool_%d", i), "inputSchema": map[string]any{"type": "object"}})
	}
	b, _ := json.Marshal(map[string]any{"tools": tools})
	f := &fakeCore{list: b}
	r, _, _ := setup(t, f)
	_, out := invoke(t, r, context.Background(), requestFixture(t), nil)
	end(t, out, "error", 0)
}

func TestCleanupWithinWallAtAdapter(t *testing.T) {
	f := &fakeCore{before: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == MCPPath {
			w.Header().Set("Mcp-Session-Id", "probe-session")
		}
		if r.Method == http.MethodDelete {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			w.WriteHeader(204)
			return true
		}
		return false
	}, model: func(w http.ResponseWriter, r *http.Request, n int) {
		modelReply(w, `[{"type":"text","text":"ok"}]`, "end_turn")
	}}
	r, _, _ := setup(t, f)
	q := requestFixture(t)
	q.Arguments.Limits.WallS = 1
	start := time.Now()
	_, b := invoke(t, r, context.Background(), q, nil)
	end(t, b, "ok", 1)
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatal("SDK cleanup outlived run deadline")
	}
}

// H1: A successful run must cancel its transport scope before SDK Close.
// Reply immediately to DELETE: observing it is an ordering failure, independent
// of wall time or the speed of the test machine.
func TestCancelBeforeSessionCloseAtAdapter(t *testing.T) {
	var deletes atomic.Int32
	f := &fakeCore{before: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == MCPPath {
			w.Header().Set("Mcp-Session-Id", "ordering-session")
		}
		if r.Method == http.MethodDelete {
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		return false
	}, model: func(w http.ResponseWriter, r *http.Request, n int) {
		modelReply(w, `[{"type":"text","text":"ok"}]`, "end_turn")
	}}
	r, _, _ := setup(t, f)
	_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
	end(t, b, "ok", 1)
	if deletes.Load() != 0 {
		t.Fatal("session.Close sent DELETE before run cancellation")
	}
}

// H2: Exercise the real bearer HTTP adapter on both outgoing paths. No run
// deadline supplies the rejection: the reader must return a size error itself.
func TestResponseBody4MiBLimitAtAdapter(t *testing.T) {
	const ceiling = 4 * 1024 * 1024
	for _, path := range []string{ModelPath, MCPPath} {
		for _, size := range []int{ceiling - 1, ceiling + 1} {
			t.Run(fmt.Sprintf("%s/%d", path, size), func(t *testing.T) {
				f := &fakeCore{before: func(w http.ResponseWriter, r *http.Request) bool {
					_, _ = io.WriteString(w, strings.Repeat("x", size))
					return true
				}}
				r, _, _ := setup(t, f)
				res, err := r.client("<lauf-token>").Get(r.cfg.GatewayURL + path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = res.Body.Close() }()
				body, err := io.ReadAll(res.Body)
				if size < ceiling {
					if err != nil || len(body) != size {
						t.Fatalf("allowed body: bytes=%d err=%v", len(body), err)
					}
				} else if err == nil || err.Error() != "antwort ist zu groß" || len(body) != ceiling {
					t.Fatalf("4 MiB size guard missing: bytes=%d err=%v", len(body), err)
				}
			})
		}
	}
}

func TestRejectedMessageAtAdapter(t *testing.T) {
	const message = "Der Modell-Durchgang hat die Anfrage des Agenten-Läufers abgewiesen."
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"class":"rejected","message":"`+secret+`"}}`)
			}}
			steps := 1
			if local {
				f.list = json.RawMessage(`{"tools":[{"name":"bad name","inputSchema":{}}]}`)
				steps = 0
			}
			r, _, log := setup(t, f)
			_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
			end(t, b, "error", steps)
			var result struct{ Content []struct{ Text string } }
			if err := json.Unmarshal(b, &result); err != nil || len(result.Content) != 1 || result.Content[0].Text != message {
				t.Fatalf("rejected message: %s (%v)", b, err)
			}
			if strings.Contains(string(b)+log.String(), secret) {
				t.Fatal("rejection leaked gateway text")
			}
		})
	}
}

func TestImageMediaTypesAtAdapter(t *testing.T) {
	for _, media := range []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml", "application/pdf", "image/bmp", "image/avif", "IMAGE/PNG", ""} {
		allowed := media == "image/png" || media == "image/jpeg" || media == "image/gif" || media == "image/webp"
		t.Run(media, func(t *testing.T) {
			block, _ := json.Marshal([]any{map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": media, "data": "YQ=="}}})
			// Sender guard: invalid images must never reach the gateway.
			var hits atomic.Int32
			f := &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) {
				hits.Add(1)
				modelReply(w, `[{"type":"text","text":"ok"}]`, "end_turn")
			}}
			r, _, _ := setup(t, f)
			req := anthropic.NewRequest("s", "i", nil, 1024)
			req.Messages[0].Content = block
			_, class, detail := r.model(context.Background(), r.client("<lauf-token>"), req)
			if allowed {
				if class != "" || hits.Load() != 1 {
					t.Fatalf("allowed image: %s/%s hits=%d", class, detail, hits.Load())
				}
			} else if class != "error" || detail != "rejected" || hits.Load() != 0 {
				t.Fatalf("forbidden image sent: %s/%s hits=%d", class, detail, hits.Load())
			}
			// MCP result projection through the entire loop into the next request.
			tool, _ := json.Marshal(map[string]any{"content": []any{map[string]string{"type": "image", "mimeType": media, "data": "YQ=="}}})
			f = &fakeCore{tool: tool, model: func(w http.ResponseWriter, r *http.Request, n int) {
				if n == 1 {
					modelReply(w, `[{"type":"tool_use","id":"x","name":"erp_list_things","input":{"q":"`+secret+`"}}]`, "tool_use")
				} else {
					modelReply(w, `[{"type":"text","text":"ok"}]`, "end_turn")
				}
			}}
			r, _, _ = setup(t, f)
			_, b := invoke(t, r, context.Background(), requestFixture(t), nil)
			end(t, b, "ok", 2)
			encoded, _ := json.Marshal(f.requests[1])
			if strings.Contains(string(encoded), anthropic.Placeholder) == allowed || strings.Contains(string(encoded), "YQ==") != allowed {
				t.Fatalf("image projection: %s", encoded)
			}
			// The same content validator also guards model responses/history.
			f = &fakeCore{model: func(w http.ResponseWriter, r *http.Request, n int) { modelReply(w, string(block), "end_turn") }}
			r, _, _ = setup(t, f)
			_, b = invoke(t, r, context.Background(), requestFixture(t), nil)
			want := "error"
			if allowed {
				want = "ok"
			}
			end(t, b, want, 1)
		})
	}
}
