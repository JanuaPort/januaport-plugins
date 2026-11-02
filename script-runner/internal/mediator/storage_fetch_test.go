package mediator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
)

func courierFixture(t *testing.T, h http.HandlerFunc) (*storageCourier, *httptest.Server, *time.Time, *syncBuffer) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	root := t.TempDir()
	key := filepath.Join(t.TempDir(), "runtime-key")
	if e := os.WriteFile(key, []byte(testKey), 0600); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	logs := &syncBuffer{}
	return newStorageCourier(srv.URL, key, root, func() time.Time { return now }, slog.New(slog.NewTextHandler(logs, nil))), srv, &now, logs
}
func courierList(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("../../contract/golden/storage/valid.json")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func courierPackageName(t *testing.T) string {
	t.Helper()
	var s storagepins.Signed
	var m storagepins.Manifest
	if json.Unmarshal(courierList(t), &s) != nil || json.Unmarshal(s.Manifest, &m) != nil {
		t.Fatal("golden")
	}
	p := m.Pins[0]
	return p.Name + "-" + p.Commit + ".pkg"
}

func TestCourierOpaqueAtomicTransportAndPrune(t *testing.T) {
	raw := append([]byte(" \n"), courierList(t)...)
	body := []byte("opaque-package-CANARY")
	var packages int
	c, _, now, logs := courierFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Body != http.NoBody && r.ContentLength != 0 || r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Error("request contract")
		}
		if strings.HasSuffix(r.URL.Path, "/pins") {
			if r.URL.RawQuery != "" {
				t.Error("list query")
			}
			_, _ = w.Write(raw)
			return
		}
		packages++
		if r.URL.Query().Get("path") != "scripts/demo" || len(r.URL.Query()) != 1 {
			t.Error("package query")
		}
		_, _ = w.Write(body)
	})
	c.Once(context.Background())
	b, e := os.ReadFile(filepath.Join(c.root, "inbox/list.json"))
	if e != nil || !bytes.Equal(raw, b) {
		t.Fatal("raw list not preserved", e)
	}
	path := filepath.Join(c.root, "inbox/pkg", courierPackageName(t))
	b, e = os.ReadFile(path)
	if e != nil || !bytes.Equal(body, b) {
		t.Fatal("raw package not preserved", e)
	}
	*now = now.Add(time.Minute)
	c.Once(context.Background())
	if packages != 1 {
		t.Fatal("existing package downloaded again")
	}
	raw = []byte(`{"manifest":{"pins":[]},"sig":"opaque"}`)
	*now = now.Add(time.Minute)
	c.Once(context.Background())
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("unlisted package retained")
	}
	if strings.Contains(logs.String(), "CANARY") || strings.Contains(logs.String(), testKey) {
		t.Fatal("log echo")
	}
	if _, e = os.Stat(filepath.Join(c.root, "state.json")); !os.IsNotExist(e) {
		t.Fatal("courier wrote approval state")
	}
}

func TestCourierHTTPFailuresBackoffAndNoEcho(t *testing.T) {
	for _, status := range []int{404, 503, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			hits := 0
			c, srv, now, logs := courierFixture(t, func(w http.ResponseWriter, r *http.Request) {
				hits++
				if status == 503 {
					w.Header().Set("Retry-After", "120")
				}
				w.Header().Set("Location", "/stolen?key=URL-CANARY")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "BODY-CANARY "+testKey)
			})
			c.Once(context.Background())
			if hits != 1 {
				t.Fatal("redirect followed")
			}
			if _, e := os.Stat(filepath.Join(c.root, "inbox/list.json")); !os.IsNotExist(e) {
				t.Fatal("error body published")
			}
			delay := time.Minute
			if status == 404 {
				delay = 5 * time.Minute
			}
			if status == 503 {
				delay = 2 * time.Minute
			}
			*now = now.Add(delay - time.Nanosecond)
			c.Once(context.Background())
			if hits != 1 {
				t.Fatal("backoff bypassed")
			}
			*now = now.Add(time.Nanosecond)
			c.Once(context.Background())
			if hits != 2 {
				t.Fatal("backoff did not expire")
			}
			srv.Close()
			*now = now.Add(10 * time.Minute)
			c.Once(context.Background())
			for _, bad := range []string{"BODY-CANARY", "URL-CANARY", testKey, srv.URL} {
				if strings.Contains(logs.String(), bad) {
					t.Fatal("log echo", bad)
				}
			}
		})
	}
}

