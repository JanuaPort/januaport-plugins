//go:build windows

package fensterio

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/anmeldung"
)

const loginLifetime = 5 * time.Minute

type loopback struct {
	port   int
	result chan anmeldung.Result
	done   chan struct{}
	cancel context.CancelFunc
}

func (l *loopback) Port() int { return l.port }
func (l *loopback) Close()    { l.cancel() }

// Einzige TCP-Listener-Ausnahme im Modul. Lebensdauer ab Knopfdruck, nie im Ruhezustand.
func newLoopback(parent context.Context, f anmeldung.Flow) (*loopback, error) {
	ctx, cancel := context.WithTimeout(parent, loginLifetime)
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		cancel()
		return nil, ErrOpen
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if port < 1024 {
		listener.Close()
		cancel()
		return nil, ErrOpen
	}
	l := &loopback{port: port, result: make(chan anmeldung.Result, 1), done: make(chan struct{}), cancel: cancel}
	host := "127.0.0.1:" + strconv.Itoa(port)
	var mu sync.Mutex
	consumed := false
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 4096, ErrorLog: log.New(io.Discard, "", 0)}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Connection", "close")
		result, ok := f.Callback(r.Method, r.Host, host, r.URL.EscapedPath(), r.URL.RawQuery)
		mu.Lock()
		defer mu.Unlock()
		if !ok || consumed || ctx.Err() != nil || r.URL.IsAbs() || r.ContentLength > 0 || len(r.TransferEncoding) != 0 {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, "Ungültiger Rückruf.")
			return
		}
		consumed = true
		// Serve beendet sich sofort; Shutdown lässt nur diese feste Antwort fertig schreiben.
		_ = listener.Close()
		_, _ = io.WriteString(w, "<!doctype html><html lang=de><meta charset=utf-8><title>JanuaPort</title><p>Du kannst zum App-Fenster zurückkehren.</p></html>")
		l.result <- result
	})
	go func() {
		defer close(l.done)
		_ = server.Serve(listener)
		shutdown, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
		_ = server.Close()
		cancel()
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-l.done:
		}
	}()
	return l, nil
}
