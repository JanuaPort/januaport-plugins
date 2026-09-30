// Command script-runner ist der Beisteller „Skript-Läufer“ für JanuaPort
// (JanuaPort/januaport#928): ein Binary, drei Dienste.
//
//	script-runner mediator   vermittler: MCP-Server für das Gateway, Senke, Slot
//	script-runner guard      Wächter: PID 1 der Sandbox, genau ein Lauf je Instanz
//	script-runner fetcher    abholer: holt gepinnte Commits per ssh in den Store
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/fetcher"
	"github.com/januaport/januaport-plugins/script-runner/internal/guard"
	"github.com/januaport/januaport-plugins/script-runner/internal/mediator"
)

// Feste Orte im Container; siehe docker-compose.yml.
const (
	keyDir     = "/run/jnpt/plugins/script-runner"
	gitKeyPath = "/run/jnpt/plugins/script-runner-git/runtime-key"
	socketPath = "/run/script-runner/sandbox.sock"
	storeDir   = "/var/lib/script-runner"
	mcpAddr    = ":8090"
	healthAddr = ":8091"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Aufruf: script-runner mediator|guard|fetcher")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "mediator":
		os.Exit(runMediator())
	case "guard":
		os.Exit(guard.Run(guard.DefaultConfig()))
	case "fetcher":
		os.Exit(runFetcher())
	}
	fmt.Fprintln(os.Stderr, "unbekannter Unterbefehl")
	os.Exit(2)
}

func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func runMediator() int {
	log := logger()
	gateway := os.Getenv("JNPT_GATEWAY_URL")
	if gateway == "" {
		gateway = "http://jnpt:8484"
	}
	m, err := mediator.New(mediator.Config{
		SocketPath: socketPath, KeyDir: keyDir, StoreDir: storeDir, GatewayURL: gateway, Logger: log,
	})
	if err != nil {
		log.Error("Start gescheitert")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	mcpSrv := &http.Server{Addr: mcpAddr, Handler: m.Handler(), ReadHeaderTimeout: 10 * time.Second}
	healthSrv := &http.Server{Addr: healthAddr, Handler: m.HealthHandler(), ReadHeaderTimeout: 5 * time.Second}
	errs := make(chan error, 3)
	go func() { errs <- m.Serve(ctx) }()
	go func() { errs <- mcpSrv.ListenAndServe() }()
	go func() { errs <- healthSrv.ListenAndServe() }()
	log.Info("vermittler bereit")
	select {
	case <-ctx.Done():
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("Dienst beendet")
			return 1
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = mcpSrv.Shutdown(shutdown)
	_ = healthSrv.Shutdown(shutdown)
	return 0
}

func runFetcher() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	f := fetcher.New(fetcher.Config{StoreDir: storeDir, KeyPath: gitKeyPath, TmpDir: os.TempDir(), Logger: logger()})
	if err := f.Run(ctx); err != nil {
		return 1
	}
	return 0
}
