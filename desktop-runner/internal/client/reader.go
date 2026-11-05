package client

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Reader liest den Zustand eines laufenden Läufers, ohne ihn zu verändern (SEC K6b-2 B3). Das Bedienfeld
// hält damit Zugangsdaten und Apps, nie die Engine, nie einen Schreibweg und nie den Widerruf; was der
// Läufer löscht oder erneuert, bleibt allein seine Sache. Jeder Weg ist ein reines Lesen (TestReaderOhneSchreibendeAufrufe).
type Reader struct {
	root   string
	cipher Cipher
}

// AppsView ist, was das Bedienfeld über Apps wissen darf: geprüfte Manifesteinträge,
// ihre Sequenz (gegen veraltete Anzeige beim asynchronen Öffnen) und das Testgerät-Signal.
type AppsView struct {
	Apps        []App
	ManifestSeq uint64
	TestDevice  bool
}

// NewReader verlangt eine absolute, bereinigte Wurzel.
func NewReader(root string, c Cipher) (*Reader, error) {
	if c == nil || root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Dir(root) == root {
		return nil, ErrProtocol
	}
	return &Reader{root: root, cipher: c}, nil
}

// Load ist State.Load ohne Sperre, Widerrufsmerker und Seiteneffekt: reguläre Datei, begrenzte Größe, entschlüsseln.
func (r *Reader) Load(name string, v any) error {
	b, err := r.plain(name)
	if err != nil {
		return err
	}
	return strictUndLoeschen(b, v)
}

// strictUndLoeschen dekodiert streng und nullt den Klartext-Puffer danach, auch im Fehlerfall: Der Klartext von
// credential trägt das Gerätetoken und soll nicht länger im Speicher liegen als nötig.
func strictUndLoeschen(b []byte, v any) error {
	defer clear(b)
	return strict(b, v)
}

func (r *Reader) plain(name string) ([]byte, error) {
	if !safeName(name) {
		return nil, ErrProtocol
	}
	p := filepath.Join(r.root, name)
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 20<<20 {
		return nil, ErrProtocol
	}
	var raw []byte
	if reader, ok := r.cipher.(interface{ ReadFile(string) ([]byte, error) }); ok {
		raw, err = reader.ReadFile(p)
	} else {
		raw, err = os.ReadFile(p)
	}
	if err != nil {
		return nil, err
	}
	return r.cipher.Open(raw)
}

// Credential liest die Zugangsdaten einschließlich Gerätetoken (B4: erst beim Öffnen einer App).
func (r *Reader) Credential() (Credential, error) {
	var c Credential
	err := r.Load("credential", &c)
	return c, err
}

// identity liest nur, was die Manifestprüfung braucht. Die ganze Datei credential samt Token wird dafür entschlüsselt
// (alle 15 s), aber das Token wird nie in ein Go-Feld dekodiert, und der Klartext-Puffer wird nach dem Dekodieren genullt.
func (r *Reader) identity() (id struct {
	DeviceID string
	Pubkey   []byte
}, err error) {
	b, err := r.plain("credential")
	if err != nil {
		return id, err
	}
	defer clear(b)
	var v struct {
		DeviceID string `json:"device_id"`
		Pubkey   []byte `json:"pubkey"`
	}
	if json.Unmarshal(b, &v) != nil || v.DeviceID == "" || len(v.Pubkey) == 0 {
		return id, ErrProtocol
	}
	id.DeviceID, id.Pubkey = v.DeviceID, v.Pubkey
	return id, nil
}

// Apps liest das zuletzt vom Läufer gespeicherte Manifest und prüft es wie der Läufer: Signatur, Gerät,
// Vertrag 5. Eine Fassung, die der Läufer nicht angenommen hätte, liefert keine Apps.
func (r *Reader) Apps() (AppsView, error) {
	id, err := r.identity()
	if err != nil {
		return AppsView{}, err
	}
	var bundle Bundle
	if err = r.Load("manifest", &bundle); err != nil {
		return AppsView{}, err
	}
	m, err := VerifyManifest(bundle.Manifest.Envelope, id.Pubkey, id.DeviceID, 0)
	if err != nil || m.Contract != WindowsContract || m.Apps == nil {
		return AppsView{}, ErrManifest
	}
	return AppsView{Apps: append([]App(nil), *m.Apps...), ManifestSeq: m.Seq, TestDevice: m.TestDevice}, nil
}

// OpenApp prüft das Siegel und liefert den Ordner; derselbe Weg wie AppStore.Open.
func (r *Reader) OpenApp(app App) (string, error) { return openApp(r.root, r, app) }
