// Command script-runner ist der Beisteller „Skript-Läufer“ für JanuaPort
// Ein Binary für vier dauerhafte Dienste (apphost optional) und Volume-Init.
//
//	script-runner mediator   vermittler: MCP-Server für das Gateway, Senke, Slot
//	script-runner guard      Wächter: PID 1 der Sandbox, genau ein Lauf je Instanz
//	script-runner fetcher    abholer: prüft Pins gegen den Standard-Zweig und holt Objekte per ssh in den Store
//
//	script-runner init-libs  bereitet das Bibliotheks-Volume vor dem Sandbox-Start vor
//	script-runner apphost    geprüfte Browser-Apps und privates tls-ask-Relay
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/apphost"
	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
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
	libsDir    = "/libs/ready"
	mcpAddr    = ":8090"
	healthAddr = ":8091"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "apphost" {
		if len(os.Args) == 3 && os.Args[2] == "--help" {
			fmt.Println("apphost: Browser :8080, tls-ask-Relay :8081; Schlüssel /run/secrets/jnpt/runtime-key; JNPT_APPS_DOMAIN, JNPT_APPS_GATEWAY_URL (privater Listener), JNPT_GATEWAY_PUBLIC_URL (HTTPS-Anmeldung)")
			return
		}
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "Aufruf: script-runner apphost [--help]")
			os.Exit(2)
		}
		os.Exit(runApphost())
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Aufruf: script-runner mediator|guard|fetcher|init-libs|apphost")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "mediator":
		os.Exit(runMediator())
	case "guard":
		os.Exit(guard.Run(guard.DefaultConfig()))
	case "fetcher":
		os.Exit(runFetcher())
	case "init-libs":
		if err := initLibs("/libs"); err != nil {
			fmt.Fprintln(os.Stderr, "Bibliotheksordner nicht vorbereitet")
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "unbekannter Unterbefehl")
	os.Exit(2)
}

func runApphost() int {
	// Check the materialized key before opening either listener.
	h, err := apphost.New(apphost.Config{Domain: os.Getenv("JNPT_APPS_DOMAIN"), GatewayURL: os.Getenv("JNPT_APPS_GATEWAY_URL"), LoginURL: os.Getenv("JNPT_GATEWAY_PUBLIC_URL"), Logger: logger()})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	browser := &http.Server{Addr: ":8080", Handler: h.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	relay := &http.Server{Addr: ":8081", Handler: h.RelayHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second}
	browserListener, err := apphost.Listen(ctx, browser.Addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apphost: Browser-Listener nicht verfügbar")
		return 1
	}
	defer func() { _ = browserListener.Close() }()
	relayListener, err := apphost.Listen(ctx, relay.Addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apphost: Relay-Listener nicht verfügbar")
		return 1
	}
	defer func() { _ = relayListener.Close() }()
	errs := make(chan error, 2)
	go func() { errs <- browser.Serve(browserListener) }()
	go func() { errs <- relay.Serve(relayListener) }()
	code := 0
	select {
	case <-ctx.Done():
	case <-errs:
		fmt.Fprintln(os.Stderr, "apphost: Listener beendet")
		code = 1
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = browser.Shutdown(shutdown)
	_ = relay.Shutdown(shutdown)
	return code
}

// initLibs runs as 65532 on the full libs mount, before a subpath mount can
// resolve ready. It is idempotent and preserves existing prepared libraries.
func initLibs(root string) error {
	for _, dir := range []struct {
		name string
		mode os.FileMode
	}{{"ready", 0o755}, {"staging", 0o700}} {
		path := filepath.Join(root, dir.name)
		if err := os.MkdirAll(path, dir.mode); err != nil {
			return err
		}
		if err := os.Chmod(path, dir.mode); err != nil {
			return err
		}
	}
	return nil
}

func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func runMediator() int {
	log := logger()
	maxWallS, err := maxWallFromEnv()
	if err != nil {
		log.Error(err.Error())
		return 1
	}
	gateway := os.Getenv("JNPT_GATEWAY_URL")
	if gateway == "" {
		gateway = "http://jnpt:8484"
	}
	m, err := mediator.New(mediator.Config{
		SocketPath: socketPath, KeyDir: keyDir, StoreDir: storeDir, LibsDir: libsDir, LibsStagingDir: "/libs/staging", GatewayURL: gateway, Logger: log,
		MaxWallS: maxWallS,
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

func maxWallFromEnv() (int, error) {
	value := os.Getenv("JNPT_SCRIPT_RUNNER_MAX_WALL_S")
	if value == "" {
		return contract.MaxWallS, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 1 || seconds > contract.MaxWallS {
		return 0, errors.New("JNPT_SCRIPT_RUNNER_MAX_WALL_S nicht in 1..86400")
	}
	return seconds, nil
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
