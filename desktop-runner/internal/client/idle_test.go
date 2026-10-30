package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIdleVorSpawnUndMount(t *testing.T) { // L1
	idle := func(c *Config) { c.StartPolicy.Mode = "idle"; c.StartPolicy.IdleMinutes = 10 }
	for name, v := range map[string]struct {
		cfg     func(*Config)
		session Session
		end     string
		detail  string
	}{
		"Leerlauf zu kurz":            {idle, Session{Active: true, Account: "own", Idle: 9 * time.Minute}, "refused", "idle"},
		"Leerlauf null":               {idle, Session{Active: true, Account: "own"}, "refused", "idle"},
		"falsches Konto vor Leerlauf": {idle, Session{Active: true, Account: "other", Idle: time.Minute}, "refused", "account"},
		"Sitzung inaktiv":             {idle, Session{Active: false, Account: "own", Idle: time.Hour}, "refused", "account"},
		"kein Konto":                  {idle, Session{Active: true, Idle: time.Hour}, "refused", "account"},
		"always, falsches Konto":      {func(*Config) {}, Session{Active: true, Account: "other"}, "refused", "account"},
		"Leerlauf genau erreicht":     {idle, Session{Active: true, Account: "own", Idle: 10 * time.Minute}, "ok", ""},
		"always":                      {func(*Config) {}, Session{Active: true, Account: "own"}, "ok", ""},
	} {
		t.Run(name, func(t *testing.T) {
			b := newBed(t)
			v.cfg(&b.engine.Config)
			b.session = v.session
			got := b.execute()
			if got.End != v.end || got.Detail != v.detail || got.Log != "" {
				t.Fatalf("%+v", got)
			}
			if v.end == "refused" {
				if len(b.launches) != 0 {
					t.Fatal("Spawn trotz Ablehnung")
				}
				if exists(b.base()) {
					t.Fatal("Temp-Verzeichnis vor der Ablehnung angelegt")
				}
				if b.st.Journal().Marker != nil {
					t.Fatal("Laufmarker vor der Ablehnung geschrieben")
				}
			}
		})
	}
}

func TestIdleWirdNieAlsAccountGemeldet(t *testing.T) {
	r := Result{RunKey: "demo/daily/2026-10-23T10:00:00Z", Commit: "c"}
	got := refused(r, ErrIdle)
	if got.End != "refused" || got.Detail != "idle" {
		t.Fatalf("%+v", got)
	}
	if refused(r, ErrAccount).Detail != "account" {
		t.Fatal("account verändert")
	}
}

func resultKey(i int) string { return time.Unix(int64(i*60), 0).UTC().Format(time.RFC3339) }

func TestAbgelehntesErgebnisStautDenRueckstauNicht(t *testing.T) { // L2
	bad := &HTTPError{400, "invalid"}
	attemptsUntilDrop := func(t *testing.T, st *State) int {
		t.Helper()
		for n := 1; n <= 100; n++ {
			st.DeliverOne(func(Result) error { return bad })
			if st.Journal().Dropped > 0 {
				return n
			}
		}
		t.Fatal("abgelehntes Ergebnis blockiert den Rückstau dauerhaft")
		return 0
	}
	t.Run("nach N Versuchen verworfen, Nachfolger geht durch", func(t *testing.T) {
		st, _ := OpenState(t.TempDir(), testCipher{})
		st.Append(Result{RunKey: resultKey(1), End: "refused", Detail: "idle"})
		st.Append(Result{RunKey: resultKey(2), End: "ok"})
		n := attemptsUntilDrop(t, st)
		if n < 2 || n > 20 {
			t.Fatalf("Grenze unplausibel: %d", n)
		}
		j := st.Journal()
		if j.Dropped != 1 || len(j.Results) != 1 || j.Results[0].RunKey != resultKey(2) {
			t.Fatalf("%+v", j)
		}
		var sent []string
		if err := st.DeliverOne(func(r Result) error { sent = append(sent, r.RunKey); return nil }); err != nil || len(sent) != 1 || sent[0] != resultKey(2) || len(st.Journal().Results) != 0 {
			t.Fatal(err, sent)
		}
	})
	t.Run("Zähler gilt je Ergebnis", func(t *testing.T) {
		st, _ := OpenState(t.TempDir(), testCipher{})
		st.Append(Result{RunKey: resultKey(1), End: "refused", Detail: "idle"})
		st.Append(Result{RunKey: resultKey(2), End: "refused", Detail: "idle"})
		n := attemptsUntilDrop(t, st)
		for i := 1; i < n; i++ {
			st.DeliverOne(func(Result) error { return bad })
		}
		if j := st.Journal(); j.Dropped != 1 || len(j.Results) != 1 {
			t.Fatalf("Nachfolger erbt den Zähler: %+v", j)
		}
	})
	t.Run("andere Fehler verwerfen nie", func(t *testing.T) {
		for name, err := range map[string]error{
			"503":       &HTTPError{503, "unavailable"},
			"429":       &HTTPError{429, "rate"},
			"409 ended": &HTTPError{409, "ended"},
			"401":       &HTTPError{401, "unauthorized"},
			"Protokoll": ErrProtocol,
			"Netz":      context.DeadlineExceeded,
		} {
			st, _ := OpenState(t.TempDir(), testCipher{})
			st.Append(Result{RunKey: resultKey(1), End: "ok"})
			for i := 0; i < 100; i++ {
				st.DeliverOne(func(Result) error { return err })
			}
			if j := st.Journal(); j.Dropped != 0 || len(j.Results) != 1 {
				t.Fatalf("%s: verworfen %+v", name, j)
			}
		}
	})
	t.Run("anderer Fehler dazwischen setzt den Zähler zurück", func(t *testing.T) {
		probe, _ := OpenState(t.TempDir(), testCipher{})
		probe.Append(Result{RunKey: resultKey(1), End: "refused", Detail: "idle"})
		n := attemptsUntilDrop(t, probe)
		st, _ := OpenState(t.TempDir(), testCipher{})
		st.Append(Result{RunKey: resultKey(1), End: "refused", Detail: "idle"})
		for i := 1; i < n; i++ {
			st.DeliverOne(func(Result) error { return bad })
		}
		st.DeliverOne(func(Result) error { return &HTTPError{503, "unavailable"} })
		st.DeliverOne(func(Result) error { return bad })
		if st.Journal().Dropped != 0 {
			t.Fatal("Zähler nicht zurückgesetzt")
		}
	})
}

