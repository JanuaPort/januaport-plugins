package client

import (
	"context"
	"crypto/ed25519"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #1537 Punkt 1: Nach einem erfolgreichen, geprüften Sync räumt der Läufer Umgebungen (envs\<key>), Pakete
// (packages\<key>) und Siegel (env-<key>) ab, deren Schlüssel nicht im geprüften Manifest steht. Alle Tests laufen
// über Engine.Sync gegen den Fake-Kern von syncBed.

// vorrat legt für die Pins eine versiegelte Umgebung samt Paket ab (wie nach früheren Syncs).
func (b *syncBed) vorrat(namen ...string) []Pin {
	b.t.Helper()
	var pins []Pin
	for _, n := range namen {
		p := b.pinMitPaket(n)
		b.seedEnv(p, nil)
		pins = append(pins, p)
	}
	return pins
}

// hatAlles prüft Umgebung, Paket und Siegel eines Pins auf der Platte.
func (b *syncBed) hatAlles(p Pin) bool {
	k := envKey(p)
	return b.exists("envs", k) && b.exists("packages", k) && b.exists("env-"+k)
}

// hatNichts: keine der drei Ablagen des Pins ist übrig.
func (b *syncBed) hatNichts(p Pin) bool {
	k := envKey(p)
	return !b.exists("envs", k) && !b.exists("packages", k) && !b.exists("env-"+k)
}

// Hauptfall: Vorrat A+B+C, Manifest nur A. B und C sind vollständig weg, A bleibt und ist bereit.
func TestSyncRaeumtUmgebungenAusserhalbDesManifestsAb(t *testing.T) {
	b := newSyncBed(t)
	a := b.gut
	bc := b.vorrat("bbb", "ccc")
	if !b.hatAlles(a) || !b.hatAlles(bc[0]) || !b.hatAlles(bc[1]) {
		t.Fatal("Ausgangslage: Vorrat unvollständig")
	}
	b.serve(b.manifest(1, nil, a))
	if err := b.sync(); err != nil {
		t.Fatal("Sync:", err)
	}
	if !b.hatAlles(a) || !b.hatUmgebung(a) {
		t.Error("A (im Manifest) wurde angetastet")
	}
	for _, p := range bc {
		if !b.hatNichts(p) {
			t.Errorf("%s: Umgebung, Paket oder Siegel blieb liegen", p.Pin)
		}
	}
	if b.log() != "" {
		t.Errorf("runner.log = %q, erwartet leer", b.log())
	}
}

// Nur gültige Schlüssel werden angefasst (wie die App-Namen beim Entzug): Fremdes, Halbfertiges und die übrigen
// Zustandsdateien bleiben unberührt.
func TestSyncAbraeumenLoeschtNurGueltigeSchluessel(t *testing.T) {
	b := newSyncBed(t)
	b.vorrat("bbb")
	fremd := []string{
		filepath.Join("envs", ".prepare-123", "datei"),
		filepath.Join("envs", "fremd", "datei"),
		filepath.Join("envs", "ABC-"+strings.Repeat("a", 64), "datei"), // Großbuchstaben: kein gültiger Schlüssel
		filepath.Join("packages", "fremd.txt"),
		"env-fremd",
		"env-" + strings.Repeat("a", 40) + "-kurz",
		"test-12345678-1234-4234-8234-123456789abc",
	}
	for _, f := range fremd {
		p := filepath.Join(b.st.Root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b.serve(b.manifest(1, nil, b.gut))
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	for _, f := range fremd {
		if !b.exists(f) {
			t.Errorf("%s wurde gelöscht", f)
		}
	}
	if !b.exists("manifest") {
		t.Error("Zustandsdatei manifest fehlt")
	}
}

// Kein Abräumen ohne erfolgreichen, geprüften Sync: Offline-Fehler beim Paketabruf, falsche Signatur und alte
// Sequenz verwerfen den Abgleich; der Vorrat bleibt vollständig.
func TestKeinAbraeumenBeiGescheitertemSync(t *testing.T) {
	cases := []struct {
		name string
		prep func(b *syncBed, bc []Pin)
	}{
		{"offline beim Paketabruf", func(b *syncBed, bc []Pin) {
			neu := b.pinMitPaket("neu")
			b.pkgMode.Store("offline")
			b.serve(b.manifest(2, nil, b.gut, neu))
		}},
		{"falsche Signatur", func(b *syncBed, bc []Pin) {
			_, other, _ := ed25519.GenerateKey(nil)
			b.serveRaw(encode(b.manifest(2, nil, b.gut)), other)
		}},
		{"alte Sequenz", func(b *syncBed, bc []Pin) {
			b.serve(b.manifest(1, nil, b.gut))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newSyncBed(t)
			bc := b.vorrat("bbb", "ccc")
			b.serve(b.manifest(1, nil, b.gut, bc[0], bc[1]))
			if err := b.sync(); err != nil {
				t.Fatal("Ausgangslage:", err)
			}
			tc.prep(b, bc)
			if err := b.sync(); err == nil {
				t.Fatal("Sync gelang, erwartet Fehler")
			}
			for _, p := range append([]Pin{b.gut}, bc...) {
				if !b.hatAlles(p) {
					t.Errorf("%s: Ablage wurde nach gescheitertem Sync angetastet", p.Pin)
				}
			}
		})
	}
}

// Der Schlüssel des gerade laufenden Jobs wird nie gelöscht, auch wenn sein Pin im neuen Manifest fehlt. Erst nach
// dem Lauf räumt der nächste Sync ab.
func TestSyncLaesstUmgebungDesLaufendenJobsStehen(t *testing.T) {
	b := newSyncBed(t)
	pb := b.vorrat("bbb")[0]
	b.serve(b.manifest(1, nil, b.gut, pb))
	if err := b.sync(); err != nil {
		t.Fatal("Ausgangslage:", err)
	}
	laeuft, ende := make(chan struct{}), make(chan struct{})
	b.e.Spawn = func(context.Context, Launch) (Process, error) {
		r, w := io.Pipe()
		go func() {
			<-ende
			_ = WriteFrame(w, map[string]any{"type": "exited", "code": 0})
		}()
		close(laeuft)
		return &fakeProc{r: r, w: w}, nil
	}
	fertig := make(chan error, 1)
	go func() {
		fertig <- b.e.scheduled(context.Background(), pb, pb.Schedules[0], time.Now().UTC().Truncate(time.Minute))
	}()
	select {
	case <-laeuft:
	case <-time.After(10 * time.Second):
		t.Fatal("Job startet nicht")
	}
	b.serve(b.manifest(2, nil, b.gut))
	if err := b.sync(); err != nil {
		t.Fatal("Sync während des Laufs:", err)
	}
	if !b.hatAlles(pb) {
		t.Error("Umgebung des laufenden Jobs wurde gelöscht")
	}
	if !b.hatAlles(b.gut) {
		t.Error("A wurde angetastet")
	}
	close(ende)
	select {
	case err := <-fertig:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Job endet nicht")
	}
	b.serve(b.manifest(3, nil, b.gut))
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	if !b.hatNichts(pb) {
		t.Error("nach dem Lauf blieb die Umgebung liegen")
	}
}

// Sperre statt Verlust: Eine gesperrte Umgebung bleibt liegen, der Sync scheitert nicht (der Pin-Schritt und alle
// anderen Pins laufen weiter), im Log steht nur der feste Code 10 2. Der nächste Sync wiederholt das Abräumen.
func TestGesperrteUmgebungBleibtUndWirdBeimNaechstenSyncGeloescht(t *testing.T) {
	b := newSyncBed(t)
	pb := b.vorrat("bbb")[0]
	freigeben := sperreOrdner(t, filepath.Join(b.st.Root, "envs", envKey(pb)))
	b.serve(b.manifest(1, nil, b.gut))
	if err := b.sync(); err != nil {
		t.Fatal("Sync trotz Sperre:", err)
	}
	if !b.exists("envs", envKey(pb)) {
		t.Fatal("gesperrte Umgebung verschwand")
	}
	if b.log() != "10 2\n" {
		t.Errorf("Log: %q, erwartet genau den festen Code 10 2", b.log())
	}
	if !b.hatAlles(b.gut) {
		t.Error("A wurde angetastet")
	}
	freigeben()
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	if !b.hatNichts(pb) {
		t.Error("nach der Entsperrung blieb die Umgebung liegen")
	}
}

// Kein Verweis wird verfolgt: Ist der Eintrag selbst ein Verweis, verliert nur der Verweis, nie sein Ziel.
func TestVerweisUnterEnvsWirdNichtVerfolgt(t *testing.T) {
	b := newSyncBed(t)
	pb := b.vorrat("bbb")[0]
	canary := filepath.Join(t.TempDir(), "ziel", "wichtig.txt")
	if err := os.MkdirAll(filepath.Dir(canary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canary, []byte("bleibt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(b.st.Root, "envs", envKey(pb))); err != nil {
		t.Fatal(err)
	}
	linkDir(t, filepath.Join(b.st.Root, "envs", envKey(pb)), filepath.Dir(canary))
	b.serve(b.manifest(1, nil, b.gut))
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(canary); err != nil || string(got) != "bleibt" {
		t.Fatalf("Ziel außerhalb angetastet: %q %v", got, err)
	}
	if b.exists("envs", envKey(pb)) {
		t.Error("Verweis blieb liegen")
	}
}

// Ist envs\ oder packages\ selbst ein Verweis, wird darin nichts gelöscht und der Verweis bleibt.
func TestVerweisAlsEnvsOderPackagesWurzelBleibtUnberuehrt(t *testing.T) {
	for _, area := range []string{"envs", "packages"} {
		t.Run(area, func(t *testing.T) {
			b := newSyncBed(t)
			b.vorrat("bbb")
			canary := filepath.Join(t.TempDir(), "ziel")
			if err := os.MkdirAll(filepath.Join(canary, strings.Repeat("a", 40)+"-"+strings.Repeat("b", 64)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(b.st.Root, area)); err != nil {
				t.Fatal(err)
			}
			linkDir(t, filepath.Join(b.st.Root, area), canary)
			b.serve(b.manifest(1, nil))
			if err := b.sync(); err != nil {
				t.Fatal("Sync:", err)
			}
			if _, err := os.Stat(filepath.Join(canary, strings.Repeat("a", 40)+"-"+strings.Repeat("b", 64))); err != nil {
				t.Errorf("Ziel hinter %s angetastet: %v", area, err)
			}
			if _, err := os.Lstat(filepath.Join(b.st.Root, area)); err != nil {
				t.Errorf("Verweis %s verschwand: %v", area, err)
			}
		})
	}
}

// Struktur wie beim App-Entzug: unter der Zustandssperre, nie os.RemoveAll (Junctions), nur removeTree.
func TestUmgebungAufraeumenLaeuftUnterZustandssperreOhneRemoveAll(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "envprune.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var uses, removeAll, removeTreeCalls int
	for _, d := range f.Decls {
		ast.Inspect(d, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "Use":
					uses++
				case "RemoveAll":
					removeAll++
				}
			}
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "removeTree" {
					removeTreeCalls++
				}
			}
			return true
		})
	}
	if uses == 0 || removeAll != 0 || removeTreeCalls == 0 {
		t.Fatalf("State.Use: %d, os.RemoveAll: %d, removeTree: %d", uses, removeAll, removeTreeCalls)
	}
}

// Ein Pin ohne Umgebung (kaputtes Paket) steht im Manifest: Sein Schlüssel zählt als zugewiesen, nichts wird
// gelöscht, was zu ihm gehört. Ein früherer Commit desselben Pins (anderer Schlüssel) ist dagegen entzogen.
func TestAbraeumenZaehltAuchPinsOhneUmgebungAlsZugewiesen(t *testing.T) {
	b := newSyncBed(t)
	alt := b.vorrat("bbb")[0]
	// Siegel und Ablage des neuen Commits liegen vor (Rest eines Abbruchs), der Pin selbst scheitert im Pin-Schritt.
	kaputt := b.pin("kaputt", strings.Repeat("b", 64))
	b.pkgs[kaputt.Commit] = []byte("kein Paket")
	rest := filepath.Join(b.st.Root, "envs", envKey(kaputt), "rest.txt")
	if err := os.MkdirAll(filepath.Dir(rest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rest, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	b.serve(b.manifest(1, nil, b.gut, kaputt))
	if err := b.sync(); err != nil {
		t.Fatal(err)
	}
	if b.hatUmgebung(kaputt) {
		t.Fatal("Ausgangslage: kaputter Pin hat eine Umgebung")
	}
	if !b.exists("envs", envKey(kaputt), "rest.txt") {
		t.Error("Ablage eines Pins, der im Manifest steht, wurde gelöscht")
	}
	if !b.hatNichts(alt) {
		t.Error("entzogener Pin blieb liegen")
	}
}
