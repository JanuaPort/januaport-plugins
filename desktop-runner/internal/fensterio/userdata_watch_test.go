package fensterio

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUserDataDir(t *testing.T) {
	root := t.TempDir()
	got, err := UserDataDir(root, "kasse_2")
	if err != nil || got != filepath.Join(root, "webview", "kasse_2") {
		t.Fatalf("%q %v", got, err)
	}
	for _, app := range []string{"", ".", "..", "../x", "a/b", `a\b`, "Kasse", "a-b", "a b", "ä", "x\x00"} {
		if d, err := UserDataDir(root, app); err == nil {
			t.Errorf("Name %q ergibt %q", app, d)
		}
	}
	for _, r := range []string{"", ".", "relativ", `\\server\share`} {
		if d, err := UserDataDir(r, "kasse"); err == nil && !filepath.IsAbs(r) {
			t.Errorf("Wurzel %q ergibt %q", r, d)
		}
	}
}

// Das Benutzerdatenverzeichnis liegt je App getrennt unter der Zustandswurzel: Der Widerruf des Läufers
// löscht es mit dem Zustand (ohne Verweisen zu folgen, siehe client.TestRevokeLoeschtWebview…).
func TestUserDataDirJeAppGetrennt(t *testing.T) {
	root := t.TempDir()
	a, _ := UserDataDir(root, "eins")
	b, _ := UserDataDir(root, "zwei")
	if a == b || filepath.Dir(a) != filepath.Dir(b) {
		t.Fatal(a, b)
	}
}

func TestWatchMeldetAenderungenNurEinmal(t *testing.T) {
	var mu sync.Mutex
	state := AppList{Names: []string{"a"}}
	var err error
	read := func() (AppList, error) { mu.Lock(); defer mu.Unlock(); return state, err }
	var got []AppList
	var gmu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Watch(ctx, 2*time.Millisecond, read, func(l AppList) { gmu.Lock(); got = append(got, l); gmu.Unlock() })
		close(done)
	}()
	waitFor(t, "erste Meldung", func() bool { gmu.Lock(); defer gmu.Unlock(); return len(got) == 1 })
	time.Sleep(20 * time.Millisecond)
	gmu.Lock()
	if len(got) != 1 {
		t.Fatalf("unveränderte Liste mehrfach gemeldet: %d", len(got))
	}
	gmu.Unlock()
	mu.Lock()
	state = AppList{Names: []string{"a", "b"}, TestDevice: true}
	mu.Unlock()
	waitFor(t, "Änderung", func() bool { gmu.Lock(); defer gmu.Unlock(); return len(got) == 2 })
	// Einzelne Lesefehler behalten den letzten Stand; erst anhaltende Fehler leeren die Liste.
	gmu.Lock()
	n := len(got)
	gmu.Unlock()
	mu.Lock()
	err = errors.New("kurz")
	mu.Unlock()
	waitFor(t, "anhaltender Fehler leert", func() bool {
		gmu.Lock()
		defer gmu.Unlock()
		return len(got) == n+1 && len(got[len(got)-1].Names) == 0
	})
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Watch endet nicht mit dem Kontext")
	}
}