// Alter Kern: kennt die Klasse idle nicht und antwortet 400 invalid. Das Gerät
// meldet idle unverändert, verdeckt es nie als account und staut nicht.
func TestAltKernLehntIdleAbJournalStautNicht(t *testing.T) {
	var mu sync.Mutex
	var details []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var v struct {
			Detail string `json:"detail"`
			End    string `json:"end"`
		}
		json.Unmarshal(b, &v)
		mu.Lock()
		details = append(details, v.End+"/"+v.Detail)
		mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		if v.Detail == "idle" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"class":"invalid","message":"Ungültige Anfrage."}}`))
			return
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	api, err := NewAPI(Trust{URL: srv.URL}, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	st, _ := OpenState(t.TempDir(), testCipher{})
	st.Append(Result{RunKey: resultKey(1), ManifestSeq: 1, Commit: "c", End: "refused", Detail: "idle", FinishedAt: "2026-10-23T10:00:00Z"})
	st.Append(Result{RunKey: resultKey(2), ManifestSeq: 1, Commit: "c", End: "ok", FinishedAt: "2026-10-23T10:01:00Z"})
	for i := 0; i < 20 && len(st.Journal().Results) > 0; i++ {
		st.DeliverOne(func(r Result) error { return api.Report(context.Background(), r) })
	}
	j := st.Journal()
	if len(j.Results) != 0 || j.Dropped != 1 {
		t.Fatalf("Rückstau bleibt: %+v", j)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(details) < 3 || details[len(details)-1] != "ok/" {
		t.Fatalf("Nachfolger nicht zugestellt: %v", details)
	}
	for _, d := range details[:len(details)-1] {
		if d != "refused/idle" {
			t.Fatalf("idle wurde umgedeutet: %v", details)
		}
	}
}

func TestVerwurfWirdLokalGeloggt(t *testing.T) {
	b := newBed(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"class":"invalid","message":"Ungültige Anfrage."}}`))
	}))
	defer srv.Close()
	api, _ := NewAPI(Trust{URL: srv.URL}, "token", nil)
	defer api.Close()
	b.engine.API = api
	b.engine.Log = &Logger{Root: b.st.Root}
	b.st.Append(Result{RunKey: resultKey(1), ManifestSeq: 1, Commit: "c", End: "refused", Detail: "idle", FinishedAt: "2026-10-23T10:00:00Z"})
	for i := 0; i < 100 && b.st.Journal().Dropped == 0; i++ {
		b.engine.deliverOne(context.Background())
	}
	raw, err := os.ReadFile(filepath.Join(b.st.Root, "runner.log"))
	if err != nil || strings.TrimSpace(string(raw)) != "5 1" {
		t.Fatalf("Logzeile %q (%v)", raw, err)
	}
}
