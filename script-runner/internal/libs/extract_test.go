package libs

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

// extractAll entpackt die Wheels in ein frisches Wurzelverzeichnis und
// versiegelt es (Manifest, Modi).
func extractAll(t *testing.T, lim Limits, used int64, wheels ...[]byte) (string, error) {
	t.Helper()
	root := filepath.Join(tempDir(t), "libs")
	ex := newExtractor(root, lim, used)
	for _, w := range wheels {
		if err := ex.addWheel(bytes.NewReader(w), int64(len(w))); err != nil {
			return root, err
		}
	}
	return root, ex.seal(strings.Repeat("a", 64))
}

func TestExtractValidWheel(t *testing.T) {
	w := wheelBytes(t, "demo", "1.0", map[string]string{
		"demo/__init__.py":       "VERSION = '1.0'\n",
		"demo/sub/mod.py":        "X = 1\n",
		"demo/data/ünïcode名.txt": "ok",
		"demo/with space.py":     "Y = 2\n",
	})
	root, err := extractAll(t, DefaultLimits, 0, w)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "demo", "sub", "mod.py"))
	if err != nil || string(b) != "X = 1\n" {
		t.Fatalf("mod.py = %q, %v", b, err)
	}
	assertNormalizedModes(t, root)
	if _, err := os.Stat(filepath.Join(root, manifestName)); err != nil {
		t.Fatalf("Manifest fehlt: %v", err)
	}
	if err := verifyDir(root, strings.Repeat("a", 64)); err != nil {
		t.Fatalf("frisch entpacktes Verzeichnis besteht die Prüfung nicht: %v", err)
	}
}

