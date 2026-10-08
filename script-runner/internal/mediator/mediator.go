// Package mediator ist der vermittler: MCP-Server für das Gateway (Weg A),
// Halter der Pins, einzige Senke für Code (R6) und Gegenstelle des Wächters
// (Weg C). Er protokolliert nur Zählwerte (M6).
package mediator

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/store"
)

// Config des vermittlers. Leere Dauern bekommen die Standards aus dem
// Vertrag.
type Config struct {
	SocketPath   string // Unix-Socket für den Wächter (Weg C)
	KeyDir       string // Plugin-Ordner mit runtime-key und pins.json
	StoreDir     string // gemeinsames Volume mit dem abholer (Weg D)
	GatewayURL   string // Basis für Weg B, ohne /mcp
	Logger       *slog.Logger
	BusyWait     time.Duration // Vertrag §5: 3 s
	PollInterval time.Duration // Abstand für pins.json, runtime-key, Store
}

// Mediator ist der vermittler.
type Mediator struct {
	cfg   Config
	store *store.Store
	log   *slog.Logger
	sb    *sandboxes

	mu        sync.Mutex
	key       string
	pins      []pinState
	repoURL   string
	isolation string
	cache     map[cacheKey]verdict
}

// New prüft die Konfiguration.
func New(cfg Config) (*Mediator, error) {
	if cfg.SocketPath == "" || cfg.KeyDir == "" || cfg.StoreDir == "" || cfg.GatewayURL == "" {
		return nil, errors.New("mediator: Konfiguration unvollständig")
	}
	if cfg.BusyWait == 0 {
		cfg.BusyWait = 3 * time.Second
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	m := &Mediator{
		cfg:       cfg,
		store:     store.Open(filepath.Join(cfg.StoreDir, "store.git")),
		log:       cfg.Logger,
		isolation: contract.IsolationInvalid,
		cache:     map[cacheKey]verdict{},
	}
	m.sb = newSandboxes(m.onHello, m.log)
	// Erster Abgleich vor dem ersten Request: kein Fenster, in dem ein
	// vorhandener Schlüssel noch nicht gilt.
	m.refresh(context.Background())
	return m, nil
}

// Serve betreibt den Socket für den Wächter und den Abgleich der Pins, bis
// ctx endet.
func (m *Mediator) Serve(ctx context.Context) error {
	_ = os.Remove(m.cfg.SocketPath)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: m.cfg.SocketPath, Net: "unix"})
	if err != nil {
		return err
	}
	// Der Wächter läuft mit eigener UID und braucht Schreibrecht am Socket;
	// den Ordner sieht er nur read-only (M4).
	if err := os.Chmod(m.cfg.SocketPath, 0o666); err != nil {
		_ = ln.Close()
		return err
	}
	go func() { <-ctx.Done(); _ = ln.Close() }()
	go m.pollLoop(ctx)
	return m.sb.accept(ln)
}

func (m *Mediator) pollLoop(ctx context.Context) {
	t := time.NewTicker(m.cfg.PollInterval)
	defer t.Stop()
	for {
		m.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// onHello übernimmt die Selbstauskunft einer frischen Instanz. Nutzbar ist
// sie nur mit Seccomp-Modus 2 UND unserem Profil (S2, Fassung 3 B2,
// fail-closed).
func (m *Mediator) onHello(isolation string, seccomp int, profile string) bool {
	ok := seccomp == 2 && profile == contract.ProfileJnpt && (isolation == contract.IsolationGVisor || isolation == contract.IsolationStandard)
	m.mu.Lock()
	if ok {
		m.isolation = isolation
	} else {
		m.isolation = contract.IsolationInvalid
	}
	m.mu.Unlock()
	return ok
}

// Handler ist Weg A: POST /mcp mit Bearer runtime-key (R13).
func (m *Mediator) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "januaport-script-runner", Version: "1"},
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	server.AddReceivingMiddleware(m.middleware)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux := http.NewServeMux()
	mux.Handle("/mcp", m.auth(h))
	return mux
}

// HealthHandler ist der eigene Listener für /healthz. 200 heißt: Schlüssel
// da und eine Sandbox mit Seccomp 2 hat sich gemeldet.
func (m *Mediator) HealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		ok := m.key != "" && m.isolation != contract.IsolationInvalid
		m.mu.Unlock()
		if !ok {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

func (m *Mediator) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		key := m.key
		m.mu.Unlock()
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" || !ok || subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Mediator) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/list":
			return m.toolsList(), nil
		case "tools/call":
			call, ok := req.(*mcp.CallToolRequest)
			if !ok {
				return nil, errors.New("tools/call ohne Parameter")
			}
			return m.callTool(ctx, call)
		}
		return next(ctx, method, req)
	}
}

func (m *Mediator) toolsList() *mcp.ListToolsResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	listed := make([]contract.ListedPin, 0, len(m.pins))
	for _, p := range m.pins {
		listed = append(listed, p.listed())
	}
	return contract.ToolsList(listed, m.isolation)
}
