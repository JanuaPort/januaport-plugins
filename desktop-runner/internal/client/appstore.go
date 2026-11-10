package client

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

// AppStore legt App-Pakete (Vertrag 5) privat unter <Zustand>/apps/<name>/<commit>/ ab, versiegelt sie
// und prüft das Siegel vor jedem Öffnen. Das Siegel liegt im verschlüsselten Zustand, nicht neben den
// Dateien; der Widerruf löscht Ablage und Siegel mit dem gesamten Zustand, der Entzug einer App räumt
// pruneApps ab (appprune.go). Nur der Läufer schreibt;
// das Bedienfeld liest über Reader.OpenApp denselben Prüfweg (openApp).
type AppStore struct{ state *State }

func NewAppStore(s *State) *AppStore { return &AppStore{state: s} }

type appSeal struct {
	Name   string     `json:"name"`
	Commit string     `json:"commit"`
	Files  []FileSeal `json:"files"`
}

func validAppEntry(app App) bool {
	return fenster.ValidAppName(app.Name) && validID("sha256", app.Commit) && app.Path == "apps/"+app.Name
}

func sealName(app App) string { return "app-" + app.Name + "-" + app.Commit }

func (s *AppStore) dir(app App) string {
	return filepath.Join(s.state.Root, "apps", app.Name, app.Commit)
}

// sealSource ist das Lesen der Siegel; State und Reader erfüllen es.
type sealSource interface {
	Load(name string, v any) error
}

// Open prüft das Siegel und liefert erst dann den Ordner für das Fenster.
func (s *AppStore) Open(app App) (string, error) { return openApp(s.state.Root, s.state, app) }

// openApp ist der gemeinsame, rein lesende Prüfweg von Läufer und Bedienfeld.
func openApp(root string, seals sealSource, app App) (string, error) {
	if !validAppEntry(app) {
		return "", ErrHash
	}
	var seal appSeal
	if err := seals.Load(sealName(app), &seal); err != nil || seal.Name != app.Name || seal.Commit != app.Commit {
		return "", ErrHash
	}
	dir := filepath.Join(root, "apps", app.Name, app.Commit)
	for _, p := range []string{filepath.Dir(dir), dir} {
		if info, err := os.Lstat(p); err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return "", ErrHash
		}
	}
	if err := VerifyEnvironment(dir, seal.Files); err != nil {
		return "", ErrHash
	}
	// Auch ein in sich stimmiges Siegel genügt nicht: Das Literal muss in jeder HTML-Datei vorn stehen.
	for _, f := range seal.Files {
		if path.Ext(f.Path) != ".html" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil || !leadingCSP(b) {
			return "", ErrHash
		}
	}
	return dir, nil
}

// Ensure legt die App ab, falls sie fehlt. Eine vorhandene, aber nicht mehr zum Siegel passende
// Ablage wird nie stillschweigend repariert oder überschrieben.
func (s *AppStore) Ensure(ctx context.Context, api *API, app App) error {
	if !validAppEntry(app) {
		return ErrHash
	}
	if _, err := s.Open(app); err == nil {
		return nil
	}
	if _, err := os.Lstat(s.dir(app)); err == nil {
		return ErrHash
	}
	raw, err := api.Package(ctx, Pin{Algo: "sha256", Commit: app.Commit, Path: app.Path})
	if err != nil {
		return err
	}
	return s.Prepare(raw, app)
}

// Prepare prüft das Paket vollständig und legt es erst danach ab: Staging im eigenen Ordner, Siegel
// speichern, dann ein einziges Umbenennen. Alle Schreibschritte laufen unter der Zustandssperre
// (State.Use) und sind damit gegen den Widerruf serialisiert: Nach dem Widerruf entsteht kein Rest.
// Jeder Fehler hinterlässt weder Ablage noch Reste.
func (s *AppStore) Prepare(raw []byte, app App) error {
	files, err := loadAppPackage(raw, app)
	if err != nil {
		return ErrHash
	}
	return s.state.Use(func() error {
		base := filepath.Join(s.state.Root, "apps", app.Name)
		if err := os.MkdirAll(base, 0o700); err != nil {
			return err
		}
		stage, err := os.MkdirTemp(base, ".prepare-")
		if err != nil {
			return err
		}
		defer func() { _ = removeTree(stage) }()
		for rel, data := range files {
			dst := filepath.Join(stage, filepath.FromSlash(rel))
			if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			if err = writeNew(dst, data); err != nil {
				return err
			}
		}
		seal, err := Snapshot(stage)
		if err != nil {
			return err
		}
		dest := s.dir(app)
		if _, err = os.Lstat(dest); err == nil {
			return ErrHash
		}
		if err = s.state.saveLocked(sealName(app), appSeal{Name: app.Name, Commit: app.Commit, Files: seal}); err != nil {
			return err
		}
		return os.Rename(stage, dest)
	})
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	for _, e := range []error{werr, serr, cerr} {
		if e != nil {
			return e
		}
	}
	return nil
}
