package client

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
)

// JanuaPort/januaport#1425: Bricht der Läufer zwischen dem Umbenennen der Umgebung nach envs/<key> und dem Speichern
// ihres Siegels (env-<key>) ab, liegt ein unfertiger eigener Stand ohne Siegel vor. Er wird ersetzt, nie wie
// Manipulation behandelt. Ein gespeichertes Siegel bleibt unverändert maßgeblich: Manipulation ergibt ErrHash.

const echtesPython = "python-3.12.10-embed-amd64.zip"

// echtePaketUmgebung baut einen Pin, dessen Paket Prepare wirklich übersteht: Die Allowlist wird für den Test auf ein
// synthetisches Embed-ZIP mit python.exe gesetzt (kein echtes Python nötig).
func (b *syncBed) echterPin(name string) Pin {
	b.t.Helper()
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	f, err := zw.Create("python.exe")
	if err != nil {
		b.t.Fatal(err)
	}
	if _, err = f.Write([]byte("x")); err != nil {
		b.t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		b.t.Fatal(err)
	}
	orig := pythonAllowlist
	b.t.Cleanup(func() { pythonAllowlist = orig })
	pythonAllowlist = []byte(`{"version":1,"entries":[{"version":"3.12.10","filename":"` + echtesPython + `","sha256":"` + sum(zipped.Bytes()) + `"}]}`)
	yaml := "name: " + name + "\ndescription: test\nentry: main.py\ninput_schema: {type: object}\ntools: []\ntimeout_s: 10\npython: " + echtesPython + "\n"
	commit, raw := testfixture.PackageAt(name, map[string][]byte{"jnpt-desktop.yaml": []byte(yaml), "main.py": []byte("pass"), echtesPython: zipped.Bytes()}, "")
	b.pkgs[commit] = raw
	return b.pin(name, commit)
}

func (b *syncBed) envDir(p Pin) string { return filepath.Join(b.st.Root, "envs", envKey(p)) }

// unfertigerStand legt den Zustand nach einem Abbruch an: Prepare lief (Verzeichnis liegt vor), das Siegel wurde
// nie gespeichert.
func (b *syncBed) unfertigerStand(p Pin) {
	b.t.Helper()
	if _, err := Prepare(filepath.Join(b.st.Root, "envs"), b.pkgs[p.Commit], p); err != nil {
		b.t.Fatal("Ausgangslage:", err)
	}
	if b.exists("env-" + envKey(p)) {
		b.t.Fatal("Ausgangslage: Siegel gespeichert")
	}
}

func TestAbbruchVorSiegelDerNaechsteAbgleichMachtDenPinBereit(t *testing.T) {
	b := newSyncBed(t)
	p := b.echterPin("echt")
	b.unfertigerStand(p)
	// Fremdes im unfertigen Verzeichnis darf nicht überleben: Dem alten Inhalt wird nie vertraut, er wird ersetzt.
	altlast := filepath.Join(b.envDir(p), "pin", "altlast.txt")
	if err := os.WriteFile(altlast, []byte("alt"), 0o600); err != nil {
		t.Fatal(err)
	}
	b.serve(b.manifest(1, nil, b.gut, p))
	if err := b.sync(); err != nil {
		t.Fatal("Abgleich:", err)
	}
	if !b.hatUmgebung(p) || !b.hatUmgebung(b.gut) {
		t.Fatalf("Umgebungen: echt %v, gut %v, runner.log %q", b.hatUmgebung(p), b.hatUmgebung(b.gut), b.log())
	}
	if b.log() != "" {
		t.Fatalf("runner.log = %q, erwartet leer", b.log())
	}
	var env Environment
	if err := b.st.Load("env-"+envKey(p), &env); err != nil {
		t.Fatal("Siegel nicht gespeichert:", err)
	}
	if err := VerifyEnvironment(b.envDir(p), env.Files); err != nil {
		t.Fatal("Umgebung passt nicht zum Siegel:", err)
	}
	if _, err := os.Lstat(altlast); err == nil {
		t.Fatal("Altlast aus dem unfertigen Stand blieb liegen")
	}
	if r := b.slot(p); r.End != "ok" || b.spawns.Load() != 1 {
		t.Fatalf("Lauf: %+v, Starts %d", r, b.spawns.Load())
	}
}

