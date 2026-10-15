package libs

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"sort"
	"testing"
)

// zent ist ein Eintrag eines Test-Zips. Mit setHdr lassen sich die Felder
// setzen, die ein normaler Schreiber nie so setzen würde (Angriffsform).
type zent struct {
	name   string
	data   []byte
	mode   os.FileMode // 0 = Standard; sonst per SetMode (Symlink, Gerät, setuid …)
	store  bool        // ohne Kompression
	setHdr func(*zip.FileHeader)
}

func zipOf(t testing.TB, ents ...zent) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.store {
			h.Method = zip.Store
		}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		if e.setHdr != nil {
			e.setHdr(h)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatalf("zip %q: %v", e.name, err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatalf("zip %q: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rawZip schreibt einen Eintrag mit frei gewählten Kopfzeilen (CreateRaw):
// data ist der bereits komprimierte Inhalt.
func rawZip(t testing.TB, h zip.FileHeader, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// deflatedZeros komprimiert n Null-Bytes (die Form einer Bombe).
func deflatedZeros(t testing.TB, n int) (comp []byte, crc uint32) {
	t.Helper()
	var buf bytes.Buffer
	fw, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	h := crc32.NewIEEE()
	for left := n; left > 0; {
		c := chunk
		if left < len(c) {
			c = c[:left]
		}
		if _, err := fw.Write(c); err != nil {
			t.Fatal(err)
		}
		h.Write(c)
		left -= len(c)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), h.Sum32()
}

// wheelBytes baut ein kleines synthetisches Wheel: die Dateien plus ein
// .dist-info.
func wheelBytes(t testing.TB, dist, version string, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var ents []zent
	for _, n := range names {
		ents = append(ents, zent{name: n, data: []byte(files[n])})
	}
	di := dist + "-" + version + ".dist-info/"
	ents = append(ents,
		zent{name: di + "METADATA", data: []byte("Metadata-Version: 2.1\nName: " + dist + "\nVersion: " + version + "\n")},
		zent{name: di + "WHEEL", data: []byte("Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n")},
		zent{name: di + "RECORD", data: nil},
	)
	return zipOf(t, ents...)
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// lockLine ist ein Eintrag im pip-Format mit einem Hash.
func lockLine(name, version string, wheel []byte) string {
	return fmt.Sprintf("%s==%s \\\n    --hash=sha256:%s\n", name, version, sum(wheel))
}

func reasonOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	if err == nil {
		return ""
	}
	return "kein *Error: " + err.Error()
}

func mustHaveReason(t *testing.T, err error, want string) {
	t.Helper()
	if got := reasonOf(err); got != want {
		t.Fatalf("Grund = %q (err = %v), want %q", got, err, want)
	}
}

// tempDir ist t.TempDir, das auch nicht beschreibbare Ordner (0555) wieder
// los wird: libs/<hash> ist absichtlich schreibgeschützt.
func tempDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { _ = removeAll(dir) })
	return dir
}
