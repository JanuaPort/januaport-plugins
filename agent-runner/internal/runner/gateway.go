package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/januaport/januaport-plugins/agent-runner/internal/anthropic"
)

type bearer struct {
	token   string
	base    http.RoundTripper
	revoked *atomic.Bool
	scope   context.Context
}

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	stop := func() bool { return false }
	if b.scope != nil {
		stop = context.AfterFunc(b.scope, cancel)
		if b.scope.Err() != nil {
			cancel()
		}
	}
	cleanup := func() { stop(); cancel() }
	req = req.Clone(ctx)
	req.Header.Set("Authorization", "Bearer "+b.token)
	res, e := b.base.RoundTrip(req)
	if e != nil {
		cleanup()
		return nil, e
	}
	res.Body = &boundedBody{ReadCloser: &scopedBody{ReadCloser: res.Body, cleanup: cleanup}, remaining: maxBody}
	if b.revoked != nil && (res.StatusCode == 401 || res.StatusCode == 403) {
		b.revoked.Store(true)
	}
	return res, nil
}

type scopedBody struct {
	io.ReadCloser
	cleanup func()
}

func (b *scopedBody) Close() error { defer b.cleanup(); return b.ReadCloser.Close() }

type boundedBody struct {
	io.ReadCloser
	remaining int
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, errors.New("antwort ist zu groß")
	}
	if len(p) > b.remaining {
		p = p[:b.remaining]
	}
	n, e := b.ReadCloser.Read(p)
	b.remaining -= n
	return n, e
}
func (r *Runner) client(token string) *http.Client {
	return &http.Client{Transport: bearer{token: token, base: r.transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type modelResponse struct {
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
}

func (r *Runner) model(ctx context.Context, c *http.Client, request anthropic.Request) (modelResponse, string, string) {
	var response modelResponse
	b, e := json.Marshal(request)
	if e != nil || anthropic.Validate(b) != nil {
		return response, "error", "rejected"
	}
	req, e := http.NewRequestWithContext(ctx, "POST", r.cfg.GatewayURL+ModelPath, bytes.NewReader(b))
	if e != nil {
		return response, "error", "gateway"
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := c.Do(req)
	if e != nil {
		return response, "error", "gateway"
	}
	defer func() { _ = res.Body.Close() }()
	var envelope struct {
		Response modelResponse `json:"response"`
		Error    *struct {
			Class  string `json:"class"`
			Detail string `json:"detail"`
		} `json:"error"`
	}
	d := json.NewDecoder(res.Body)
	if d.Decode(&envelope) != nil || d.Decode(new(any)) != io.EOF {
		return response, "error", "gateway"
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return response, "revoked", ""
	}
	if envelope.Error != nil {
		switch envelope.Error.Class {
		case "limit":
			detail := "gateway"
			switch envelope.Error.Detail {
			case "steps", "tool_calls", "wall", "tokens_in", "tokens_out":
				detail = envelope.Error.Detail
			}
			return response, "limit", detail
		case "cancelled":
			return response, "cancelled", ""
		case "revoked":
			return response, "revoked", ""
		case "provider", "rejected":
			return response, "error", envelope.Error.Class
		}
		return response, "error", "gateway"
	}
	if res.StatusCode != 200 || anthropic.ValidateContent(envelope.Response.Content) != nil {
		return response, "error", "gateway"
	}
	return envelope.Response, "", ""
}