func TestCourierRedirectNeverReachesSecondServer(t *testing.T) {
	var hits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	c, _, _, _ := courierFixture(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL+"/key", 307) })
	c.Once(context.Background())
	if hits.Load() != 0 {
		t.Fatal("redirect followed")
	}
}

type countingTransport struct {
	base http.RoundTripper
	read atomic.Int64
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, e := c.base.RoundTrip(r)
	if e == nil {
		resp.Body = &countingBody{ReadCloser: resp.Body, n: &c.read}
	}
	return resp, e
}

type countingBody struct {
	io.ReadCloser
	n *atomic.Int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, e := c.ReadCloser.Read(p)
	c.n.Add(int64(n))
	return n, e
}
func TestCourierLimitReaderBoundsActualHTTPRead(t *testing.T) {
	c, _, _, _ := courierFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(bytes.Repeat([]byte("x"), 2<<20)) })
	transport := &countingTransport{base: c.client.Transport}
	c.client.Transport = transport
	c.Once(context.Background())
	if transport.read.Load() > storagepins.MaxListBytes+1 {
		t.Fatal("unbounded body read", transport.read.Load())
	}
	if _, e := os.Stat(filepath.Join(c.root, "inbox/list.json")); !os.IsNotExist(e) {
		t.Fatal("oversize published")
	}
}

func TestCourierPackageLimitsAndRetry(t *testing.T) {
	for _, kind := range []string{"package_limit", "total_limit", "404", "503"} {
		t.Run(kind, func(t *testing.T) {
			count := 0
			c, _, now, _ := courierFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/pins") {
					_, _ = w.Write(courierList(t))
					return
				}
				count++
				if kind == "404" || kind == "503" {
					w.Header().Set("Retry-After", "180")
					if kind == "404" {
						w.WriteHeader(404)
					} else {
						w.WriteHeader(503)
					}
					return
				}
				_, _ = io.WriteString(w, strings.Repeat("x", 65))
			})
			c.packageLimit = 64
			if kind == "total_limit" {
				c.packageLimit = 128
				c.totalLimit = 64
			}
			c.Once(context.Background())
			p := filepath.Join(c.root, "inbox/pkg", courierPackageName(t))
			if _, e := os.Stat(p); !os.IsNotExist(e) {
				t.Fatal("failed package published")
			}
			if kind == "404" || kind == "503" {
				*now = now.Add(time.Minute)
				c.Once(context.Background())
				if count != 1 {
					t.Fatal("package backoff bypassed")
				}
			}
		})
	}
}

func TestCourierRejectsPathEscapeAndReloadsKey(t *testing.T) {
	hits := 0
	auth := ""
	c, _, now, _ := courierFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"manifest":{"pins":[{"name":"../../escape","commit":"x","path":"/"}]},"sig":"x"}`)
	})
	c.Once(context.Background())
	if hits != 1 {
		t.Fatal("unsafe routing fetched")
	}
	if e := os.WriteFile(c.keyPath, []byte("rotated"), 0600); e != nil {
		t.Fatal(e)
	}
	*now = now.Add(time.Minute)
	c.Once(context.Background())
	if auth != "Bearer rotated" {
		t.Fatal("old key reused")
	}
	if e := os.Remove(c.keyPath); e != nil {
		t.Fatal(e)
	}
	*now = now.Add(time.Minute)
	c.Once(context.Background())
	if hits != 2 {
		t.Fatal("request without key")
	}
}
