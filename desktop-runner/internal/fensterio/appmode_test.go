package fensterio

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

// Schreibt echte signierte Manifestbytes, auch für ungültige und alte Kernstände.
func saveAppMode(t *testing.T, b *runtimeBed, mode *string, seq uint64) {
	t.Helper()
	b.bed.SaveManifest(t, b.bed.Apps, seq, true)
	var bundle client.Bundle
	if err := b.bed.St.Load("manifest", &bundle); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(bundle.Manifest.Envelope.Manifest, &raw); err != nil {
		t.Fatal(err)
	}
	app := raw["apps"].([]any)[0].(map[string]any)
	delete(app, "mode")
	if mode != nil {
		app["mode"] = *mode
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	can, err := client.Canonical(data)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Manifest.Envelope = client.SignedManifest{Manifest: data, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(b.bed.Key, can))}
	if err = b.bed.St.Save("manifest", bundle); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAppModeAusAktuellemManifest(t *testing.T) {
	b := newRuntimeBed(t)
	for i, mode := range []string{"a", "b", "", "a", "b"} {
		saveAppMode(t, b, &mode, uint64(i+2))
		o, err := b.rt.Open("demo")
		if err != nil {
			t.Fatal(err)
		}
		if o.Mode != mode || o.ManifestSeq != uint64(i+2) {
			t.Errorf("Modus/Sequenz veraltet: %q/%d", o.Mode, o.ManifestSeq)
		}
		// Dieselbe Brücke und derselbe echte HTTP-Adapter in beiden Modi.
		o.Host.Receive(fenster.Source{MainFrame: true, URL: fenster.StartURL("demo")}, `{"id":"1","befehl":"werkzeuge","args":{}}`)
		o.Host.Wait()
		if got := o.Host.Drain(); len(got) != 1 || !json.Valid(got[0]) || b.core.tokHits.Load() != int32(i+1) || b.core.mcpHits.Load() != int32(i+1) {
			t.Fatalf("Modus änderte den Token-/Brückenweg: %q", got)
		}
		o.Close()
	}
	bad := "c"
	saveAppMode(t, b, &bad, 10)
	if o, err := b.rt.Open("demo"); err == nil {
		o.Close()
		t.Fatal("ungültiger Modus am echten Reader angenommen")
	}
}

func TestRuntimeWatchMeldetModuswechsel(t *testing.T) {
	b := newRuntimeBed(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan AppList, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.rt.Watch(ctx, time.Millisecond, func(l AppList) { got <- l })
	}()
	defer func() { cancel(); <-done }()
	check := func(mode string, seq uint64) {
		t.Helper()
		select {
		case l := <-got:
			if len(l.Names) != 1 || l.Names[0] != "demo" || l.Modes["demo"] != mode || l.ManifestSeq != seq {
				t.Fatalf("Moduswechsel fehlt: %+v", l)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("nächstes Manifest ohne Modusmeldung")
		}
	}
	check("", 1) // alter Kern ohne mode
	for i, mode := range []string{"a", "b", "", "a"} {
		saveAppMode(t, b, &mode, uint64(i+2))
		check(mode, uint64(i+2))
	}
}

func TestAppModeNativVerdrahtet(t *testing.T) {
	// Die Wirkung an Win32 belegt zusätzlich die BOX-Sonde.
	for file, fragments := range map[string][]string{
		"webview_windows.go":                   {"s.o.ShowLoginBar()"},
		"login_surface_windows.go":             {"s.o.ShowLoginBar()", "func (s *Surface) UpdateApps("},
		"../../cmd/bedienfeld/apps_windows.go": {"s.UpdateApps(l)", "s.UpdateApps(a.apps)"},
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range fragments {
			if !strings.Contains(string(b), fragment) {
				t.Errorf("%s fehlt %s", file, fragment)
			}
		}
	}
}
