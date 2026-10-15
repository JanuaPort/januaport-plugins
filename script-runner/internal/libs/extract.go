package libs

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits sind die Grenzen v1 für Wheels und entpackten Inhalt (SEC B4 a, e).
// Gezählt wird beim Entpacken, nie nach den Angaben im Zip-Kopf.
type Limits struct {
	MaxFileBytes      int64 // eine entpackte Datei
	MaxWheelBytes     int64 // alle entpackten Dateien eines Wheels
	MaxLockBytes      int64 // alle entpackten Dateien eines Locks („installiert“)
	MaxLockFiles      int   // Dateien eines Locks
	MaxTotalBytes     int64 // alle Locks auf der Platte zusammen
	MaxWheelFileBytes int64 // die .whl-Datei selbst
	MaxWheelEntries   int   // Einträge im Zip eines Wheels (Dateien und Ordner)
	MaxRatio          int64 // entpackt zu .whl-Größe, je Wheel
	RatioFloorBytes   int64 // unterhalb dieser entpackten Summe wird das Verhältnis nicht geprüft
}

// DefaultLimits sind die Grenzen v1 (PO/LEAD 15.10.): höchstens 300 MiB
// installiert und 30 000 Dateien je Lock, 2 GiB gesamt. numpy plus pandas
// belegen 91 MiB und 2478 Dateien (Messung P0).
var DefaultLimits = Limits{
	MaxFileBytes:      128 << 20,
	MaxWheelBytes:     300 << 20,
	MaxLockBytes:      300 << 20,
	MaxLockFiles:      30000,
	MaxTotalBytes:     2 << 30,
	MaxWheelFileBytes: 256 << 20,
	MaxWheelEntries:   40000,
	MaxRatio:          64,
	RatioFloorBytes:   1 << 20,
}

const (
	manifestName   = ".jnpt-manifest"
	maxNameBytes   = 255
	maxPathBytes   = 1024
	maxManifestLen = 64 << 20
	copyBufBytes   = 32 << 10
)

// extractor entpackt die Wheels eines Locks in ein gemeinsames Wurzelverzeichnis
// und führt Buch für das Manifest. Er schreibt nur in root.
type extractor struct {
	root  string
	lim   Limits
	used  int64 // Bytes anderer Locks auf der Platte
	bytes int64 // entpackte Bytes dieses Locks
	files int

	made  bool
	seen  map[string]seenEntry // gefalteter Pfad → Eintrag
	dirs  map[string]bool      // Ordner relativ zur Wurzel (Originalschreibweise)
	datas []manifestFile
}

type seenEntry struct {
	orig string
	dir  bool
}

func newExtractor(root string, lim Limits, usedElsewhere int64) *extractor {
	return &extractor{root: root, lim: lim, used: usedElsewhere, seen: map[string]seenEntry{}, dirs: map[string]bool{}}
}

// addWheel entpackt ein Wheel. Erst werden alle Köpfe geprüft (Namen,
// Typen, Zip64, Verschlüsselung, Kollisionen), dann die Dateien gestreamt.
func (e *extractor) addWheel(ra io.ReaderAt, size int64) error {
	if size > e.lim.MaxWheelFileBytes {
		return tooLarge("Wheel-Datei über der Grenze")
	}
	if err := checkStructure(ra, size, e.lim.MaxWheelEntries); err != nil {
		return err
	}
	zr, err := zip.NewReader(io.NewSectionReader(ra, 0, size), size)
	if err != nil && (!errors.Is(err, zip.ErrInsecurePath) || zr == nil) {
		return invalid("Wheel ist kein gültiges Zip")
	}
	if len(zr.File) > e.lim.MaxWheelEntries {
		return tooLarge("zu viele Einträge im Wheel")
	}
	type item struct {
		f   *zip.File
		rel string
		dir bool
	}
	items := make([]item, 0, len(zr.File))
	for _, f := range zr.File {
		rel, isDir, err := e.checkHeader(f)
		if err != nil {
			return err
		}
		items = append(items, item{f: f, rel: rel, dir: isDir})
	}
	if err := e.makeRoot(); err != nil {
		return err
	}
	var wheelBytes int64
	for _, it := range items {
		full := filepath.Join(e.root, filepath.FromSlash(it.rel))
		if !within(e.root, full) {
			return invalid("Pfad außerhalb des Ziels")
		}
		if it.dir {
			if err := os.MkdirAll(full, 0o700); err != nil {
				return err
			}
			e.markDirs(it.rel)
			continue
		}
		if err := e.mkParents(it.rel); err != nil {
			return err
		}
		n, sum, err := e.writeFile(full, it.f, &wheelBytes, size)
		if err != nil {
			return err
		}
		e.datas = append(e.datas, manifestFile{Path: it.rel, Size: n, SHA256: sum})
	}
	return nil
}

