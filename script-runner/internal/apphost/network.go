package apphost

import (
	"context"
	"net"
	"net/http"
	"time"
)

func newDialer() *net.Dialer {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	d.SetMultipathTCP(false)
	return d
}

func newListenConfig() *net.ListenConfig {
	lc := &net.ListenConfig{}
	lc.SetMultipathTCP(false)
	return lc
}

func newTransport() *http.Transport {
	return &http.Transport{Proxy: nil, DialContext: newDialer().DialContext,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout: 60 * time.Second, MaxIdleConns: 32}
}

// Listen disables MPTCP explicitly, independently of GODEBUG and kernel defaults.
func Listen(ctx context.Context, addr string) (net.Listener, error) {
	return newListenConfig().Listen(ctx, "tcp", addr)
}
