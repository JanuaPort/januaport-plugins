package client

import (
	"sync"
	"testing"
	"time"
)

// #1421: Feste Sperr-Reihenfolge e.mu vor State.mu. Unter State.mu (State.Use) darf nie e.mu genommen werden, sonst
// hängt der Läufer im AB/BA-Deadlock, sobald der Takt (pruneStored) auf eine Pause/Halt-Umschaltung trifft.

const sperrFrist = 10 * time.Second

// warteBis meldet den Test als gescheitert, wenn die Goroutinen nicht binnen der Frist fertig sind.
func warteBis(t *testing.T, wg *sync.WaitGroup, was string) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(sperrFrist):
		t.Fatalf("%s: nicht binnen %s fertig, Sperr-Reihenfolge verletzt (Deadlock)", was, sperrFrist)
	}
}

// Deterministisch: Der Takt hält die Zustandssperre und wartet im Hook, bis die Pause e.mu hält und an der
// Zustandssperre hängt. Erst dann liest der Takt weiter. Nimmt er dabei e.mu, hängen beide für immer.
func TestTaktUnterZustandssperreNimmtNieDieEngineSperre(t *testing.T) {
	b := zweiAppsAbgelegt(t)
	inside := make(chan struct{})
	b.e.pruneHook = func() {
		close(inside)
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
			if !b.e.mu.TryLock() {
				return // ein anderer hält e.mu
			}
			b.e.mu.Unlock()
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); b.e.pruneStored() }()
	<-inside
	go func() { defer wg.Done(); _ = b.e.Pause(15) }()
	warteBis(t, &wg, "pruneStored gegen Pause")
	if state, _, fault := b.e.HaltStatus(); state != "pausiert" || fault {
		t.Fatalf("Halt: %q fault=%v", state, fault)
	}
}

// Belastung ohne Hook: Takt und Pause/Fortsetzen laufen gegeneinander. Zusatz, kein Rot-Beweis: Das Zeitfenster der
// Kollision ist winzig, den Deadlock belegt der Hook-Test. Zeitbegrenzt statt nach Rundenzahl (jede
// Pause schreibt mit fsync, auf langsamen Platten dauert eine feste Rundenzahl beliebig lange).
func TestTaktGegenPauseFortsetzenBleibtLebendig(t *testing.T) {
	b := zweiAppsAbgelegt(t)
	ende := time.Now().Add(2 * time.Second)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for time.Now().Before(ende) {
			b.e.pruneStored()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; time.Now().Before(ende); i++ {
			_ = b.e.Pause([]int{15, 0}[i%2])
		}
	}()
	go func() {
		defer wg.Done()
		for time.Now().Before(ende) {
			b.e.HaltStatus()
		}
	}()
	warteBis(t, &wg, "Takt gegen Pause/Fortsetzen")
}

// Der Takt liest das gespeicherte Manifest (Platte), nicht den Speicherstand: Sync speichert zuerst und setzt e.bundle
// danach, die Platte ist also immer gleich neu oder neuer.
func TestTaktNimmtDasGespeicherteManifestVonDerPlatte(t *testing.T) {
	for _, tc := range []struct {
		name         string
		platte       func(b *pruneBed) []App
		speicher     func(b *pruneBed) []App
		zweiteBleibt bool
	}{
		{"Platte neuer: App aus neuem Manifest bleibt", func(b *pruneBed) []App { return []App{b.demo, b.zweite} },
			func(b *pruneBed) []App { return []App{b.demo} }, true},
		{"Platte entzieht: App wird gelöscht", func(b *pruneBed) []App { return []App{b.demo} },
			func(b *pruneBed) []App { return []App{b.demo, b.zweite} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := zweiAppsAbgelegt(t)
			b.e.mu.Lock()
			b.e.bundle.Manifest.Envelope = b.huelle(2, tc.speicher(b)...)
			b.e.mu.Unlock()
			plattenStand := b.e.view()
			plattenStand.Manifest.Envelope = b.huelle(3, tc.platte(b)...)
			if err := b.st.Save("manifest", plattenStand); err != nil {
				t.Fatal(err)
			}
			b.e.pruneStored()
			if got := b.exists("apps", "zweite", b.zweite.Commit, "index.html"); got != tc.zweiteBleibt {
				t.Fatalf("App zweite vorhanden: %v, erwartet %v", got, tc.zweiteBleibt)
			}
		})
	}
}