func (e *extractor) makeRoot() error {
	if e.made {
		return nil
	}
	if err := os.MkdirAll(e.root, 0o700); err != nil {
		return err
	}
	e.made = true
	return nil
}

func (e *extractor) mkParents(rel string) error {
	parent := filepath.Dir(filepath.FromSlash(rel))
	if parent == "." {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(e.root, parent), 0o700); err != nil {
		return err
	}
	e.markDirs(filepath.ToSlash(parent))
	return nil
}

// markDirs vermerkt einen Ordner und alle seine Eltern fürs Manifest.
func (e *extractor) markDirs(rel string) {
	for p := rel; p != ""; p = pathDir(p) {
		e.dirs[p] = true
	}
}

func pathDir(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[:i]
}

func within(root, full string) bool {
	rel, err := filepath.Rel(root, full)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// checkHeader prüft einen Eintrag, ohne seine Daten zu lesen, und führt Buch
// über Namen und Dateizahl. Rückgabe: relativer Pfad und ob es ein Ordner ist.
func (e *extractor) checkHeader(f *zip.File) (string, bool, error) {
	if f.Flags&0x1 != 0 || f.Flags&0x40 != 0 {
		return "", false, invalid("verschlüsselter Eintrag")
	}
	if f.Method != zip.Store && f.Method != zip.Deflate {
		return "", false, invalid("Kompressionsverfahren nicht zulässig")
	}
	if f.UncompressedSize == ^uint32(0) || f.CompressedSize == ^uint32(0) {
		return "", false, invalid("Zip64-Eintrag")
	}
	isDir := strings.HasSuffix(f.Name, "/")
	name := strings.TrimSuffix(f.Name, "/")
	if err := validateName(name); err != nil {
		return "", false, err
	}
	typ := f.ExternalAttrs >> 16 & 0o170000
	if f.Mode()&(fs.ModeSymlink|fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0 {
		return "", false, invalid("Symlink oder Gerätedatei im Wheel")
	}
	switch {
	case typ == 0:
	case typ == 0o100000 && !isDir:
	case typ == 0o040000 && isDir:
	default:
		return "", false, invalid("Dateityp im Wheel nicht zulässig")
	}
	if isDir {
		if f.UncompressedSize64 != 0 {
			return "", false, invalid("Ordnereintrag mit Daten")
		}
	} else {
		if f.UncompressedSize64 > uint64(e.lim.MaxFileBytes) { //nolint:gosec // die Grenze ist positiv
			return "", false, tooLarge("Datei über der Grenze")
		}
		e.files++
		if e.files > e.lim.MaxLockFiles {
			return "", false, tooLarge("zu viele Dateien im Lock")
		}
	}
	if err := e.register(name, isDir); err != nil {
		return "", false, err
	}
	return name, isDir, nil
}

// register führt die gefalteten Namen aller Pfade und Elternordner: zwei
// Namen, die nach Case-Folding gleich sind, sind eine Kollision (Lehre #1002),
// ebenso Datei gegen Ordner und eine doppelte Datei.
func (e *extractor) register(name string, isDir bool) error {
	segs := strings.Split(name, "/")
	for i := range segs {
		orig := strings.Join(segs[:i+1], "/")
		last := i == len(segs)-1
		dir := !last || isDir
		key := fold(orig)
		if key == manifestName && i == 0 {
			return invalid("reservierter Name")
		}
		prev, ok := e.seen[key]
		switch {
		case !ok:
			e.seen[key] = seenEntry{orig: orig, dir: dir}
		case prev.orig != orig:
			return invalid("Namen, die sich nur in der Schreibweise unterscheiden")
		case prev.dir != dir || (!dir && last):
			return invalid("doppelter Name oder Datei gegen Ordner")
		}
	}
	return nil
}

// fold faltet einen Namen für den Kollisionsvergleich.
func fold(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(unicode.ToLower(unicode.ToUpper(r)))
	}
	return b.String()
}

// validateName weist unzulässige Namen ab (SEC B4 b): leer, absolut,
// Rückstrich, NUL und andere Steuerzeichen, Laufwerksbuchstabe, `.`/`..`,
// leere Bestandteile, kein UTF-8, zu lang.
func validateName(name string) error {
	if name == "" || len(name) > maxPathBytes || !utf8.ValidString(name) {
		return invalid("unzulässiger Dateiname")
	}
	if strings.HasPrefix(name, "/") || strings.ContainsRune(name, '\\') {
		return invalid("absoluter Pfad oder Rückstrich im Dateinamen")
	}
	if len(name) >= 2 && name[1] == ':' && (name[0] >= 'a' && name[0] <= 'z' || name[0] >= 'A' && name[0] <= 'Z') {
		return invalid("Laufwerksbuchstabe im Dateinamen")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return invalid("Steuerzeichen im Dateinamen")
		}
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." || len(seg) > maxNameBytes {
			return invalid("unzulässiger Bestandteil im Dateinamen")
		}
	}
	return nil
}

