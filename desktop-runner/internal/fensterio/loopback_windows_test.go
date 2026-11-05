//go:build windows

package fensterio

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/anmeldung"
)

// Kein Browser und keine GUI. Echter Windows-HTTP-Adapter, auch auf BOX separat ausführbar.
func TestLoopbackNurEinmalUndFesterHost(t *testing.T) {
	f, _ := anmeldung.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l, e := newLoopback(ctx, f)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	host := fmt.Sprintf("127.0.0.1:%d", l.Port())
	u := "http://" + host + "/jnpt/callback?state=" + f.State + "&code=synthetic-code"
	c := &http.Client{Timeout: time.Second}
	for _, tc := range []struct{ host, url string }{{"localhost:" + fmt.Sprint(l.Port()), u}, {host, strings.Replace(u, f.State, strings.Repeat("x", 43), 1)}, {host, u + "&fehler=abgebrochen"}, {host, u + "&code=other"}} {
		req, _ := http.NewRequest("GET", tc.url, nil)
		req.Host = tc.host
		res, e := c.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatal(res.StatusCode)
		}
		select {
		case <-l.result:
			t.Fatal("falscher Rückruf verbraucht")
		default:
		}
	}
	res, e := c.Get(u)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'none'") || strings.Contains(string(b), f.State) || strings.Contains(string(b), "synthetic-code") {
		t.Fatal("Antwort")
	}
	select {
	case r := <-l.result:
		if r.Code != "synthetic-code" {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("kein Ergebnis")
	}
	select {
	case <-l.done:
	case <-time.After(time.Second):
		t.Fatal("Listener bleibt offen")
	}
	if conn, e := net.DialTimeout("tcp4", host, time.Second); e == nil {
		conn.Close()
		t.Fatal("Listener weiter offen")
	}
}

func TestLoopbackAbbruchUndFehler(t *testing.T) {
	for _, code := range []string{"kein_zugriff", "platz_gesperrt", "abgebrochen", "cancel"} {
		t.Run(code, func(t *testing.T) {
			f, _ := anmeldung.New()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			l, e := newLoopback(ctx, f)
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			if code == "cancel" {
				cancel()
			} else {
				res, e := http.Get(fmt.Sprintf("http://127.0.0.1:%d/jnpt/callback?state=%s&fehler=%s", l.Port(), f.State, code))
				if e != nil {
					t.Fatal(e)
				}
				res.Body.Close()
				r := <-l.result
				if r.Code != "" || r.Fehler != code {
					t.Fatal(r)
				}
			}
			select {
			case <-l.done:
			case <-time.After(time.Second):
				t.Fatal("nicht geschlossen")
			}
		})
	}
}
