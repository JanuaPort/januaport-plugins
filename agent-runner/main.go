package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/januaport/januaport-plugins/agent-runner/internal/runner"
)

func main() {
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-runner: Dienst nicht verfügbar.")
		os.Exit(1)
	}
}
func serve() error {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		c := &http.Client{Timeout: 3 * time.Second}
		res, e := c.Get("http://127.0.0.1:8090/healthz")
		if e != nil {
			return e
		}
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			return errors.New("not ready")
		}
		return nil
	}
	gateway := os.Getenv("JNPT_GATEWAY_URL")
	if gateway == "" {
		gateway = "http://jnpt:8484"
	}
	wall := runner.DefaultMaxWallS
	if s := os.Getenv("JNPT_AGENT_MAX_WALL_S"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil {
			return e
		}
		wall = n
	}
	r, e := runner.New(runner.Config{GatewayURL: gateway, KeyFile: "/run/jnpt/plugins/agent-runner/runtime-key", MaxWallS: wall, Log: os.Stdout})
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	handler := r.Handler()
	server := &http.Server{Addr: ":8090", ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: time.Duration(wall+10) * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	// Cancellation of the process also reaches each active request.
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requestCtx, cancel := context.WithCancel(req.Context())
		defer cancel()
		unregister := context.AfterFunc(ctx, cancel)
		defer unregister()
		handler.ServeHTTP(w, req.WithContext(requestCtx))
	})
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdown)
		case <-done:
		}
	}()
	e = server.ListenAndServe()
	close(done)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