// writeFile entpackt einen Eintrag und zählt dabei jedes Byte mit; den
// Angaben im Kopf wird nicht geglaubt. wheelBytes ist die laufende Summe des
// Wheels, wheelSize die Größe der .whl-Datei für das Verhältnis.
func (e *extractor) writeFile(full string, f *zip.File, wheelBytes *int64, wheelSize int64) (int64, string, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, "", invalid("Eintrag nicht lesbar")
	}
	defer func() { _ = rc.Close() }()
	out, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	var n int64
	buf := make([]byte, copyBufBytes)
	for {
		m, rerr := rc.Read(buf)
		if m > 0 {
			n += int64(m)
			*wheelBytes += int64(m)
			e.bytes += int64(m)
			if err := e.checkRunning(n, *wheelBytes, wheelSize); err != nil {
				_ = out.Close()
				return n, "", err
			}
			if _, werr := out.Write(buf[:m]); werr != nil {
				_ = out.Close()
				return n, "", werr
			}
			h.Write(buf[:m])
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			_ = out.Close()
			return n, "", invalid("Eintrag beschädigt")
		}
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return n, "", err
	}
	if err := out.Close(); err != nil {
		return n, "", err
	}
	if err := os.Chmod(full, 0o444); err != nil { //nolint:gosec // schreibgeschützt ist gewollt (SEC B4 d)
		return n, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// checkRunning prüft nach jedem gelesenen Stück alle Grenzen (SEC B4 a).
func (e *extractor) checkRunning(file, wheel, wheelSize int64) error {
	switch {
	case file > e.lim.MaxFileBytes:
		return tooLarge("Datei über der Grenze")
	case wheel > e.lim.MaxWheelBytes:
		return tooLarge("Wheel über der Grenze")
	case e.bytes > e.lim.MaxLockBytes:
		return tooLarge("Lock über der Grenze")
	case e.used+e.bytes > e.lim.MaxTotalBytes:
		return tooLarge("Bibliotheken insgesamt über der Grenze")
	case e.lim.MaxRatio > 0 && wheel > e.lim.RatioFloorBytes && wheel/e.lim.MaxRatio > wheelSize:
		return tooLarge("Kompressionsverhältnis über der Grenze")
	}
	return nil
}

// Grenzen für das Zip-Verzeichnis: Das Zip-Paket liest das ganze zentrale
// Verzeichnis in den Speicher, bevor ein Eintrag geprüft ist. Eine Größe pro
// Eintrag und die Zahl der Einträge begrenzen den Speicher auf einige zehn MiB.
const (
	zipRecordLen       = 46
	maxRecordExtra     = 256
	maxRecordComment   = 256
	zipEndRecordLen    = 22
	zip64LocatorLen    = 20
	maxEndRecordSearch = 1 << 16
)

// checkStructure prüft das Ende des Zips und sein zentrales Verzeichnis, bevor
// das Zip-Paket es einliest (SEC B4 e): kein Zip64, keine mehrteiligen
// Archive, höchstens maxEntries Einträge, und das Verzeichnis besteht aus
// genau so vielen lückenlosen Einträgen mit begrenzten Feldern, wie das
// Ende angibt — dann sieht auch das Zip-Paket genau diese und keine weiteren.
func checkStructure(ra io.ReaderAt, size int64, maxEntries int) error {
	pos, rec, err := findEndRecord(ra, size)
	if err != nil {
		return err
	}
	diskNo := binary.LittleEndian.Uint16(rec[4:6])
	diskDir := binary.LittleEndian.Uint16(rec[6:8])
	onDisk := binary.LittleEndian.Uint16(rec[8:10])
	entries := binary.LittleEndian.Uint16(rec[10:12])
	dirSize := binary.LittleEndian.Uint32(rec[12:16])
	dirOff := binary.LittleEndian.Uint32(rec[16:20])
	if entries == 0xFFFF || dirSize == 0xFFFFFFFF || dirOff == 0xFFFFFFFF || onDisk == 0xFFFF {
		return invalid("Zip64")
	}
	if pos >= zip64LocatorLen {
		var loc [4]byte
		if _, err := ra.ReadAt(loc[:], pos-zip64LocatorLen); err == nil && bytes.Equal(loc[:], []byte("PK\x06\x07")) {
			return invalid("Zip64")
		}
	}
	if diskNo != 0 || diskDir != 0 || onDisk != entries || entries == 0 {
		return invalid("Zip mehrteilig oder ohne Einträge")
	}
	if int(entries) > maxEntries {
		return tooLarge("zu viele Einträge im Wheel")
	}
	if int64(dirOff)+int64(dirSize) != pos {
		return invalid("Zip-Verzeichnis liegt nicht lückenlos vor dem Ende")
	}
	br := bufio.NewReaderSize(io.NewSectionReader(ra, int64(dirOff), int64(dirSize)), 64<<10)
	var consumed int64
	var hdr [zipRecordLen]byte
	for i := 0; i < int(entries); i++ {
		if _, err := io.ReadFull(br, hdr[:]); err != nil || !bytes.Equal(hdr[:4], []byte("PK\x01\x02")) {
			return invalid("Zip-Verzeichnis beschädigt")
		}
		name := int64(binary.LittleEndian.Uint16(hdr[28:30]))
		extra := int64(binary.LittleEndian.Uint16(hdr[30:32]))
		comment := int64(binary.LittleEndian.Uint16(hdr[32:34]))
		if name > maxPathBytes+1 || extra > maxRecordExtra || comment > maxRecordComment {
			return invalid("Zip-Verzeichnis mit überlangen Feldern")
		}
		n := name + extra + comment
		if _, err := br.Discard(int(n)); err != nil {
			return invalid("Zip-Verzeichnis beschädigt")
		}
		consumed += zipRecordLen + n
	}
	if consumed != int64(dirSize) {
		return invalid("Zip-Verzeichnis hat mehr oder weniger Einträge als angegeben")
	}
	return nil
}

// findEndRecord sucht das Ende des Zips von hinten wie das Zip-Paket: das
// letzte Vorkommen der Kennung, dessen Kommentarlänge noch in die Datei passt.
func findEndRecord(ra io.ReaderAt, size int64) (int64, []byte, error) {
	if size < zipEndRecordLen {
		return 0, nil, invalid("Wheel ist kein gültiges Zip")
	}
	tail := min(size, zipEndRecordLen+maxEndRecordSearch)
	buf := make([]byte, tail)
	if _, err := ra.ReadAt(buf, size-tail); err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, invalid("Wheel ist kein gültiges Zip")
	}
	for i := len(buf) - zipEndRecordLen; i >= 0; i-- {
		if !bytes.Equal(buf[i:i+4], []byte("PK\x05\x06")) {
			continue
		}
		commentLen := int(binary.LittleEndian.Uint16(buf[i+20 : i+22]))
		if i+zipEndRecordLen+commentLen > len(buf) {
			continue
		}
		return size - tail + int64(i), buf[i : i+zipEndRecordLen], nil
	}
	return 0, nil, invalid("Wheel ist kein gültiges Zip")
}