// Der Wiederanlauf lädt das Paket neu und prüft es vollständig, bevor das Verzeichnis ersetzt wird: Ein jetzt
// kaputtes Paket lässt den unfertigen Stand nicht als Umgebung durchgehen.
func TestAbbruchVorSiegelKaputtesPaketMachtKeineUmgebung(t *testing.T) {
	b := newSyncBed(t)
	p := b.echterPin("echt")
	b.unfertigerStand(p)
	b.pkgs[p.Commit] = []byte("kein Paket")
	b.serve(b.manifest(1, nil, b.gut, p))
	if err := b.sync(); err != nil {
		t.Fatal("Abgleich:", err)
	}
	if b.hatUmgebung(p) || b.exists("env-"+envKey(p)) {
		t.Fatal("Umgebung ohne gültiges Paket")
	}
	if b.log() != "2 1\n" {
		t.Fatalf("runner.log = %q", b.log())
	}
}

// Das unfertige Verzeichnis darf ein Verweis (Symlink, unter Windows Junction) sein: Er wird als Verweis entfernt, das Ziel bleibt unberührt.
func TestAbbruchVorSiegelEntferntVerweisNichtDessenZiel(t *testing.T) {
	b := newSyncBed(t)
	p := b.echterPin("echt")
	ziel := t.TempDir()
	wache := filepath.Join(ziel, "wache.txt")
	if err := os.WriteFile(wache, []byte("bleibt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(b.st.Root, "envs"), 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir(t, b.envDir(p), ziel)
	b.serve(b.manifest(1, nil, b.gut, p))
	if err := b.sync(); err != nil {
		t.Fatal("Abgleich:", err)
	}
	if raw, err := os.ReadFile(wache); err != nil || string(raw) != "bleibt" {
		t.Fatalf("Ziel des Verweises angetastet: %q %v", raw, err)
	}
	if !b.hatUmgebung(p) {
		t.Fatalf("Pin nicht bereit, runner.log %q", b.log())
	}
	if info, err := os.Lstat(b.envDir(p)); err != nil || !info.IsDir() {
		t.Fatalf("envs/<key> ist kein echtes Verzeichnis: %v %v", info, err)
	}
}

// Gegenprobe: Mit gespeichertem Siegel bleibt Manipulation ErrHash, es wird nie still repariert.
func TestManipulationMitSiegelBleibtErrHashUndWirdNichtRepariert(t *testing.T) {
	b := newSyncBed(t)
	p := b.echterPin("echt")
	b.serve(b.manifest(1, nil, b.gut, p))
	if err := b.sync(); err != nil || !b.hatUmgebung(p) {
		t.Fatalf("Ausgangslage: %v, runner.log %q", err, b.log())
	}
	manipuliert := filepath.Join(b.envDir(p), "pin", "main.py")
	if err := os.WriteFile(manipuliert, []byte("import os"), 0o600); err != nil {
		t.Fatal(err)
	}
	fetched := b.fetched.Load()
	b.serve(b.manifest(2, nil, b.gut, p))
	if err := b.sync(); err != nil {
		t.Fatal("Abgleich:", err)
	}
	if b.hatUmgebung(p) {
		t.Fatal("manipulierte Umgebung gilt als bereit")
	}
	if b.log() != "2 1\n" {
		t.Fatalf("runner.log = %q, erwartet Klasse hash", b.log())
	}
	if raw, err := os.ReadFile(manipuliert); err != nil || string(raw) != "import os" {
		t.Fatalf("Verzeichnis wurde repariert oder entfernt: %q %v", raw, err)
	}
	if b.fetched.Load() != fetched {
		t.Fatal("Paket neu geholt, obwohl ein Siegel gespeichert ist")
	}
	// Prepare selbst ersetzt nie ein vorhandenes Verzeichnis.
	if _, err := Prepare(filepath.Join(b.st.Root, "envs"), b.pkgs[p.Commit], p); err != ErrHash {
		t.Fatalf("Prepare auf vorhandenem Verzeichnis: %v, erwartet ErrHash", err)
	}
}