// assertNormalizedModes: B4 (d) — Dateien 0444, Verzeichnisse 0555, nie
// setuid, setgid, sticky oder ausführbar für Dateien.
func assertNormalizedModes(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		want := fs.FileMode(0o444)
		if d.IsDir() {
			want = fs.ModeDir | 0o555
		}
		if info.Mode() != want {
			t.Errorf("%s: Modus %v, want %v", p, info.Mode(), want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// B4 (b): Eintragsnamen.
func TestExtractRejectsUnsafeNames(t *testing.T) {
	bad := []struct{ name, entry string }{
		{"Pfadausbruch", "../evil.py"},
		{"Pfadausbruch tief", "a/../../evil.py"},
		{"Pfadausbruch in der Mitte", "a/../b.py"},
		{"nur ..", ".."},
		{"nur .", "."},
		{"Punkt-Segment", "a/./b.py"},
		{"Punkt am Anfang", "./a.py"},
		{"absolut", "/etc/passwd"},
		{"Backslash", "a\\b.py"},
		{"Backslash am Anfang", "\\evil.py"},
		{"Windows-Ausbruch", "..\\evil.py"},
		{"NUL", "a\x00b.py"},
		{"NUL am Ende", "a.py\x00"},
		{"Laufwerksbuchstabe mit Slash", "C:/evil.py"},
		{"Laufwerksbuchstabe klein", "c:evil.py"},
		{"leerer Bestandteil", "a//b.py"},
		{"leerer Name", ""},
		{"Steuerzeichen", "a\x01b.py"},
		{"DEL", "a\x7fb.py"},
		{"Zeilenumbruch", "a\nb.py"},
		{"ungültiges UTF-8", "bad\xffutf8.py"},
		{"Name über 255 Byte", strings.Repeat("a", 256) + ".py"},
		{"Pfad über 1024 Byte", strings.Repeat("a/", 600) + "x.py"},
		{"reservierter Name", ".jnpt-manifest"},
		{"reservierter Name, andere Schreibweise", ".JNPT-Manifest"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			w := zipOf(t, zent{name: tt.entry, data: []byte("x")})
			_, err := extractAll(t, DefaultLimits, 0, w)
			mustHaveReason(t, err, contract.ReasonLibsInvalid)
		})
	}
}

func TestExtractAcceptsOrdinaryNames(t *testing.T) {
	ents := []zent{
		{name: "pkg/", data: nil},
		{name: "pkg/__init__.py", data: []byte("x")},
		{name: "pkg/__pycache__/m.cpython-312.pyc", data: []byte("x")},
		{name: "pkg.libs/libopenblas64_p-r0-0cf96a72.3.23.dev.so", data: []byte("x")},
		{name: "pkg-1.0.dist-info/METADATA", data: []byte("x")},
		{name: "pkg/名前.py", data: []byte("x")},
		{name: "pkg/a b.py", data: []byte("x")},
		{name: "pkg/..hidden", data: []byte("x")},
		{name: "pkg/a..b", data: []byte("x")},
		{name: "empty/", data: nil},
	}
	if _, err := extractAll(t, DefaultLimits, 0, zipOf(t, ents...)); err != nil {
		t.Fatal(err)
	}
}

// B4 (b): doppelte Namen nach Case-Folding (Lehre #1002) und Kollisionen
// zwischen Datei und Ordner.
func TestExtractRejectsDuplicateNames(t *testing.T) {
	tests := []struct {
		name string
		ents []zent
	}{
		{"gleicher Name", []zent{{name: "a.py", data: []byte("1")}, {name: "a.py", data: []byte("2")}}},
		{"Groß-/Kleinschreibung", []zent{{name: "Pkg/Mod.py", data: []byte("1")}, {name: "pkg/mod.py", data: []byte("2")}}},
		{"nur der Ordner weicht ab", []zent{{name: "Pkg/a.py", data: []byte("1")}, {name: "pkg/b.py", data: []byte("2")}}},
		{"Kelvin-Zeichen", []zent{{name: "k.py", data: []byte("1")}, {name: "\u212a.py", data: []byte("2")}}},
		{"Datei und Ordner gleichen Namens", []zent{{name: "a", data: []byte("1")}, {name: "a/b.py", data: []byte("2")}}},
		{"Ordner und Datei gleichen Namens", []zent{{name: "a/b.py", data: []byte("2")}, {name: "a", data: []byte("1")}}},
		{"Datei und Ordner, andere Schreibweise", []zent{{name: "A", data: []byte("1")}, {name: "a/b.py", data: []byte("2")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := extractAll(t, DefaultLimits, 0, zipOf(t, tt.ents...))
			mustHaveReason(t, err, contract.ReasonLibsInvalid)
		})
	}
}

// Dieselbe Datei in zwei Wheels desselben Locks ist ebenfalls eine
// Kollision (ein gemeinsamer Namensraum je Lock).
func TestExtractRejectsCollisionAcrossWheels(t *testing.T) {
	a := wheelBytes(t, "a", "1", map[string]string{"shared/__init__.py": "1"})
	b := wheelBytes(t, "b", "1", map[string]string{"Shared/__init__.py": "2"})
	_, err := extractAll(t, DefaultLimits, 0, a, b)
	mustHaveReason(t, err, contract.ReasonLibsInvalid)
	c := wheelBytes(t, "c", "1", map[string]string{"shared/__init__.py": "3"})
	_, err = extractAll(t, DefaultLimits, 0, a, c)
	mustHaveReason(t, err, contract.ReasonLibsInvalid)
}

// B4 (c): Symlinks und Gerätedateien über die externen Attribute.
func TestExtractRejectsSpecialFiles(t *testing.T) {
	dosUnix := func(unixMode uint32) func(*zip.FileHeader) {
		return func(h *zip.FileHeader) {
			h.CreatorVersion = 20 // MS-DOS: Go liest die Unix-Bits nicht, wir schon
			h.ExternalAttrs = unixMode << 16
		}
	}
	tests := []struct {
		name string
		ent  zent
	}{
		{"Symlink", zent{name: "evil", data: []byte("/etc/passwd"), mode: os.ModeSymlink | 0o777}},
		{"Symlink auf ..", zent{name: "evil", data: []byte("../.."), mode: os.ModeSymlink | 0o777}},
		{"Zeichengerät", zent{name: "dev", mode: os.ModeDevice | os.ModeCharDevice | 0o666}},
		{"Blockgerät", zent{name: "dev", mode: os.ModeDevice | 0o666}},
		{"benannte Pipe", zent{name: "fifo", mode: os.ModeNamedPipe | 0o666}},
		{"Socket", zent{name: "sock", mode: os.ModeSocket | 0o666}},
		{"Symlink als Ordnereintrag", zent{name: "d/", mode: os.ModeSymlink | 0o777}},
		{"Symlink-Bits, Ersteller DOS", zent{name: "evil", data: []byte("/etc"), setHdr: dosUnix(0o120777)}},
		{"Gerät-Bits, Ersteller DOS", zent{name: "dev", setHdr: dosUnix(0o020666)}},
		{"Blockgerät-Bits, Ersteller DOS", zent{name: "dev", setHdr: dosUnix(0o060666)}},
		{"FIFO-Bits, Ersteller DOS", zent{name: "fifo", setHdr: dosUnix(0o010666)}},
		{"Socket-Bits, Ersteller DOS", zent{name: "sock", setHdr: dosUnix(0o140666)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := extractAll(t, DefaultLimits, 0, zipOf(t, tt.ent))
			mustHaveReason(t, err, contract.ReasonLibsInvalid)
		})
	}
}

// B4 (d): Modi werden normalisiert, kein setuid.
func TestExtractNormalizesModes(t *testing.T) {
	w := zipOf(t,
		zent{name: "bin/setuid", data: []byte("x"), mode: os.ModeSetuid | 0o755},
		zent{name: "bin/setgid", data: []byte("x"), mode: os.ModeSetgid | 0o755},
		zent{name: "bin/sticky", data: []byte("x"), mode: os.ModeSticky | 0o777},
		zent{name: "bin/exec", data: []byte("x"), mode: 0o755},
		zent{name: "bin/world", data: []byte("x"), mode: 0o777},
		zent{name: "bin/none", data: []byte("x"), setHdr: func(h *zip.FileHeader) { h.CreatorVersion = 3<<8 | 20; h.ExternalAttrs = 0o100000 << 16 }},
		zent{name: "bin/writeonly", data: []byte("x"), mode: 0o200},
		zent{name: "wide/", mode: os.ModeDir | os.ModeSetuid | 0o777},
		zent{name: "closed/", mode: os.ModeDir | 0o000},
		zent{name: "closed/f.py", data: []byte("x")},
	)
	root, err := extractAll(t, DefaultLimits, 0, w)
	if err != nil {
		t.Fatal(err)
	}
	assertNormalizedModes(t, root)
	// Lesbar für die Sandbox (anderer Nutzer): „other“ hat r (und x bei Ordnern).
	info, _ := os.Stat(filepath.Join(root, "bin", "setuid"))
	if info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 || info.Mode().Perm()&0o004 == 0 {
		t.Fatalf("Modus von setuid-Datei = %v", info.Mode())
	}
}

// B4 (e): Zip64 und Anzahl der Einträge.
func TestExtractRejectsZip64(t *testing.T) {
	t.Run("Eintrag mit Zip64-Größen", func(t *testing.T) {
		w := rawZip(t, zip.FileHeader{Name: "big.bin", Method: zip.Store, CompressedSize64: 4, UncompressedSize64: 5 << 30}, []byte("data"))
		_, err := extractAll(t, DefaultLimits, 0, w)
		mustHaveReason(t, err, contract.ReasonLibsInvalid)
	})
	t.Run("Zip64-Verzeichnisende (über 65535 Einträge)", func(t *testing.T) {
		var ents []zent
		for i := 0; i < 65536; i++ {
			ents = append(ents, zent{name: "d/f" + itoa(i), store: true})
		}
		_, err := extractAll(t, DefaultLimits, 0, zipOf(t, ents...))
		mustHaveReason(t, err, contract.ReasonLibsInvalid)
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestExtractRejectsEncryptedAndUnknownMethods(t *testing.T) {
	for name, h := range map[string]zip.FileHeader{
		"verschlüsselt":      {Name: "a.py", Method: zip.Store, Flags: 0x1, CompressedSize64: 4, UncompressedSize64: 4},
		"starke Verschl.":    {Name: "a.py", Method: zip.Store, Flags: 0x40, CompressedSize64: 4, UncompressedSize64: 4},
		"bzip2":              {Name: "a.py", Method: 12, CompressedSize64: 4, UncompressedSize64: 4},
		"AES (Methode 99)":   {Name: "a.py", Method: 99, CompressedSize64: 4, UncompressedSize64: 4},
		"LZMA (Methode 14)":  {Name: "a.py", Method: 14, CompressedSize64: 4, UncompressedSize64: 4},
		"Deflate64 (Meth.9)": {Name: "a.py", Method: 9, CompressedSize64: 4, UncompressedSize64: 4},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := extractAll(t, DefaultLimits, 0, rawZip(t, h, []byte("data")))
			mustHaveReason(t, err, contract.ReasonLibsInvalid)
		})
	}
}

func TestExtractRejectsNotAZip(t *testing.T) {
	for name, b := range map[string][]byte{
		"leer":            {},
		"Text":            []byte("kein zip"),
		"abgeschnitten":   zipOf(t, zent{name: "a", data: []byte("x")})[:20],
		"Ende abgeschn.":  zipOf(t, zent{name: "a", data: []byte("x")})[:60],
		"nur Signaturen":  []byte("PK\x05\x06"),
		"Kommentarlänge":  append(zipOf(t, zent{name: "a", data: []byte("x")})[:0:0], []byte("PK\x05\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\xff\xff")...),
		"Bytes vor dem Z": append([]byte("junk"), zipOf(t, zent{name: "../x", data: []byte("x")})...),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := extractAll(t, DefaultLimits, 0, b)
			mustHaveReason(t, err, contract.ReasonLibsInvalid)
		})
	}
}

// Zip mit CRC-Fehler: Go prüft die Summe beim Lesen bis zum Ende.
func TestExtractRejectsBadCRC(t *testing.T) {
	h := zip.FileHeader{Name: "a.py", Method: zip.Store, CompressedSize64: 4, UncompressedSize64: 4, CRC32: 0xdeadbeef}
	_, err := extractAll(t, DefaultLimits, 0, rawZip(t, h, []byte("data")))
	mustHaveReason(t, err, contract.ReasonLibsInvalid)
}

// B4 (a): Größen werden beim Streamen gezählt, Grenzen je Datei, Wheel, Lock
// und gesamt, dazu Einträge, Dateien und ein Kompressionsverhältnis.
func TestExtractLimits(t *testing.T) {
	small := func(mut func(*Limits)) Limits {
		l := DefaultLimits
		mut(&l)
		return l
	}
	blob := func(n int) []byte { return bytes.Repeat([]byte("ab"), n/2) }
	tests := []struct {
		name   string
		lim    Limits
		used   int64
		wheels [][]byte
		reason string // leer = muss gelingen
	}{
		{"Datei genau an der Grenze", small(func(l *Limits) { l.MaxFileBytes = 100 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: blob(100)})}, ""},
		{"Datei eins über der Grenze", small(func(l *Limits) { l.MaxFileBytes = 100 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: blob(102)})}, contract.ReasonLibsTooLarge},
		{"Datei gespeichert, über der Grenze", small(func(l *Limits) { l.MaxFileBytes = 100 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: blob(200), store: true})}, contract.ReasonLibsTooLarge},
		{"Wheel an der Grenze", small(func(l *Limits) { l.MaxWheelBytes = 200 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: blob(100)}, zent{name: "b", data: blob(100)})}, ""},
		{"Wheel über der Grenze (Summe der Dateien)", small(func(l *Limits) { l.MaxWheelBytes = 150 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: blob(100)}, zent{name: "b", data: blob(100)})}, contract.ReasonLibsTooLarge},
		{"Lock über der Grenze (zwei Wheels)", small(func(l *Limits) { l.MaxLockBytes = 150 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: blob(100)}), zipOf(t, zent{name: "b", data: blob(100)})}, contract.ReasonLibsTooLarge},
		{"Lock genau an der Grenze", small(func(l *Limits) { l.MaxLockBytes = 200 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: blob(100)}), zipOf(t, zent{name: "b", data: blob(100)})}, ""},
		{"gesamt: andere Locks belegen schon fast alles", small(func(l *Limits) { l.MaxTotalBytes = 1000 }), 950,
			[][]byte{zipOf(t, zent{name: "a", data: blob(100)})}, contract.ReasonLibsTooLarge},
		{"gesamt: knapp genug", small(func(l *Limits) { l.MaxTotalBytes = 1000 }), 900,
			[][]byte{zipOf(t, zent{name: "a", data: blob(100)})}, ""},
		{"Wheel-Datei über der Grenze", small(func(l *Limits) { l.MaxWheelFileBytes = 100 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: blob(2000), store: true})}, contract.ReasonLibsTooLarge},
		{"Einträge je Wheel an der Grenze", small(func(l *Limits) { l.MaxWheelEntries = 3 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: []byte("1")}, zent{name: "b", data: []byte("1")}, zent{name: "c", data: []byte("1")})}, ""},
		{"Einträge je Wheel über der Grenze", small(func(l *Limits) { l.MaxWheelEntries = 3 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: []byte("1")}, zent{name: "b", data: []byte("1")}, zent{name: "c", data: []byte("1")}, zent{name: "d", data: []byte("1")})}, contract.ReasonLibsTooLarge},
		{"Ordnereinträge zählen als Einträge", small(func(l *Limits) { l.MaxWheelEntries = 2 }), 0,
			[][]byte{zipOf(t, zent{name: "d/"}, zent{name: "e/"}, zent{name: "d/a", data: []byte("1")})}, contract.ReasonLibsTooLarge},
		{"Dateien je Lock über der Grenze (zwei Wheels)", small(func(l *Limits) { l.MaxLockFiles = 3 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: []byte("1")}, zent{name: "b", data: []byte("1")}), zipOf(t, zent{name: "c", data: []byte("1")}, zent{name: "d", data: []byte("1")})}, contract.ReasonLibsTooLarge},
		{"Dateien je Lock an der Grenze", small(func(l *Limits) { l.MaxLockFiles = 4 }), 0,
			[][]byte{zipOf(t, zent{name: "a", data: []byte("1")}, zent{name: "b", data: []byte("1")}), zipOf(t, zent{name: "c", data: []byte("1")}, zent{name: "d", data: []byte("1")})}, ""},
		{"Kompressionsverhältnis über der Grenze", small(func(l *Limits) { l.MaxRatio = 10; l.RatioFloorBytes = 0 }), 0,
			[][]byte{zipOf(t, zent{name: "z", data: make([]byte, 100_000)})}, contract.ReasonLibsTooLarge},
		{"Kompressionsverhältnis, kleine Wheels unterhalb des Sockels", small(func(l *Limits) { l.MaxRatio = 10; l.RatioFloorBytes = 1 << 20 }), 0,
			[][]byte{zipOf(t, zent{name: "z", data: make([]byte, 100_000)})}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := extractAll(t, tt.lim, tt.used, tt.wheels...)
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("abgewiesen: %v", err)
				}
				return
			}
			mustHaveReason(t, err, tt.reason)
		})
	}
}