// Manifest einer fertigen Bibliothek (B3).
type manifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type dirManifest struct {
	Version int            `json:"version"`
	Lock    string         `json:"lock"`
	Dirs    []string       `json:"dirs"`
	Files   []manifestFile `json:"files"`
}

// seal schreibt das Manifest, setzt die Modi (Dateien 0444, Ordner 0555, nie
// setuid) und sichert alles mit fsync. Danach ist das Verzeichnis bereit zum
// Umbenennen.
func (e *extractor) seal(lockHash string) error {
	if err := e.makeRoot(); err != nil {
		return err
	}
	dirs := make([]string, 0, len(e.dirs))
	for d := range e.dirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	sort.Slice(e.datas, func(i, j int) bool { return e.datas[i].Path < e.datas[j].Path })
	m := dirManifest{Version: 1, Lock: lockHash, Dirs: dirs, Files: e.datas}
	if m.Dirs == nil {
		m.Dirs = []string{}
	}
	if m.Files == nil {
		m.Files = []manifestFile{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := writeSynced(filepath.Join(e.root, manifestName), b); err != nil {
		return err
	}
	// Tiefste Ordner zuerst: Kinder sind fertig, bevor der Elternordner
	// schreibgeschützt wird.
	sort.Slice(dirs, func(i, j int) bool { return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/") })
	for _, d := range dirs {
		if err := syncAndLock(filepath.Join(e.root, filepath.FromSlash(d))); err != nil {
			return err
		}
	}
	return syncAndLock(e.root)
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o444)
}

func syncAndLock(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	if err := d.Close(); err != nil {
		return err
	}
	return os.Chmod(dir, 0o555) //nolint:gosec // schreibgeschützt ist gewollt (SEC B4 d)
}

func readManifest(root string) (dirManifest, error) {
	p := filepath.Join(root, manifestName)
	info, err := os.Lstat(p)
	if err != nil {
		return dirManifest{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o444 || info.Size() > maxManifestLen {
		return dirManifest{}, errors.New("libs: Manifest keine reguläre schreibgeschützte Datei")
	}
	f, err := os.Open(p)
	if err != nil {
		return dirManifest{}, err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(io.LimitReader(f, maxManifestLen))
	dec.DisallowUnknownFields()
	var m dirManifest
	if err := dec.Decode(&m); err != nil {
		return dirManifest{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return dirManifest{}, errors.New("libs: Daten nach dem Manifest")
	}
	if m.Version != 1 {
		return dirManifest{}, errors.New("libs: Manifest-Fassung unbekannt")
	}
	return m, nil
}

// verifyDir prüft ein fertiges Verzeichnis gegen sein Manifest (B3): Jede
// Datei und jeder Ordner steht im Manifest und umgekehrt, Größe und SHA-256
// stimmen, Modi sind 0444/0555, es gibt keine Symlinks.
func verifyDir(root, lockHash string) error {
	m, err := readManifest(root)
	if err != nil {
		return err
	}
	if m.Lock != lockHash {
		return errors.New("libs: Manifest gehört zu einem anderen Lock")
	}
	wantFiles := make(map[string]manifestFile, len(m.Files))
	for _, f := range m.Files {
		wantFiles[f.Path] = f
	}
	wantDirs := make(map[string]bool, len(m.Dirs))
	for _, d := range m.Dirs {
		wantDirs[d] = true
	}
	seenFiles, seenDirs := 0, 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			if info.Mode() != fs.ModeDir|0o555 {
				return errors.New("libs: Ordner nicht 0555")
			}
			if rel != "." {
				if !wantDirs[rel] {
					return errors.New("libs: Ordner nicht im Manifest")
				}
				seenDirs++
			}
		case info.Mode().IsRegular():
			if info.Mode() != 0o444 {
				return errors.New("libs: Datei nicht 0444")
			}
			if rel == manifestName {
				return nil
			}
			want, ok := wantFiles[rel]
			if !ok || info.Size() != want.Size {
				return errors.New("libs: Datei nicht im Manifest oder Größe weicht ab")
			}
			sum, err := fileSHA256(p)
			if err != nil || sum != want.SHA256 {
				return errors.New("libs: Inhalt weicht vom Manifest ab")
			}
			seenFiles++
		default:
			return errors.New("libs: Symlink oder Sonderdatei")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if seenFiles != len(wantFiles) || seenDirs != len(wantDirs) {
		return errors.New("libs: Manifest nennt Einträge, die fehlen")
	}
	return nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, copyBufBytes)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// removeAll löscht einen Baum, dessen Ordner schreibgeschützt sein können.
func removeAll(path string) error {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}
