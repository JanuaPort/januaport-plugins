package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestL5EndaufnahmeNachHaltUndWandzeit(t *testing.T) {
	for _, tc := range []struct {
		name, end string
		captures  int32
	}{
		{"anhalten", "cancelled", 0},
		{"wandzeit", "timeout", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := l5Bed(t, true, "test_aaaaaaaa")
			started := make(chan struct{})
			b.exit = func(*io.PipeWriter) { close(started) } // wait for kill/wall, no script capture
			var captures, uploads atomic.Int32
			b.engine.Capture = func(ctx context.Context) ([]byte, error) {
				captures.Add(1)
				if tc.name == "wandzeit" {
					if !b.engine.mu.TryLock() {
						t.Error("GDI must run outside e.mu")
						return nil, ErrProtocol
					}
					b.engine.mu.Unlock()
				}
				if err := ctx.Err(); err != nil {
					t.Error("completion capture inherited expired wall context", err)
					return nil, err
				}
				return l5PNG, nil
			}
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
				uploads.Add(1)
				if r.URL.Path != "/api/device/v1/jobs/"+b.result.JobID+"/screenshot" {
					t.Error("unexpected route", r.URL.Path)
				}
				w.WriteHeader(204)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan Result, 1)
			wall := 1
			if tc.name == "anhalten" {
				wall = 10
			}
			go func() {
				done <- b.engine.Execute(ctx, b.result, b.env, json.RawMessage(`{}`), "", wall, 0)
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("process did not start")
			}
			if tc.name == "anhalten" {
				mustHalt(t, b.engine, "angehalten", time.Time{})
			}
			select {
			case got := <-done:
				if got.End != tc.end || captures.Load() != tc.captures || uploads.Load() != tc.captures {
					t.Fatalf("end=%s captures=%d uploads=%d; want %s/%d/%d", got.End, captures.Load(), uploads.Load(), tc.end, tc.captures, tc.captures)
				}
			case <-ctx.Done():
				t.Fatal("run did not complete")
			}
		})
	}
}

func TestL5LaufpinSeparatGebunden(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		name, pin := "matching", "test_aaaaaaaa"
		if mismatch {
			name, pin = "different-run-pin", "demo"
		}
		t.Run(name, func(t *testing.T) {
			b := l5Bed(t, true, pin)
			var order TestOrder
			if err := b.st.Load("test-"+b.result.JobID, &order); err != nil {
				t.Fatal(err)
			}
			if mismatch {
				// Keep the executable package and environment valid for demo.
				// A separate signed test grant references those same bytes. verified
				// validates the grant, not the running environment's pin name.
				m, err := ParseManifest(order.Envelope.Manifest)
				if err != nil {
					t.Fatal(err)
				}
				m.Pins[0].Pin = "test_aaaaaaaa"
				order.Envelope, b.engine.Credential = l1Sign(t, encode(m), m.DeviceID)
				order.Pin = m.Pins[0].Pin
				b.engine.bundle.Manifest.Envelope = order.Envelope
				if err = b.st.Save("test-"+order.JobID, order); err != nil {
					t.Fatal(err)
				}
			}
			p := b.env.Origin
			if !order.verified(b.engine.Credential) || order.JobID != b.result.JobID || order.ManifestSeq != b.result.ManifestSeq || order.Commit != b.result.Commit || p.Commit != order.Commit || p.Path != order.Path || p.Algo != order.Algo || (p.Pin != order.Pin) != mismatch {
				t.Fatal("fixture must pass verified and every binding except the run pin")
			}
			var captures, uploads atomic.Int32
			b.engine.Capture = func(context.Context) ([]byte, error) { captures.Add(1); return l5PNG, nil }
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { uploads.Add(1); w.WriteHeader(204) })
			in := l5Raw(b, `{"type":"ergebnis","wert":42}`, `{"type":"screenshot"}`)
			got := b.execute()
			wantResult, wantCaptures, wantReply := "42", int32(2), "screenshot"
			if mismatch {
				wantResult, wantCaptures, wantReply = "", 0, "fehler"
			}
			replies := l5Replies(t, in)
			if got.End != "ok" || string(got.Result) != wantResult || captures.Load() != wantCaptures || uploads.Load() != wantCaptures || len(replies) != 2 || replies[1]["type"] != wantReply {
				t.Fatalf("end=%s result=%s captures=%d uploads=%d replies=%v", got.End, got.Result, captures.Load(), uploads.Load(), replies)
			}
		})
	}
}
