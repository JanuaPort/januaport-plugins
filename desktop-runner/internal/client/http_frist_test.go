package client

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// JanuaPort/januaport#1426: Der Paketabruf hat keine Gesamtfrist mehr, sondern eine Stillstandsfrist (30 s ohne ein
// einziges empfangenes Byte). Netzfehler und Stillstand im Körper gelten als offline, nie als ErrProtocol. Werkzeug-
// aufrufe des App-Gateways gelten bis zur dokumentierten Zeitgrenze (60 s je Befehl, getragen vom Kontext), die
// Kopf-Frist unterschreitet sie nicht. Heartbeat, Claim und Report behalten 30 s bis zum Kopf und 40 s insgesamt.
// Die Fristen sind Felder der API und werden in den Tests auf Millisekunden verkürzt (keine echten 40 s).

var paketPin = Pin{Pin: "demo", Algo: "sha256", Commit: strings.Repeat("a", 64), Path: "demo"}

func fristAPI(t *testing.T, h http.HandlerFunc, f fristen) *API {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	a, err := NewAPI(Trust{URL: srv.URL}, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.fristen = f
	return a
}

func paketKopf(w http.ResponseWriter, laenge int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/vnd.jnpt.gitobjects.v1")
	w.Header().Set("Content-Length", strconv.Itoa(laenge))
	w.WriteHeader(200)
}

func schnell() fristen {
	return fristen{kurzKopf: time.Second, kurzGesamt: 2 * time.Second, werkzeugKopf: 2 * time.Second, stillstand: 400 * time.Millisecond}
}

func TestStandardfristen(t *testing.T) {
	a, err := NewAPI(Trust{URL: "http://127.0.0.1:1"}, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := fristen{kurzKopf: 30 * time.Second, kurzGesamt: 40 * time.Second, werkzeugKopf: 60 * time.Second, stillstand: 30 * time.Second}
	if a.fristen != want {
		t.Fatalf("fristen = %+v, erwartet %+v", a.fristen, want)
	}
	if a.fristen.werkzeugKopf < 60*time.Second {
		t.Fatal("Kopf-Frist der Werkzeugaufrufe unterschreitet die dokumentierte Zeitgrenze 60 s")
	}
	if a.http.Timeout != 0 {
		t.Fatalf("der HTTP-Client trägt eine feste Gesamtfrist: %v", a.http.Timeout)
	}
}

// Langsam, aber stetig: Die Gesamtzeit liegt weit über der kurzen Gesamtfrist, jede Lücke unter der Stillstandsfrist.
func TestPaketAbrufStetigUeberDieGesamtfristHinausGelingt(t *testing.T) {
	paket := bytes.Repeat([]byte("p"), 20*1024)
	f := fristen{kurzKopf: 200 * time.Millisecond, kurzGesamt: 300 * time.Millisecond, werkzeugKopf: time.Second, stillstand: 400 * time.Millisecond}
	a := fristAPI(t, func(w http.ResponseWriter, r *http.Request) {
		paketKopf(w, len(paket))
		for i := 0; i < len(paket); i += 1024 {
			if _, err := w.Write(paket[i : i+1024]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(40 * time.Millisecond)
		}
	}, f)
	start := time.Now()
	got, err := a.Package(context.Background(), paketPin)
	if err != nil {
		t.Fatalf("Paketabruf: %v", err)
	}
	if time.Since(start) < 2*f.kurzGesamt {
		t.Fatalf("Der Test dauerte nur %v, die Gesamtfrist wurde nicht überschritten", time.Since(start))
	}
	if !bytes.Equal(got, paket) {
		t.Fatal("Paket unvollständig")
	}
}

func TestPaketAbrufStillstandImKoerperIstNetzfehlerNieProtokollfehler(t *testing.T) {
	a := fristAPI(t, func(w http.ResponseWriter, r *http.Request) {
		paketKopf(w, 10000)
		_, _ = w.Write(bytes.Repeat([]byte("p"), 100))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // verstummt mitten im Körper
	}, schnell())
	start := time.Now()
	_, err := a.Package(context.Background(), paketPin)
	if err == nil || !IsOffline(err) || errors.Is(err, ErrProtocol) {
		t.Fatalf("Stillstand im Körper: %v, erwartet Netzklasse (offline)", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Abbruch nach %v, erwartet etwa die Stillstandsfrist", d)
	}
}

func TestPaketAbrufStillstandVorDemKopfIstOffline(t *testing.T) {
	a := fristAPI(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}, schnell())
	start := time.Now()
	_, err := a.Package(context.Background(), paketPin)
	if err == nil || !IsOffline(err) {
		t.Fatalf("Stillstand vor dem Kopf: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Abbruch nach %v", d)
	}
}

func TestPaketAbrufVerbindungsabbruchImKoerperIstOffline(t *testing.T) {
	a := fristAPI(t, func(w http.ResponseWriter, r *http.Request) {
		paketKopf(w, 10000)
		_, _ = w.Write(bytes.Repeat([]byte("p"), 100))
		w.(http.Flusher).Flush()
		c, _, _ := w.(http.Hijacker).Hijack()
		_ = c.Close()
	}, schnell())
	_, err := a.Package(context.Background(), paketPin)
	if err == nil || !IsOffline(err) || errors.Is(err, ErrProtocol) {
		t.Fatalf("Verbindungsabbruch im Körper: %v, erwartet Netzklasse (offline)", err)
	}
}

// Ein echter Protokollfehler bleibt ErrProtocol: Größe über der Grenze und falscher Inhaltstyp.
func TestPaketAbrufProtokollfehlerBleibenProtokollfehler(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"zu gross": func(w http.ResponseWriter, r *http.Request) {
			paketKopf(w, 4096)
			_, _ = w.Write(bytes.Repeat([]byte("p"), 4096))
		},
		"falscher Inhaltstyp": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := fristAPI(t, handler, schnell())
			_, _, err := a.request(context.Background(), "GET", "packages/"+paketPin.Commit+"?path=demo", nil, 0, "", 1024)
			if !errors.Is(err, ErrProtocol) || IsOffline(err) {
				t.Fatalf("%v, erwartet ErrProtocol", err)
			}
		})
	}
}