// B4 (a): dem Zip-Kopf wird nie geglaubt. Gelogene Größen führen weder zu
// einer zu großen Datei noch zu einem Absturz.
func TestExtractNeverTrustsHeaderSizes(t *testing.T) {
	lim := DefaultLimits
	lim.MaxFileBytes = 100
	t.Run("Kopf deklariert viel, Inhalt klein", func(t *testing.T) {
		h := zip.FileHeader{Name: "a", Method: zip.Store, CompressedSize64: 1, UncompressedSize64: 3 << 30}
		_, err := extractAll(t, lim, 0, rawZip(t, h, []byte("x")))
		mustHaveReason(t, err, contract.ReasonLibsTooLarge)
	})
	t.Run("Kopf deklariert wenig, Inhalt groß", func(t *testing.T) {
		comp, crc := deflatedZeros(t, 100_000)
		h := zip.FileHeader{Name: "a", Method: zip.Deflate, CompressedSize64: uint64(len(comp)), UncompressedSize64: 10, CRC32: crc}
		root, err := extractAll(t, lim, 0, rawZip(t, h, comp))
		if r := reasonOf(err); r != contract.ReasonLibsInvalid && r != contract.ReasonLibsTooLarge {
			t.Fatalf("Grund = %q (%v)", r, err)
		}
		if info, statErr := os.Stat(filepath.Join(root, "a")); statErr == nil && info.Size() > lim.MaxFileBytes {
			t.Fatalf("Datei mit %d Byte geschrieben, Grenze %d", info.Size(), lim.MaxFileBytes)
		}
	})
	t.Run("Kopf deklariert wenig, gespeicherter Inhalt groß", func(t *testing.T) {
		h := zip.FileHeader{Name: "a", Method: zip.Store, CompressedSize64: 5000, UncompressedSize64: 10, CRC32: 0}
		root, err := extractAll(t, lim, 0, rawZip(t, h, bytes.Repeat([]byte("x"), 5000)))
		if err == nil {
			t.Fatal("angenommen")
		}
		if info, statErr := os.Stat(filepath.Join(root, "a")); statErr == nil && info.Size() > lim.MaxFileBytes {
			t.Fatalf("Datei mit %d Byte geschrieben", info.Size())
		}
	})
}

