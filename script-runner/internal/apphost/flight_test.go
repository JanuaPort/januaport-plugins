package apphost

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Done is consulted by packageFor only once the second request joins the flight.
type flightWaiterContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *flightWaiterContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

func TestFlightFirstCallerCancellationAtHTTP(t *testing.T) {
	f := setup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var fetches atomic.Int32
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fetches.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("ETag", `"`+f.commit+`"`)
		_, _ = w.Write(f.data)
	}))
	t.Cleanup(gw.Close)
	defer unblock()
	f.h.c.GatewayURL = gw.URL
	firstReturned := make(chan struct{})
	joined := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Probe") == "waiter" {
			r = r.WithContext(&flightWaiterContext{Context: r.Context(), joined: joined})
		}
		f.h.Handler().ServeHTTP(w, r)
		if r.Header.Get("X-Probe") == "first" {
			close(firstReturned)
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := func(ctx context.Context, probe string) (*http.Response, error) {
		r, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/", nil)
		if err != nil {
			return nil, err
		}
		r.Host = "a.apps.example.test"
		r.Header.Set("Cookie", "__Host-jnpt_app="+credential("app"))
		r.Header.Set("X-Probe", probe)
		return http.DefaultClient.Do(r)
	}
	firstCtx, cancelFirst := context.WithCancel(ctx)
	defer cancelFirst()
	go func() {
		r, _ := request(firstCtx, "first")
		if r != nil {
			_ = r.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first request never reached source")
	}
	second := make(chan *http.Response, 1)
	go func() {
		r, _ := request(ctx, "waiter")
		second <- r
	}()
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("second request never joined flight")
	}
	cancelFirst()
	select {
	case <-firstReturned:
	case <-time.After(time.Second):
		t.Error("canceled first caller still occupies handler")
	}
	unblock()
	r := <-second
	if r == nil {
		t.Fatal("waiting request failed")
	}
	b, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != 200 || err != nil || len(b) == 0 {
		t.Fatalf("waiting request: status=%d body=%q err=%v", r.StatusCode, b, err)
	}
	r, err = request(ctx, "cached")
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, r, 200)
	if fetches.Load() != 1 {
		t.Fatal("cancellation poisoned shared cache", fetches.Load())
	}
}

func TestFlightOwnDeadlineAtHTTP(t *testing.T) {
	f := setup(t)
	release := make(chan struct{})
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(gw.Close)
	defer close(release)
	f.h.c.GatewayURL = gw.URL
	// Disable the client's separate safety net: the flight must own its deadline.
	f.h.client.Timeout = 0
	f.h.client.Transport.(*http.Transport).ResponseHeaderTimeout = 0
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	r := httptest.NewRequest("GET", "http://a.apps.example.test/", nil).WithContext(ctx)
	r.Header.Set("Cookie", "__Host-jnpt_app="+credential("app"))
	w := httptest.NewRecorder()
	start := time.Now()
	f.h.Handler().ServeHTTP(w, r)
	elapsed := time.Since(start)
	if w.Code != 503 || elapsed < 29*time.Second || elapsed > 33*time.Second {
		t.Fatalf("own 30-second deadline: status=%d elapsed=%v", w.Code, elapsed)
	}
}
