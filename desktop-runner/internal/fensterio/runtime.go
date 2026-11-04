package fensterio

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

// ErrOpen ist der einzige Fehler beim Öffnen einer App; die Ursache bleibt absichtlich unsichtbar.
var ErrOpen = errors.New("app_nicht_verfuegbar")

// Cipher ist die Entschlüsselung des Läuferzustands (Windows: DPAPI desselben Benutzers).
type Cipher interface {
	Seal([]byte) ([]byte, error)
	Open([]byte) ([]byte, error)
}

// UserDataDir ist das Benutzerdatenverzeichnis der WebView einer App: <Zustandswurzel>\webview\<name>.
// Es liegt unter der Wurzel, damit der Widerruf des Läufers es mit dem Zustand löscht, ohne Verweisen zu
// folgen (State.Revoke/removeTree). Je App ein eigenes Verzeichnis.
func UserDataDir(root, app string) (string, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || !fenster.ValidAppName(app) {
		return "", ErrOpen
	}
	return filepath.Join(root, "webview", app), nil
}

// RuntimeConfig verdrahtet das Fenster mit dem Zustand des Läufers und der vorhandenen Bedienfeld-Verbindung.
type RuntimeConfig struct {
	Root     string
	Cipher   Cipher
	Exchange Exchange
	Wake     func()
	Frist    time.Duration
}

// Runtime ist alles, was das Bedienfeld außerhalb des UI-Threads über Apps wissen und tun darf. Es liest
// (client.Reader), schreibt nie in den Zustand und löscht bei einem Widerruf nichts.
type Runtime struct {
	cfg    RuntimeConfig
	reader *client.Reader
}

// AppList sind die Apps des zuletzt geprüften Manifests und das Testgerät-Signal. Kein Token, kein Pfad.
type AppList struct {
	Names      []string
	TestDevice bool
}

func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	if cfg.Cipher == nil || cfg.Exchange == nil {
		return nil, ErrOpen
	}
	r, err := client.NewReader(cfg.Root, cfg.Cipher)
	if err != nil {
		return nil, ErrOpen
	}
	return &Runtime{cfg: cfg, reader: r}, nil
}

// Apps liest und prüft das Manifest (Signatur, Gerät, Vertrag 5). Das Gerätetoken wird dabei nicht gelesen.
func (r *Runtime) Apps() (AppList, error) {
	v, err := r.reader.Apps()
	if err != nil {
		return AppList{}, ErrOpen
	}
	l := AppList{TestDevice: v.TestDevice}
	for _, a := range v.Apps {
		l.Names = append(l.Names, a.Name)
	}
	return l, nil
}

// Opened ist eine geöffnete App: geprüfter Ordner, Benutzerdatenordner und der Host mit Gateway und Brücke.
type Opened struct {
	Name        string
	Dir         string
	UserDataDir string
	TestDevice  bool
	Host        *Host
	api         *client.API
}

// Close beendet den Host und gibt die Verbindungen frei. Mehrfaches Aufrufen ist harmlos.
func (o *Opened) Close() {
	o.Host.Close()
	o.api.Close()
}

// Open prüft die App gegen das aktuelle, signierte Manifest und ihr Siegel, liest erst danach die
// Zugangsdaten und baut Gateway und Host. Bei „revoked“ antwortet das Gateway mit einem festen Fehler;
// gelöscht wird nichts (SEC K6b-2 B3). Das blockiert (Datei- und Entschlüsselungsarbeit): nie auf dem UI-Thread.
func (r *Runtime) Open(name string) (*Opened, error) {
	if !fenster.ValidAppName(name) {
		return nil, ErrOpen
	}
	view, err := r.reader.Apps()
	if err != nil {
		return nil, ErrOpen
	}
	i := slices.IndexFunc(view.Apps, func(a client.App) bool { return a.Name == name })
	if i < 0 {
		return nil, ErrOpen
	}
	dir, err := r.reader.OpenApp(view.Apps[i])
	if err != nil {
		return nil, ErrOpen
	}
	userData, err := UserDataDir(r.cfg.Root, name)
	if err != nil {
		return nil, err
	}
	cred, err := r.reader.Credential()
	if err != nil {
		return nil, ErrOpen
	}
	// onRevoke löscht nichts: Der Widerruf gehört dem Läufer.
	api, err := client.NewAPI(cred.Trust, cred.Token, func() error { return nil })
	if err != nil {
		return nil, ErrOpen
	}
	host, err := NewHost(HostConfig{App: name, Gateway: NewGateway(api, name), Lokal: NewLokal(r.cfg.Exchange), Wake: r.cfg.Wake, Frist: r.cfg.Frist})
	if err != nil {
		api.Close()
		return nil, ErrOpen
	}
	return &Opened{Name: name, Dir: dir, UserDataDir: userData, TestDevice: view.TestDevice, Host: host, api: api}, nil
}

// OpenAsync öffnet in einer eigenen Goroutine und ruft done von dort auf. Der UI-Thread kehrt sofort zurück.
func (r *Runtime) OpenAsync(name string, done func(*Opened, error)) {
	go func() {
		o, err := r.Open(name)
		done(o, err)
	}()
}

// Watch meldet die App-Liste beim Start und bei jeder Änderung. Einzelne Lesefehler (etwa mitten in einem
// Sync des Läufers) behalten den letzten Stand; erst drei Fehler in Folge leeren die Liste.
func (r *Runtime) Watch(ctx context.Context, interval time.Duration, emit func(AppList)) {
	Watch(ctx, interval, r.Apps, emit)
}

// Watch ist die Schleife hinter Runtime.Watch.
func Watch(ctx context.Context, interval time.Duration, read func() (AppList, error), emit func(AppList)) {
	var last AppList
	have := false
	failures := 0
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		l, err := read()
		switch {
		case err == nil:
			failures = 0
			if !have || !slices.Equal(l.Names, last.Names) || l.TestDevice != last.TestDevice {
				last, have = l, true
				emit(l)
			}
		default:
			failures++
			if failures == 3 && (!have || len(last.Names) != 0) {
				last, have = AppList{}, true
				emit(last)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