// B4 (f): Messtest mit 42.zip-artigen Bomben. Ein Wheel, dessen Einträge sich
// auf ein Vielfaches seiner Größe entpacken, wird abgewiesen, und der
// Spitzen-Heap bleibt klein — gemessen wird der Speicher WÄHREND des
// Entpackens, nicht das Ergebnis (Lehre #703). Die Köpfe sind ehrlich (Go
// bricht bei mehr Bytes als deklariert ab); die Grenzen müssen also beim
// Streamen greifen.
func TestExtractBombPeakHeap(t *testing.T) {
	const heapCap = 24 << 20 // erlaubter Zuwachs des Heaps
	bomb := func(entries, perEntry int) []byte {
		comp, crc := deflatedZeros(t, perEntry)
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for i := 0; i < entries; i++ {
			w, err := zw.CreateRaw(&zip.FileHeader{Name: "bomb/f" + itoa(i) + ".bin", Method: zip.Deflate, CRC32: crc,
				CompressedSize64: uint64(len(comp)), UncompressedSize64: uint64(perEntry)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(comp); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	many := bomb(100, 2<<20)   // 200 MiB in 100 Einträgen, je Eintrag erlaubt
	single := bomb(1, 192<<20) // ein Eintrag mit 192 MiB
	t.Logf("viele: %d Byte Wheel → 200 MiB; ein großer: %d Byte Wheel → 192 MiB", len(many), len(single))

	huge := func(l *Limits) {
		l.MaxFileBytes, l.MaxWheelBytes, l.MaxLockBytes, l.MaxTotalBytes = 1<<30, 1<<30, 1<<30, 4<<30
	}
	tests := []struct {
		name  string
		wheel []byte
		mut   func(*Limits)
	}{
		{"viele Einträge, Kompressionsverhältnis", many, func(l *Limits) {}},
		{"viele Einträge, Grenze je Wheel", many, func(l *Limits) { huge(l); l.MaxRatio = 1 << 30; l.MaxWheelBytes = 64 << 20 }},
		{"viele Einträge, Grenze je Lock", many, func(l *Limits) { huge(l); l.MaxRatio = 1 << 30; l.MaxLockBytes = 64 << 20 }},
		{"ein großer Eintrag, Standardgrenzen (Kopf über Dateigrenze)", single, func(l *Limits) {}},
		{"ein großer Eintrag, Kompressionsverhältnis", single, func(l *Limits) { huge(l) }},
		{"ein großer Eintrag, Grenze je Lock", single, func(l *Limits) { huge(l); l.MaxRatio = 1 << 30; l.MaxLockBytes = 64 << 20 }},
		{"ein großer Eintrag, Grenze je Wheel", single, func(l *Limits) { huge(l); l.MaxRatio = 1 << 30; l.MaxWheelBytes = 64 << 20 }},
		{"ein großer Eintrag, Grenze gesamt", single, func(l *Limits) { huge(l); l.MaxRatio = 1 << 30; l.MaxTotalBytes = 64 << 20 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lim := DefaultLimits
			tt.mut(&lim)
			peak := measurePeakHeap(func() {
				_, err := extractAll(t, lim, 0, tt.wheel)
				mustHaveReason(t, err, contract.ReasonLibsTooLarge)
			})
			t.Logf("Spitzen-Heap-Zuwachs: %.1f MiB", float64(peak)/(1<<20))
			if peak > heapCap {
				t.Fatalf("Spitzen-Heap +%d MiB, Obergrenze %d MiB", peak>>20, heapCap>>20)
			}
		})
	}
}

// measurePeakHeap führt fn aus und liefert den größten beobachteten
// Zuwachs des belegten Heaps gegenüber dem Stand davor.
func measurePeakHeap(fn func()) uint64 {
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var (
		mu   sync.Mutex
		peak uint64
		stop = make(chan struct{})
		done = make(chan struct{})
	)
	go func() {
		defer close(done)
		tick := time.NewTicker(500 * time.Microsecond)
		defer tick.Stop()
		for {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			mu.Lock()
			if m.HeapInuse > base.HeapInuse && m.HeapInuse-base.HeapInuse > peak {
				peak = m.HeapInuse - base.HeapInuse
			}
			mu.Unlock()
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	fn()
	close(stop)
	<-done
	mu.Lock()
	defer mu.Unlock()
	return peak
}

// B3: das Manifest bindet jede Datei (Pfad, Größe, SHA-256); jede Abweichung
// beim Start wird erkannt. Die Mutationen ändern jeweils nur das eine Merkmal
// (Rechte der Umgebung bleiben 0444/0555), damit jede Prüfung einzeln belegt
// ist.
func TestVerifyDirDetectsEveryDeviation(t *testing.T) {
	build := func(t *testing.T) string {
		w := wheelBytes(t, "demo", "1.0", map[string]string{"demo/__init__.py": "A = 1\n", "demo/sub/m.py": "B = 2\n"})
		root, err := extractAll(t, DefaultLimits, 0, w)
		if err != nil {
			t.Fatal(err)
		}
		return root
	}
	lockHash := strings.Repeat("a", 64)
	// edit öffnet den Ordner kurz zum Schreiben und stellt 0555 wieder her.
	edit := func(t *testing.T, dir string, fn func()) {
		t.Helper()
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		fn()
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	rewrite := func(t *testing.T, p, content string) {
		t.Helper()
		edit(t, filepath.Dir(p), func() {
			if err := os.Chmod(p, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(p, 0o444); err != nil {
				t.Fatal(err)
			}
		})
	}
	tests := []struct {
		name string
		mut  func(t *testing.T, root string)
	}{
		{"Inhalt geändert, gleiche Größe", func(t *testing.T, root string) {
			rewrite(t, filepath.Join(root, "demo", "__init__.py"), "A = 2\n")
		}},
		{"Inhalt geändert, andere Größe", func(t *testing.T, root string) {
			rewrite(t, filepath.Join(root, "demo", "__init__.py"), "A = 22\n")
		}},
		{"Datei entfernt", func(t *testing.T, root string) {
			edit(t, filepath.Join(root, "demo", "sub"), func() {
				if err := os.Remove(filepath.Join(root, "demo", "sub", "m.py")); err != nil {
					t.Fatal(err)
				}
			})
		}},
		{"Datei hinzugefügt", func(t *testing.T, root string) {
			rewrite(t, filepath.Join(root, "demo", "evil.pth"), "import os\n")
		}},
		{"Ordner hinzugefügt", func(t *testing.T, root string) {
			edit(t, root, func() {
				if err := os.Mkdir(filepath.Join(root, "extra"), 0o555); err != nil {
					t.Fatal(err)
				}
			})
		}},
		{"Ordner entfernt", func(t *testing.T, root string) {
			edit(t, filepath.Join(root, "demo", "sub"), func() {
				if err := os.Remove(filepath.Join(root, "demo", "sub", "m.py")); err != nil {
					t.Fatal(err)
				}
			})
			edit(t, filepath.Join(root, "demo"), func() {
				if err := os.Remove(filepath.Join(root, "demo", "sub")); err != nil {
					t.Fatal(err)
				}
			})
		}},
		{"Symlink hinzugefügt", func(t *testing.T, root string) {
			edit(t, root, func() {
				if err := os.Symlink("/etc/passwd", filepath.Join(root, "link")); err != nil {
					t.Skip("Symlinks nicht verfügbar:", err)
				}
			})
		}},
		{"Datei durch Symlink ersetzt", func(t *testing.T, root string) {
			p := filepath.Join(root, "demo", "__init__.py")
			edit(t, filepath.Dir(p), func() {
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "demo", "sub", "m.py"), p); err != nil {
					t.Skip("Symlinks nicht verfügbar:", err)
				}
			})
		}},
		{"Datei beschreibbar", func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "demo", "__init__.py"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"Datei ausführbar", func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "demo", "__init__.py"), 0o555); err != nil {
				t.Fatal(err)
			}
		}},
		{"setuid-Datei", func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "demo", "__init__.py"), os.ModeSetuid|0o444); err != nil {
				t.Fatal(err)
			}
		}},
		{"Ordner beschreibbar", func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "demo"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"Wurzel beschreibbar", func(t *testing.T, root string) {
			if err := os.Chmod(root, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"Manifest fehlt", func(t *testing.T, root string) {
			edit(t, root, func() {
				if err := os.Remove(filepath.Join(root, manifestName)); err != nil {
					t.Fatal(err)
				}
			})
		}},
		{"Manifest kaputt", func(t *testing.T, root string) {
			rewrite(t, filepath.Join(root, manifestName), "{")
		}},
		{"Manifest leer", func(t *testing.T, root string) {
			rewrite(t, filepath.Join(root, manifestName), "")
		}},
		{"Manifest mit fremdem Feld", func(t *testing.T, root string) {
			b, err := os.ReadFile(filepath.Join(root, manifestName))
			if err != nil {
				t.Fatal(err)
			}
			rewrite(t, filepath.Join(root, manifestName), strings.Replace(string(b), "{", `{"extra":1,`, 1))
		}},
		{"Manifest als Ordner", func(t *testing.T, root string) {
			edit(t, root, func() {
				if err := os.Remove(filepath.Join(root, manifestName)); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, manifestName), 0o555); err != nil {
					t.Fatal(err)
				}
			})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := build(t)
			if err := verifyDir(root, lockHash); err != nil {
				t.Fatalf("Ausgangszustand: %v", err)
			}
			tt.mut(t, root)
			if err := verifyDir(root, lockHash); err == nil {
				t.Fatal("Abweichung nicht erkannt")
			}
		})
	}
	t.Run("falscher Lock-Hash", func(t *testing.T) {
		root := build(t)
		if err := verifyDir(root, strings.Repeat("b", 64)); err == nil {
			t.Fatal("Manifest eines anderen Locks angenommen")
		}
	})
}

func TestSealManifestListsEveryFile(t *testing.T) {
	w := wheelBytes(t, "demo", "1.0", map[string]string{"demo/__init__.py": "A = 1\n"})
	root, err := extractAll(t, DefaultLimits, 0, w)
	if err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]manifestFile{}
	for _, f := range m.Files {
		got[f.Path] = f
	}
	f, ok := got["demo/__init__.py"]
	if !ok || f.Size != 6 || f.SHA256 != sum([]byte("A = 1\n")) {
		t.Fatalf("Eintrag = %+v, ok = %v", f, ok)
	}
	if _, ok := got["demo-1.0.dist-info/METADATA"]; !ok || len(m.Files) != 4 {
		t.Fatalf("Dateien im Manifest: %v", got)
	}
	if m.Lock != strings.Repeat("a", 64) {
		t.Fatalf("Lock = %q", m.Lock)
	}
}
