package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type haltFailCipher struct {
	testCipher
	fail bool
}

func (c *haltFailCipher) Seal(b []byte) ([]byte, error) {
	if c.fail {
		return nil, errors.New("synthetic disk failure")
	}
	return c.testCipher.Seal(b)
}

func TestHaltSpeicherfehlerSperrt(t *testing.T) {
	b := haltBed(t)
	c := &haltFailCipher{}
	b.st.cipher = c
	mustHalt(t, b.engine, "angehalten", time.Time{})
	c.fail = true
	if err := b.engine.Halt("arbeitet", time.Time{}); err == nil {
		t.Fatal("Speicherfehler verschluckt")
	}
	state, _, fault := b.engine.HaltStatus()
	if state != "angehalten" || !fault {
		t.Fatal(state, fault)
	}
	if got := b.execute(); got.End != "cancelled" || len(b.launches) != 0 {
		t.Fatal(got)
	}
	c.fail = false
	mustHalt(t, b.engine, "arbeitet", time.Time{})
	if state, _, fault := b.engine.HaltStatus(); state != "arbeitet" || fault {
		t.Fatal(state, fault)
	}
}

func TestHaltMeldungNeustartUndBestaetigung(t *testing.T) {
	b := haltBed(t)
	var hits atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		n := hits.Add(1)
		if n == 1 {
			close(entered)
			<-release
		} else if body["state"] != "angehalten" {
			t.Error("veraltete Nachlieferung", body)
		}
		w.WriteHeader(204)
	})
	base := time.Now()
	mustHalt(t, b.engine, "pausiert", time.Time{})
	done := make(chan struct{})
	go func() { b.engine.deliverHalt(context.Background(), base); close(done) }()
	<-entered
	mustHalt(t, b.engine, "angehalten", time.Time{})
	close(release)
	<-done
	b.engine.deliverHalt(context.Background(), base.Add(10*time.Second))
	if hits.Load() != 2 {
		t.Fatal("alte Bestätigung löscht neueren Halt", hits.Load())
	}
	st, err := OpenState(b.st.Root, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{State: st, API: b.engine.API}
	e.deliverHalt(context.Background(), base.Add(11*time.Second))
	if hits.Load() != 2 {
		t.Fatal("Neustart umgeht Bremse")
	}
	e.deliverHalt(context.Background(), base.Add(20*time.Second))
	if hits.Load() != 3 {
		t.Fatal("Neustart verliert letzte Meldung")
	}
}

func TestHaltMeldungOhneDauerhafteBremseGesperrt(t *testing.T) {
	for _, kind := range []string{"kaputt", "schreiben"} {
		t.Run(kind, func(t *testing.T) {
			b := haltBed(t)
			var hits atomic.Int32
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(204) })
			mustHalt(t, b.engine, "angehalten", time.Time{})
			if kind == "kaputt" {
				os.WriteFile(filepath.Join(b.st.Root, "halt-attempt"), []byte("bad"), 0600)
			} else {
				b.st.cipher = &haltFailCipher{fail: true}
			}
			b.engine.deliverHalt(context.Background(), time.Now())
			if hits.Load() != 0 {
				t.Fatal("ungezählte Meldung")
			}
		})
	}
}

func TestHaltBrichtOffenenClaimAb(t *testing.T) {
	b := haltBed(t)
	entered := make(chan struct{})
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() })
	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		j, err := b.engine.claim(ctx)
		if j != nil || err != nil {
			t.Error(j, err)
		}
		close(done)
	}()
	<-entered
	mustHalt(t, b.engine, "pausiert", time.Time{})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Long poll nicht abgebrochen")
	}
}

func TestHaltDirekteStartwegeGesperrt(t *testing.T) {
	for _, state := range []string{"pausiert", "angehalten"} {
		t.Run(state, func(t *testing.T) {
			b := haltBed(t)
			var hits atomic.Int32
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(204) })
			mustHalt(t, b.engine, state, time.Time{})
			if got := b.execute(); got.End != "cancelled" {
				t.Fatal(got)
			}
			if err := b.engine.adhoc(context.Background(), Job{}); err != nil {
				t.Fatal(err)
			}
			if err := b.engine.scheduled(context.Background(), b.env.Origin, Schedule{Name: "daily"}, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if _, err := b.engine.claim(context.Background()); err != nil {
				t.Fatal(err)
			}
			j := b.st.Journal()
			if len(b.launches) != 0 || hits.Load() != 0 || len(j.Results) != 0 || j.Marker != nil || len(j.Slots) != 0 {
				t.Fatal("Halt lässt Arbeit/Marker durch", j, hits.Load())
			}
		})
	}
}

func TestPauseNeustartUndAblaufOhneNachholen(t *testing.T) {
	b := haltBed(t)
	mustHalt(t, b.engine, "pausiert", time.Now().UTC().Add(time.Minute))
	st, err := OpenState(b.st.Root, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{State: st}
	if state, _, fault := e.HaltStatus(); state != "pausiert" || fault {
		t.Fatal(state, fault)
	}
	// A deadline elapsed while powered off: first startup read resumes locally,
	// skipping the current slot as well, even if it was never polled during pause.
	if err := st.Save("halt", haltState{State: "pausiert", Until: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	e = &Engine{State: st}
	if state, _, fault := e.HaltStatus(); state != "arbeitet" || fault {
		t.Fatal(state, fault)
	}
	if !e.scheduleHalted(time.Now().UTC().Truncate(time.Minute)) {
		t.Fatal("Pause holt beim Start nach")
	}
	if e.scheduleHalted(time.Now().UTC().Truncate(time.Minute).Add(time.Minute)) {
		t.Fatal("nächster Slot gesperrt")
	}
}

func TestHaltMeldungWiederholtFehler(t *testing.T) {
	b := haltBed(t)
	var hits atomic.Int32
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(503) })
	mustHalt(t, b.engine, "angehalten", time.Time{})
	base := time.Now()
	b.engine.deliverHalt(context.Background(), base)
	b.engine.deliverHalt(context.Background(), base.Add(10*time.Second))
	if hits.Load() != 2 {
		t.Fatal("fehlgeschlagene Meldung verworfen", hits.Load())
	}
}

func TestHaltLaesstServerUnveraendert(t *testing.T) {
	b := haltBed(t)
	b.engine.Container = &DockerExecutor{}
	if err := b.st.Save("halt", haltState{State: "angehalten"}); err != nil {
		t.Fatal(err)
	}
	if err := b.engine.Halt("angehalten", time.Time{}); err == nil {
		t.Fatal("Windows-Steuerung am Server")
	}
	if state, _, fault := b.engine.HaltStatus(); state != "arbeitet" || fault {
		t.Fatal(state, fault)
	}
	if b.engine.workHalted() || b.engine.scheduleHalted(time.Now()) {
		t.Fatal("Server durch lokale Windows-Datei gesperrt")
	}
}

func TestHaltSperrtSlotreservierung(t *testing.T) {
	b := haltBed(t)
	mustHalt(t, b.engine, "angehalten", time.Time{})
	// Exercise the final admission boundary directly, as if Halt arrived after
	// scheduled's earlier check and manifest parsing. This uses the real State.
	ok, err := b.engine.reserveScheduled("demo/daily", b.result, time.Now().Add(time.Minute))
	if err != nil || ok {
		t.Fatal("Slot nach Halt reserviert", ok, err)
	}
	j := b.st.Journal()
	if j.Marker != nil || len(j.Slots) != 0 || len(j.Results) != 0 {
		t.Fatal(j)
	}
}
