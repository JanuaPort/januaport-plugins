package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Trust struct {
	URL         string `json:"url"`
	CA          []byte `json:"ca,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}
type Credential struct {
	Trust     Trust  `json:"trust"`
	DeviceID  string `json:"device_id"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	Pubkey    []byte `json:"pubkey"`
}
type PairReply struct {
	DeviceID     string `json:"device_id"`
	Token        string `json:"token"`
	ExpiresAt    string `json:"expires_at"`
	AnlagePubkey string `json:"anlage_pubkey"`
}
type StartReply struct {
	RunToken  string `json:"run_token"`
	ExpiresAt string `json:"expires_at"`
	MCPPath   string `json:"mcp_path"`
}
type HTTPError struct {
	Status int
	Class  string
}

func (e *HTTPError) Error() string { return "http_error" }

type API struct {
	base     string
	token    string
	http     *http.Client
	revoked  atomic.Bool
	onRevoke func() error
}

func NewAPI(trust Trust, token string, onRevoke func() error) (*API, error) {
	u, e := url.Parse(trust.URL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") || strings.ContainsAny(token, "\r\n") {
		return nil, ErrProtocol
	}
	host := u.Hostname()
	plain := u.Scheme == "http"
	if plain {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, ErrProtocol
		}
	}
	if len(trust.CA) > 0 && trust.Fingerprint != "" {
		return nil, ErrProtocol
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(trust.CA) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(trust.CA) {
			return nil, ErrProtocol
		}
		cfg.RootCAs = pool
	}
	if trust.Fingerprint != "" {
		expected, e := hex.DecodeString(trust.Fingerprint)
		if e != nil || len(expected) != 32 {
			return nil, ErrProtocol
		}
		// Only pin mode bypasses the CA chain; VerifyConnection is mandatory and
		// checks the exact DER digest BEFORE HTTP can transmit a code or credential.
		cfg.InsecureSkipVerify = true
		cfg.VerifyConnection = func(s tls.ConnectionState) error {
			if len(s.PeerCertificates) == 0 {
				return ErrProtocol
			}
			cert := s.PeerCertificates[0]
			actual := sha256.Sum256(cert.Raw)
			if subtle.ConstantTimeCompare(actual[:], expected) != 1 {
				return ErrProtocol
			}
			return nil
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: cfg, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, MaxResponseHeaderBytes: 32 << 10,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, e := dialer.DialContext(ctx, network, addr)
			if e != nil {
				return nil, e
			}
			if plain {
				remote, _, e := net.SplitHostPort(c.RemoteAddr().String())
				if e != nil || !net.ParseIP(remote).IsLoopback() {
					c.Close()
					return nil, ErrProtocol
				}
			}
			return c, nil
		}}
	return &API{base: strings.TrimSuffix(trust.URL, "/") + "/api/device/v1/", token: token, onRevoke: onRevoke, http: &http.Client{Transport: transport, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrProtocol }}}, nil
}
func (a *API) Close() { a.http.CloseIdleConnections() }
func (a *API) request(ctx context.Context, method, endpoint string, body any, seq uint64, runToken string, max int) ([]byte, int, error) {
	if a.revoked.Load() {
		return nil, 0, ErrRevoked
	}
	var reader io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil || len(raw) > 8<<20 {
			return nil, 0, ErrProtocol
		}
		reader = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, a.base+endpoint, reader)
	if e != nil {
		return nil, 0, ErrProtocol
	}
	req.Header.Set("X-Jnpt-Desktop-Contract", "1")
	if endpoint != "pair" {
		if a.token == "" {
			return nil, 0, ErrProtocol
		}
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if seq != 0 {
		req.Header.Set("X-Jnpt-Manifest-Seq", strconv.FormatUint(seq, 10))
	}
	if runToken != "" {
		if endpoint != "mcp" || strings.ContainsAny(runToken, "\r\n") {
			return nil, 0, ErrProtocol
		}
		req.Header.Set("X-Jnpt-Run-Token", runToken)
	}
	res, e := a.http.Do(req)
	if e != nil {
		return nil, 0, e
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		max = 80 << 10
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, int64(max)+1))
	if e != nil || len(b) > max {
		return nil, res.StatusCode, ErrProtocol
	}
	// Revocation is handled before all ordinary response-shape checks.
	if res.StatusCode == 401 && endpoint != "pair" {
		var v struct {
			Error struct {
				Class string `json:"class"`
			} `json:"error"`
		}
		if strict(b, &v) == nil && v.Error.Class == "revoked" {
			a.revoked.Store(true)
			if a.onRevoke != nil {
				if e = a.onRevoke(); e != nil {
					return nil, 401, e
				}
			}
			return nil, 401, ErrRevoked
		}
	}
	if res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Mcp-Session-Id") != "" {
		return nil, res.StatusCode, ErrProtocol
	}
	if res.StatusCode == 204 {
		if len(b) != 0 {
			return nil, 204, ErrProtocol
		}
		return b, 204, nil
	}
	if res.StatusCode >= 400 {
		var v struct {
			Error struct {
				Class   string `json:"class"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if strict(b, &v) != nil {
			return nil, res.StatusCode, ErrProtocol
		}
		return nil, res.StatusCode, &HTTPError{res.StatusCode, v.Error.Class}
	}
	if res.StatusCode != 200 {
		return nil, res.StatusCode, ErrProtocol
	}
	expectedType := "application/json"
	if strings.HasPrefix(endpoint, "packages/") {
		expectedType = "application/vnd.jnpt.gitobjects.v1"
	}
	if res.Header.Get("Content-Type") != expectedType {
		return nil, 200, ErrProtocol
	}
	return b, 200, nil
}
func (a *API) Pair(ctx context.Context, code, version string) (PairReply, error) {
	var out PairReply
	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`).MatchString(code) {
		return out, ErrProtocol
	}
	b, status, e := a.request(ctx, "POST", "pair", struct {
		Contract int    `json:"contract"`
		Code     string `json:"code"`
		Version  string `json:"runner_version"`
	}{1, code, version}, 0, "", 16<<10)
	if e != nil {
		return out, e
	}
	if status != 200 || strict(b, &out) != nil {
		return out, ErrProtocol
	}
	key, e := base64.StdEncoding.Strict().DecodeString(out.AnlagePubkey)
	if e != nil || len(key) != 32 || out.DeviceID == "" || !strings.HasPrefix(out.Token, "jnpt_") || strings.ContainsAny(out.Token, "\r\n") || !utc(out.ExpiresAt) {
		return PairReply{}, ErrProtocol
	}
	return out, nil
}
func (a *API) Config(ctx context.Context) (Config, error) {
	var out Config
	b, _, e := a.request(ctx, "GET", "config", nil, 0, "", 64<<10)
	if e != nil {
		return out, e
	}
	if strict(b, &out) != nil || out.Contract != 1 || out.Device.ID == "" || out.ExpectedAccount == "" || (out.Device.Kind != "dedicated" && out.Device.Kind != "workstation") || (out.StartPolicy.Mode != "always" && out.StartPolicy.Mode != "idle") || out.StartPolicy.IdleMinutes < 0 || out.StartPolicy.IdleMinutes > 525600 || out.HeartbeatS != 60 || out.PollHoldS != 25 {
		return out, ErrProtocol
	}
	return out, nil
}
func (a *API) Manifest(ctx context.Context) (SignedManifest, error) {
	var out SignedManifest
	b, _, e := a.request(ctx, "GET", "manifest", nil, 0, "", 4<<20)
	if e != nil {
		return out, e
	}
	if strict(b, &out) != nil {
		return out, ErrManifest
	}
	return out, nil
}
func (a *API) Heartbeat(ctx context.Context, h Heartbeat) (HeartbeatReply, error) {
	var out HeartbeatReply
	b, _, e := a.request(ctx, "POST", "heartbeat", h, 0, "", 64<<10)
	if e != nil {
		return out, e
	}
	if strict(b, &out) != nil || out.Cancel == nil {
		return out, ErrProtocol
	}
	return out, nil
}
func (a *API) Package(ctx context.Context, p Pin) ([]byte, error) {
	if !validID(p.Algo, p.Commit) || !safePath(p.Path) {
		return nil, ErrHash
	}
	b, _, e := a.request(ctx, "GET", "packages/"+p.Commit+"?path="+url.QueryEscape(p.Path), nil, 0, "", 128<<20)
	return b, e
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (a *API) Claim(ctx context.Context) (*Job, error) {
	b, s, e := a.request(ctx, "POST", "jobs/claim", nil, 0, "", 300<<10)
	if e != nil {
		return nil, e
	}
	if s == 204 {
		return nil, nil
	}
	var out struct {
		Job Job `json:"job"`
	}
	if strict(b, &out) != nil {
		return nil, ErrProtocol
	}
	j := out.Job
	if !uuidRE.MatchString(j.JobID) || !safeName(j.Pin) || !validID(j.Algo, j.Commit) || !safePath(j.Path) || j.ManifestSeq == 0 || !inputOK(j.Input) || !utc(j.StartBy) || j.Limits.WallS < 1 || j.Limits.WallS > 86400 || j.Limits.ToolCalls < 0 {
		return nil, ErrProtocol
	}
	return &j, nil
}
func (a *API) Start(ctx context.Context, r Result) (StartReply, error) {
	var out StartReply
	ep := "runs/start"
	var body any = struct {
		RunKey string `json:"run_key"`
	}{r.RunKey}
	seq := r.ManifestSeq
	if r.JobID != "" {
		if !uuidRE.MatchString(r.JobID) {
			return out, ErrProtocol
		}
		ep = "jobs/" + r.JobID + "/start"
		body = nil
		seq = 0
	}
	b, _, e := a.request(ctx, "POST", ep, body, seq, "", 16<<10)
	if e != nil {
		return out, e
	}
	if strict(b, &out) != nil || out.RunToken == "" || !utc(out.ExpiresAt) || out.MCPPath != "/api/device/v1/mcp" {
		return out, ErrProtocol
	}
	return out, nil
}
func (a *API) MCP(ctx context.Context, token string, id int64, name string, args json.RawMessage) (json.RawMessage, error) {
	body := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", id, "tools/call", struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}{name, args}}
	b, _, e := a.request(ctx, "POST", "mcp", body, 0, token, 8<<20)
	if e != nil {
		return nil, e
	}
	var reply struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int64           `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   json.RawMessage `json:"error,omitempty"`
	}
	if strict(b, &reply) != nil || reply.JSONRPC != "2.0" || reply.ID != id || len(reply.Result) == 0 || len(reply.Error) != 0 {
		return nil, ErrProtocol
	}
	return reply.Result, nil
}
func (a *API) Report(ctx context.Context, r Result) error {
	if r.Log != "" {
		return ErrProtocol
	}
	_, s, e := a.request(ctx, "POST", "runs/result", r, 0, "", 80<<10)
	if e != nil {
		return e
	}
	if s != 204 {
		return ErrProtocol
	}
	return nil
}
func IsOffline(err error) bool { var ne net.Error; return errors.As(err, &ne) }