// Halt und Stop beenden den Abruf sofort, auch mitten im Körper und bei langer Stillstandsfrist.
func TestPaketAbrufKontextAbbruchBeendetSofort(t *testing.T) {
	f := schnell()
	f.stillstand = time.Minute
	a := fristAPI(t, func(w http.ResponseWriter, r *http.Request) {
		paketKopf(w, 1<<20)
		for r.Context().Err() == nil {
			_, _ = w.Write([]byte("p"))
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}, f)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	start := time.Now()
	_, err := a.Package(ctx, paketPin)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Abbruch: %v, erwartet context.Canceled", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Abruf endete erst nach %v", d)
	}
}

func appRPCServer(t *testing.T, kopfVerzoegerung, koerperPause time.Duration, f fristen) (*API, AppSession) {
	t.Helper()
	a := fristAPI(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(kopfVerzoegerung)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,`))
		w.(http.Flusher).Flush()
		time.Sleep(koerperPause)
		_, _ = w.Write([]byte(`"result":{"ok":true}}`))
	}, f)
	return a, AppSession{token: appTokenPrefix + "synthetic"}
}

// Gateway-Befehle gelten bis zur Zeitgrenze des Kontexts, nicht bis zu den kurzen Fristen der übrigen Aufrufe.
func TestAppWerkzeugaufrufUeberDieKurzenFristenHinausGelingt(t *testing.T) {
	f := fristen{kurzKopf: 100 * time.Millisecond, kurzGesamt: 200 * time.Millisecond, werkzeugKopf: 3 * time.Second, stillstand: time.Second}
	a, s := appRPCServer(t, 400*time.Millisecond, 400*time.Millisecond, f)
	res, err := a.AppRPC(context.Background(), s, 1, "tools/list", nil)
	if err != nil || string(res) != `{"ok":true}` {
		t.Fatalf("Werkzeugaufruf: %s %v", res, err)
	}
}

func TestAppWerkzeugaufrufEndetSpaetestensMitDemKontext(t *testing.T) {
	f := fristen{kurzKopf: 100 * time.Millisecond, kurzGesamt: 200 * time.Millisecond, werkzeugKopf: time.Minute, stillstand: time.Minute}
	a, s := appRPCServer(t, 1500*time.Millisecond, 0, f)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := a.AppRPC(ctx, s, 1, "tools/list", nil); err == nil {
		t.Fatal("Aufruf trotz abgelaufenem Kontext erfolgreich")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Kontext nicht beachtet: %v", d)
	}
}

// Gegenprobe: Die übrigen Aufrufe behalten ihre kurzen Fristen (hier auf Millisekunden verkürzt).
func TestKurzeAufrufeBehaltenKopfUndGesamtfrist(t *testing.T) {
	f := fristen{kurzKopf: 150 * time.Millisecond, kurzGesamt: 400 * time.Millisecond, werkzeugKopf: 3 * time.Second, stillstand: time.Second}
	t.Run("Kopf", func(t *testing.T) {
		a := fristAPI(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, f)
		start := time.Now()
		_, err := a.Config(context.Background())
		var ne net.Error
		if !errors.As(err, &ne) || time.Since(start) > 3*time.Second {
			t.Fatalf("Kopf-Frist: %v nach %v", err, time.Since(start))
		}
	})
	t.Run("Gesamt", func(t *testing.T) {
		a := fristAPI(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("{"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}, f)
		start := time.Now()
		if _, err := a.Config(context.Background()); err == nil || time.Since(start) > 3*time.Second {
			t.Fatalf("Gesamtfrist: %v nach %v", err, time.Since(start))
		}
	})
}
