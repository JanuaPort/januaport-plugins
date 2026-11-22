package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// JanuaPort/januaport#1434: Nach gesetztem Laufmarker gibt es keinen Rückweg ohne Abschluss. Scheitert danach ein
// Speichern, wird der Lauf als interner Fehler abgeschlossen (report), damit der Marker ins Journal wandert. Sonst
// endet jeder weitere Auftrag bis zum Neustart mit ErrBusy.

// speicherSperre macht name unspeicherbar: An seiner Stelle liegt ein nicht leeres Verzeichnis, die atomare
// Ersetzung der Datei scheitert (Fehlerinjektion ohne Eingriff in den Produktionscode).
func speicherSperre(t *testing.T, root, name string) (aufheben func()) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "blocker"), 0o700); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
}

func ergebnisFuer(t *testing.T, st *State, key string) Result {
	t.Helper()
	for _, r := range st.Journal().Results {
		if r.Key() == key {
			return r
		}
	}
	t.Fatalf("kein Ergebnis für %s: %+v", key, st.Journal())
	return Result{}
}

func TestAdHocSpeichernDerFreigabeScheitertSchliesstDenLaufUndGibtDenMarkerFrei(t *testing.T) {
	c, order := l1Order(t, true, "test_aaaaaaaa")
	st, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	e := Engine{State: st, Credential: c, bundle: Bundle{Manifest: ManifestState{Envelope: order.Envelope}}}
	job := func(id string) Job {
		return Job{JobID: id, Pin: order.Pin, Commit: order.Commit, Path: order.Path, Algo: order.Algo, ManifestSeq: order.ManifestSeq,
			Input: json.RawMessage(`{}`), StartBy: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	}
	erster := job(order.JobID)
	aufheben := speicherSperre(t, st.Root, "test-"+erster.JobID)

	if err = e.adhoc(context.Background(), erster); err != nil {
		t.Fatalf("adhoc: %v", err)
	}
	j := st.Journal()
	if j.Marker != nil {
		t.Fatalf("Laufmarker blieb stehen: %+v", j.Marker)
	}
	if r := ergebnisFuer(t, st, erster.JobID); r.End != "internal" {
		t.Fatalf("Ergebnis %+v, erwartet internal", r)
	}

	// Der nächste Auftrag läuft, statt mit ErrBusy verworfen zu werden.
	aufheben()
	zweiter := job("22345678-1234-4234-8234-123456789abc")
	if err = e.adhoc(context.Background(), zweiter); err != nil {
		t.Fatalf("Folgeauftrag: %v", err)
	}
	if r := ergebnisFuer(t, st, zweiter.JobID); r.End != "refused" || r.Detail != "runtime" {
		t.Fatalf("Folgeauftrag lief nicht: %+v", r)
	}
	if st.Journal().Marker != nil {
		t.Fatal("Marker nach dem Folgeauftrag")
	}
}

// Zeitplan-Weg: Nach reserveScheduled steht der Marker. Ein Fehler von ObserveTime (offline-Zweig) lässt ihn stehen.
func TestZeitplanObserveTimeScheitertSchliesstDenLaufUndGibtDenMarkerFrei(t *testing.T) {
	b := newSyncBed(t)
	b.serve(b.manifest(1, nil, b.gut))
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	b.e.syncErr = &net.OpError{Op: "dial", Err: errors.New("offline")}
	slot := time.Now().UTC().Truncate(time.Minute)
	aufheben := speicherSperre(t, b.st.Root, "clock")

	if err := b.e.scheduled(context.Background(), b.gut, b.gut.Schedules[0], slot); err != nil {
		t.Fatalf("scheduled: %v", err)
	}
	if b.st.Journal().Marker != nil {
		t.Fatalf("Laufmarker blieb stehen: %+v", b.st.Journal().Marker)
	}
	if r := ergebnisFuer(t, b.st, RunKey(b.gut, b.gut.Schedules[0], slot)); r.End != "internal" {
		t.Fatalf("Ergebnis %+v, erwartet internal", r)
	}

	aufheben()
	b.e.syncErr = nil
	if err := b.e.scheduled(context.Background(), b.gut, Schedule{Name: "nochmal", Input: json.RawMessage(`{}`)}, slot); err != nil {
		t.Fatalf("Folgelauf: %v", err)
	}
	if r := ergebnisFuer(t, b.st, RunKey(b.gut, Schedule{Name: "nochmal"}, slot)); r.End != "ok" || b.spawns.Load() != 1 {
		t.Fatalf("Folgelauf lief nicht: %+v, Starts %d", r, b.spawns.Load())
	}
}

// markerSeal lässt das n-te Schreiben eines Journals mit Marker einmal scheitern (ein vorübergehender Schreibfehler).
type markerSeal struct {
	testCipher
	n, failAt int
}

func (m *markerSeal) Seal(b []byte) ([]byte, error) {
	if bytes.Contains(b, []byte(`"marker"`)) {
		m.n++
		if m.n == m.failAt {
			return nil, errors.New("Platte voll")
		}
	}
	return m.testCipher.Seal(b)
}

// Zeitplan-Weg, Wiederholungszweig: Der Start meldet 409 manifest, der Läufer gleicht neu ab und markiert erneut.
// Scheitert dieses Mark, bleibt der Marker aus reserveScheduled stehen.
func TestZeitplanMarkImWiederholungszweigScheitertSchliesstDenLaufUndGibtDenMarkerFrei(t *testing.T) {
	b := newSyncBed(t)
	cipher := &markerSeal{failAt: 2} // 1: reserveScheduled, 2: Mark im Wiederholungszweig
	st, err := OpenState(t.TempDir(), cipher)
	if err != nil {
		t.Fatal(err)
	}
	b.st, b.e.State = st, st
	tool := b.pin("werkzeug", strings.Repeat("c", 64))
	tool.DeclaredTools = []string{"x"}
	b.seedEnv(b.gut, nil)
	b.seedEnv(tool, func(e *Environment) { e.Tools = []string{"x"} })

	var starts atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/runs/start") {
			b.handle(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if starts.Add(1) == 1 {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":{"class":"manifest","message":"veraltet"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(StartReply{RunToken: "synthetic", ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339), MCPPath: "/api/device/v1/mcp"})
	}))
	t.Cleanup(proxy.Close)
	api, err := NewAPI(Trust{URL: proxy.URL}, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	b.e.API = api

	b.serve(b.manifest(1, nil, b.gut, tool))
	if err = b.sync(); err != nil {
		t.Fatal("Ausgangslage:", err)
	}
	slot := time.Now().UTC().Truncate(time.Minute)
	if err = b.e.scheduled(context.Background(), tool, tool.Schedules[0], slot); err != nil {
		t.Fatalf("scheduled: %v", err)
	}
	if cipher.n < 2 || starts.Load() != 1 {
		t.Fatalf("Ausgangslage des Tests: Journalschreibungen mit Marker %d, Starts %d", cipher.n, starts.Load())
	}
	if b.st.Journal().Marker != nil {
		t.Fatalf("Laufmarker blieb stehen: %+v", b.st.Journal().Marker)
	}
	if r := ergebnisFuer(t, b.st, RunKey(tool, tool.Schedules[0], slot)); r.End != "internal" {
		t.Fatalf("Ergebnis %+v, erwartet internal", r)
	}
	if err = b.e.scheduled(context.Background(), b.gut, Schedule{Name: "nochmal", Input: json.RawMessage(`{}`)}, slot); err != nil {
		t.Fatalf("Folgelauf: %v", err)
	}
	if r := ergebnisFuer(t, b.st, RunKey(b.gut, Schedule{Name: "nochmal"}, slot)); r.End != "ok" {
		t.Fatalf("Folgelauf lief nicht: %+v", r)
	}
}
