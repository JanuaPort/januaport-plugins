package client

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPaketObjektmengeModiUndNamen(t *testing.T) {
	for _, mode := range []string{"120000", "160000", "100664"} {
		commit, b := testfixture.Package(map[string][]byte{"main.py": []byte("pass")}, mode)
		if _, e := PackageFiles(b, Pin{Commit: commit, Algo: "sha256", Path: "demo"}); e != ErrHash {
			t.Fatal(mode, e)
		}
	}
	for _, name := range []string{"CON.py", "bad.", "bad ", "a:b.py", "AUX", "a\\b.py", "\x00.py", "\x01.py", ".."} {
		commit, b := testfixture.Package(map[string][]byte{name: []byte("pass")}, "")
		if _, e := PackageFiles(b, Pin{Commit: commit, Algo: "sha256", Path: "demo"}); e != ErrHash {
			t.Fatal(name, e)
		}
	}
	h, b := goldenPackage(t)
	n := int(binary.BigEndian.Uint32(b[:4]))
	frames := b[4+n:]
	for _, mode := range []string{"extra", "missing", "duplicate"} {
		hh := h
		tail := append([]byte(nil), frames...)
		if mode == "missing" {
			hh.Objects--
			size := int(binary.BigEndian.Uint32(tail[:4]))
			tail = tail[4+size:]
		} else {
			hh.Objects++
			var more []byte
			if mode == "extra" {
				more = []byte{0, 0, 0, 2, 'b', 'X'}
			} else {
				size := int(binary.BigEndian.Uint32(tail[:4]))
				more = tail[:4+size]
			}
			tail = append(tail, more...)
		}
		head := encode(hh)
		var stream bytes.Buffer
		binary.Write(&stream, binary.BigEndian, uint32(len(head)))
		stream.Write(head)
		stream.Write(tail)
		if _, e := PackageFiles(stream.Bytes(), Pin{Commit: h.Commit, Algo: h.Algo, Path: h.Path}); e != ErrHash {
			t.Fatal(mode, e)
		}
	}
	commit, b := testfixture.Package(map[string][]byte{"Main.py": []byte("a"), "main.py": []byte("b")}, "")
	if _, e := PackageFiles(b, Pin{Commit: commit, Algo: "sha256", Path: "demo"}); e != ErrHash {
		t.Fatal("case collision", e)
	}
	for _, algo := range []string{"sha1", "sha256"} {
		b, e := os.ReadFile("../../contract/golden/package_" + algo + ".bin")
		if e != nil {
			t.Fatal(e)
		}
		var h packageHead
		json.Unmarshal(b[4:4+binary.BigEndian.Uint32(b[:4])], &h)
		if _, e = PackageFiles(b, Pin{Algo: algo, Commit: h.Commit, Path: h.Path}); e != nil {
			t.Fatal("positive Kontrolle", e)
		}
	}
}
func TestStriktesJSONAmAdapter(t *testing.T) {
	for _, raw := range []string{`{"cancel":[],"manifest_seq_current":1,"idle":10}`, `{"cancel":null,"manifest_seq_current":1}`, `{"cancel":[],"manifest_seq_current":1,"manifest_seq_current":2}`, `{"cancel":[]}`, `{"cancel":[],"manifest_seq_current":1} {}`, `{"cancel":["\ud800"],"manifest_seq_current":1}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, raw)
		}))
		a, _ := NewAPI(Trust{URL: srv.URL}, "token", nil)
		_, e := a.Heartbeat(context.Background(), Heartbeat{1, true, "test", 0})
		a.Close()
		srv.Close()
		if e == nil {
			t.Fatal("Antwort akzeptiert", raw)
		}
	}
}
func TestCAFingerprintProxyUndURL(t *testing.T) {
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PairReply{"device", "jnpt_secret", "2027-01-01T00:00:00Z", base64.StdEncoding.EncodeToString(make([]byte, 32))})
	}))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	a, e := NewAPI(Trust{URL: srv.URL, CA: ca}, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if _, e = a.Pair(context.Background(), "0123456789abcdef.ABCD-EFGH-JKMN", "test"); e != nil {
		t.Fatal(e)
	}
	a, e = NewAPI(Trust{URL: srv.URL, Fingerprint: strings.Repeat("0", 64)}, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Pair(context.Background(), "0123456789abcdef.ABCD-EFGH-JKMN", "test"); e == nil || hits != 1 {
		t.Fatal("falscher Fingerprint")
	}
	a.Close()
	for _, u := range []string{"file:///test", "https://user:pass@example.org", "http://example.org", "http://127.0.0.1.evil", "https://example.org/?token=1", "https://example.org/other", "https://example.org/#x"} {
		if _, e = NewAPI(Trust{URL: u}, "", nil); e == nil {
			t.Fatal(u)
		}
	}
	if _, e = NewAPI(Trust{URL: srv.URL, CA: ca, Fingerprint: strings.Repeat("0", 64)}, "", nil); e == nil {
		t.Fatal("zwei Vertrauenswege")
	}
	proxyHits := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyHits++ }))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	a, _ = NewAPI(Trust{URL: srv.URL, CA: ca}, "", nil)
	defer a.Close()
	if _, e = a.Pair(context.Background(), "0123456789abcdef.ABCD-EFGH-JKMN", "test"); e != nil || proxyHits != 0 {
		t.Fatal("Umgebungsproxy", e)
	}
}
func TestWiderrufAnAllenHTTPWegen(t *testing.T) {
	for _, endpoint := range []string{"config", "manifest", "heartbeat", "jobs/claim", "packages/abc", "jobs/123/start", "runs/start", "mcp", "runs/result"} {
		t.Run(endpoint, func(t *testing.T) {
			st, _ := OpenState(t.TempDir(), testCipher{})
			st.Save("credential", map[string]string{"token": "secret"})
			os.Mkdir(filepath.Join(st.Root, "envs"), 0700)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(401)
				io.WriteString(w, `{"error":{"class":"revoked"}}`)
			}))
			defer srv.Close()
			a, _ := NewAPI(Trust{URL: srv.URL}, "secret", st.Revoke)
			defer a.Close()
			_, _, e := a.request(context.Background(), "POST", endpoint, nil, 0, "", 1000)
			if e != ErrRevoked {
				t.Fatal(e)
			}
			if _, e = os.Stat(st.Root); !os.IsNotExist(e) {
				t.Fatal("Bestand erhalten")
			}
			if e = st.Save("credential", map[string]string{"x": "y"}); e != ErrRevoked {
				t.Fatal("wiederbelebt", e)
			}
		})
	}
}
func TestManifestSyncAtomarUndOfflineAmAdapter(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	m := Manifest{Contract: 1, DeviceID: "own", Seq: 1, IssuedAt: "2026-10-23T10:00:00Z", GraceHours: 1, Pins: []Pin{}}
	signed := func(m Manifest) SignedManifest {
		raw := encode(m)
		can, _ := canonical(raw)
		return SignedManifest{raw, base64.StdEncoding.EncodeToString(ed25519.Sign(key, can))}
	}
	response := signed(m)
	cfg := Config{Contract: 1, ExpectedAccount: "own", HeartbeatS: 60, PollHoldS: 25}
	cfg.Device.ID = "own"
	cfg.Device.Kind = "workstation"
	cfg.StartPolicy.Mode = "always"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "config") {
			json.NewEncoder(w).Encode(cfg)
		} else {
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer srv.Close()
	st, _ := OpenState(t.TempDir(), testCipher{})
	a, _ := NewAPI(Trust{URL: srv.URL}, "token", st.Revoke)
	defer a.Close()
	e := Engine{State: st, API: a, Credential: Credential{DeviceID: "own", Pubkey: pub}}
	if err := e.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(st.Root, "manifest"))
	last := e.view().Manifest.LastSync
	for _, kind := range []string{"old", "foreign", "signature"} {
		bad := m
		bad.Seq = 2
		if kind == "old" {
			bad.Seq = 1
		}
		if kind == "foreign" {
			bad.DeviceID = "foreign"
		}
		response = signed(bad)
		if kind == "signature" {
			response.Sig = base64.StdEncoding.EncodeToString(make([]byte, 64))
		}
		if err := e.Sync(context.Background()); err != ErrManifest {
			t.Fatal(kind, err)
		}
		after, _ := os.ReadFile(filepath.Join(st.Root, "manifest"))
		if !bytes.Equal(before, after) || !e.view().Manifest.LastSync.Equal(last) {
			t.Fatal("Sync überschrieben")
		}
	}
	for _, err := range []error{ErrManifest, ErrHash, ErrRuntime, ErrProtocol} {
		if IsOffline(err) {
			t.Fatal("Prüffehler als offline")
		}
	}
	if !IsOffline(&net.DNSError{IsTimeout: true}) {
		t.Fatal("Netzausfall nicht erkannt")
	}
}
func TestZIPAbweisungAmEntpacker(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:/evil", "CON.txt", "a\\b", "bad.", "a/../b"} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			w, _ := z.Create(name)
			w.Write([]byte("x"))
			z.Close()
			var n int64
			if extract(t.TempDir(), b.Bytes(), map[string]string{}, &n) != ErrHash {
				t.Fatal("Pfad akzeptiert")
			}
		})
	}
	for _, kind := range []string{"symlink", "duplicate", "case", "special", "bomb"} {
		t.Run(kind, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			h := &zip.FileHeader{Name: "a.py", Method: zip.Deflate}
			if kind == "symlink" {
				h.SetMode(os.ModeSymlink | 0777)
			}
			if kind == "special" {
				h.SetMode(os.ModeNamedPipe | 0600)
			}
			w, _ := z.CreateHeader(h)
			if kind == "bomb" {
				w.Write(bytes.Repeat([]byte("x"), 2<<20))
			} else {
				w.Write([]byte("x"))
			}
			if kind == "duplicate" || kind == "case" {
				name := "a.py"
				if kind == "case" {
					name = "A.py"
				}
				w, _ = z.Create(name)
				w.Write([]byte("y"))
			}
			z.Close()
			var n int64
			if extract(t.TempDir(), b.Bytes(), map[string]string{}, &n) != ErrHash {
				t.Fatal("ZIP akzeptiert")
			}
		})
	}
}

type overheadCipher struct{ testCipher }

func (overheadCipher) Seal(b []byte) ([]byte, error) {
	return append(bytes.Repeat([]byte{'X'}, 16<<20-500), b...), nil
}
func TestJournalMisstVerschluesselteBytes(t *testing.T) {
	st, _ := OpenState(t.TempDir(), overheadCipher{})
	for i := 0; i < 8; i++ {
		if e := st.Append(Result{RunKey: strings.Repeat("r", 100)}); e != nil {
			t.Fatal(e)
		}
	}
	j := st.Journal()
	if j.Dropped == 0 {
		t.Fatal("Overhead ignoriert")
	}
	info, _ := os.Stat(filepath.Join(st.Root, "journal"))
	if info.Size() > 16<<20 {
		t.Fatal(info.Size())
	}
}
func TestReadyVerboteneZustaende(t *testing.T) {
	c := Config{ExpectedAccount: "own"}
	c.StartPolicy.Mode = "idle"
	c.StartPolicy.IdleMinutes = 10
	for _, s := range []Session{{Active: false, Account: "own", Idle: time.Hour}, {Active: true, Account: "other", Idle: time.Hour}, {Active: true, Account: "own", Idle: time.Minute}} {
		if Ready(s, c, true) {
			t.Fatal("ready trotz Sperre")
		}
	}
	if Ready(Session{Active: true, Account: "own", Idle: time.Hour}, c, false) {
		t.Fatal("ungeprüft ready")
	}
	if !Ready(Session{Active: true, Account: "own", Idle: 10 * time.Minute}, c, true) {
		t.Fatal("positive Kontrolle")
	}
}
func TestOfflineVerweigertWirdJournalisiert(t *testing.T) {
	st, _ := OpenState(t.TempDir(), testCipher{})
	pin := Pin{Pin: "demo", Commit: strings.Repeat("a", 64), Path: "demo", Algo: "sha256", DeclaredTools: []string{"tool"}, Schedules: []Schedule{}}
	m := Manifest{Contract: 1, DeviceID: "own", Seq: 1, IssuedAt: "2026-10-23T10:00:00Z", GraceHours: 1, Pins: []Pin{pin}}
	e := Engine{State: st, syncErr: &net.DNSError{IsTimeout: true}, bundle: Bundle{Manifest: ManifestState{Envelope: SignedManifest{Manifest: encode(m)}, LastSync: time.Now()}}}
	if err := e.scheduled(context.Background(), pin, Schedule{Name: "daily"}, time.Now().UTC().Truncate(time.Minute)); err != nil {
		t.Fatal(err)
	}
	j := st.Journal()
	if len(j.Results) != 1 || j.Results[0].End != "refused" || j.Results[0].Detail != "offline" || j.Marker != nil {
		t.Fatal(j)
	}
	if errors.Is(ErrManifest, ErrOffline) {
		t.Fatal("Klassen vermischt")
	}
}
