package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

// #1537 Punkt 2: Ein Serverfehler (HTTP 5xx) des Kerns ist eine Störung auf der Gegenseite, kein Prüffehler des
// Geräts. IsOffline gilt deshalb auch für *HTTPError mit Status >= 500, damit die Offline-Kulanz greift und
// refused() kein `hash` für eine Kernstörung bucht.

func TestIsOfflineTabelle(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"Netzfehler", &net.DNSError{IsTimeout: true}, true},
		{"Netzfehler des Paketabrufs", &netzFehler{errStillstand}, true},
		{"503", &HTTPError{Status: 503, Class: "unavailable"}, true},
		{"500", &HTTPError{Status: 500, Class: "internal"}, true},
		{"599", &HTTPError{Status: 599}, true},
		{"503 umhüllt", fmt.Errorf("sync: %w", &HTTPError{Status: 503}), true},
		{"499", &HTTPError{Status: 499}, false},
		{"404", &HTTPError{Status: 404, Class: "not_found"}, false},
		{"401", &HTTPError{Status: 401}, false},
		{"409 manifest", &HTTPError{Status: 409, Class: "manifest"}, false},
		{"429", &HTTPError{Status: 429}, false},
		{"Widerruf", ErrRevoked, false},
		{"Protokollfehler", ErrProtocol, false},
		{"Manifestfehler", ErrManifest, false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsOffline(tc.err); got != tc.want {
				t.Fatalf("IsOffline(%v) = %v, erwartet %v", tc.err, got, tc.want)
			}
		})
	}
}

// Zeitplan: Ein 5xx des Syncs ist ein Offline-Zustand. Der Lauf unterliegt der Kulanz (hier innerhalb der Frist: er
// läuft, mit ran_offline), statt als refused/hash gebucht zu werden. Ein 404/401 bleibt ein Ablehnungsgrund.
func TestZeitplanKernstoerungImSyncBuchtKeinHash(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		end    string
		detail string
		offl   bool
	}{
		{"503", &HTTPError{Status: 503, Class: "unavailable"}, "ok", "", true},
		{"500", &HTTPError{Status: 500, Class: "internal"}, "ok", "", true},
		{"404", &HTTPError{Status: 404, Class: "not_found"}, "refused", "hash", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newSyncBed(t)
			b.serve(b.manifest(1, nil, b.gut))
			if err := b.sync(); err != nil {
				t.Fatal(err)
			}
			b.e.syncErr = tc.err
			r := b.slot(b.gut)
			if r.End != tc.end || r.Detail != tc.detail || r.RanOffline != tc.offl {
				t.Fatalf("Ergebnis %+v, erwartet %s/%s, ran_offline %v", r, tc.end, tc.detail, tc.offl)
			}
		})
	}
}

// Außerhalb der Kulanz wird die Kernstörung als refused/offline gebucht, nie als hash.
func TestZeitplanKernstoerungNachAblaufDerKulanzIstOffline(t *testing.T) {
	b := newSyncBed(t)
	b.serve(b.manifest(1, nil, b.gut))
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	bundle := b.e.view()
	bundle.Manifest.LastSync = bundle.Manifest.LastSync.AddDate(0, 0, -30)
	b.e.mu.Lock()
	b.e.bundle = bundle
	b.e.mu.Unlock()
	b.e.syncErr = &HTTPError{Status: 503}
	r := b.slot(b.gut)
	if r.End != "refused" || r.Detail != "offline" {
		t.Fatalf("Ergebnis %+v, erwartet refused/offline", r)
	}
}

// Die Abgleichsebene bleibt unverändert (#1391): Ein 5xx beim Paketabruf trifft nur diesen Pin und hält weder
// Entzug noch die übrigen Pins auf; nur Netzfehler, Widerruf und Abbruch verwerfen den ganzen Abgleich.
func TestManifestEbeneZaehltEinen5xxNichtAlsNetzausfall(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"Netzfehler", context.Background(), &net.OpError{Op: "dial", Err: errors.New("x")}, true},
		{"Widerruf", context.Background(), ErrRevoked, true},
		{"Abbruch", cancelled, ErrHash, true},
		{"500", context.Background(), &HTTPError{Status: 500}, false},
		{"503", context.Background(), &HTTPError{Status: 503}, false},
		{"404", context.Background(), &HTTPError{Status: 404}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := manifestLevel(tc.ctx, tc.err); got != tc.want {
				t.Fatalf("manifestLevel = %v, erwartet %v", got, tc.want)
			}
		})
	}
}
