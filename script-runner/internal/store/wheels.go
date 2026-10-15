package store

import (
	"context"
	"crypto/sha1" //nolint:gosec // Git-Objekt-IDs im SHA-1-Format, siehe store.go
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"strconv"
	"strings"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

// wheelsDir ist der Ordner mit den Wheels direkt im Pin-Ordner (#1041).
const wheelsDir = "wheels"

// MaxWheelFiles begrenzt die Zahl der Wheels eines Pins. Die Größe der
// einzelnen Wheels und des entpackten Locks begrenzt internal/libs.
const MaxWheelFiles = 256

// WheelRef ist ein Wheel im Ordner wheels/ des Pin-Ordners: sein Dateiname und
// die Blob-ID aus dem nachgerechneten Baum.
type WheelRef struct {
	Name   string
	BlobID string
}

// ListWheels liest den Ordner wheels/ im Pin-Ordner, ohne die Wheels zu
// laden. Jeder Baum bis dorthin ist nachgerechnet (M5). Es gibt keinen
// Ordner → leere Liste. Nur reguläre Dateien mit der Endung .whl sind
// zulässig; alles andere ist libs_invalid (Symlink und Gitlink melden ihre
// eigenen Gründe).
func (s *Store) ListWheels(ctx context.Context, commit, path string) ([]WheelRef, error) {
	b, w, treeID, err := s.openPin(ctx, commit, path)
	if err != nil {
		return nil, err
	}
	defer b.close()
	data, err := w.read(treeID, "tree")
	if err != nil {
		return nil, err
	}
	entries, err := parseTree(data, w.hashLen)
	if err != nil {
		return nil, err
	}
	var wheelsID string
	for _, e := range entries {
		if e.name != wheelsDir {
			continue
		}
		if e.mode != "40000" {
			return nil, &Invalid{Reason: contract.ReasonLibsInvalid, Msg: "wheels ist kein Ordner"}
		}
		wheelsID = e.id
	}
	if wheelsID == "" {
		return nil, nil
	}
	data, err = w.read(wheelsID, "tree")
	if err != nil {
		return nil, err
	}
	entries, err = parseTree(data, w.hashLen)
	if err != nil {
		return nil, err
	}
	var refs []WheelRef
	for _, e := range entries {
		switch e.mode {
		case "100644", "100755":
		case "120000":
			return nil, &Invalid{Reason: contract.ReasonSymlink, Msg: "Symlink in wheels/"}
		case "160000":
			return nil, &Invalid{Reason: contract.ReasonGitlink, Msg: "Gitlink in wheels/"}
		default:
			return nil, &Invalid{Reason: contract.ReasonLibsInvalid, Msg: "wheels/ enthält mehr als Wheel-Dateien"}
		}
		if !gitref.ValidSegment(e.name) || !strings.HasSuffix(e.name, ".whl") {
			return nil, &Invalid{Reason: contract.ReasonLibsInvalid, Msg: "wheels/ enthält eine Datei ohne .whl"}
		}
		if len(refs) >= MaxWheelFiles {
			return nil, &Invalid{Reason: contract.ReasonLibsTooLarge, Msg: "zu viele Wheels"}
		}
		refs = append(refs, WheelRef{Name: e.name, BlobID: e.id})
	}
	return refs, nil
}

// CopyBlob streamt den Blob id nach dst und rechnet dabei seine Git-ID nach
// (M5): Stimmt sie nicht, ist das Objekt manipuliert, und der Aufrufer muss
// verwerfen, was er schon bekommen hat. Größere Objekte als max werden gar
// nicht erst gelesen. Der Speicher bleibt unabhängig von der Blob-Größe klein.
func (s *Store) CopyBlob(ctx context.Context, id string, dst io.Writer, max int64) (int64, error) {
	if !gitref.ValidCommit(id) {
		return 0, &Invalid{Reason: contract.ReasonBadPath, Msg: "ungültige Objekt-ID"}
	}
	b, err := s.batch(ctx)
	if err != nil {
		return 0, err
	}
	defer b.close()
	if _, err := io.WriteString(b.in, id+"\n"); err != nil {
		return 0, err
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == "missing" {
		return 0, &Invalid{Reason: contract.ReasonHashMismatch, Msg: "Objekt fehlt"}
	}
	if len(fields) != 3 || fields[0] != id {
		return 0, errors.New("store: unerwartete Antwort von cat-file")
	}
	if fields[1] != "blob" {
		return 0, &Invalid{Reason: contract.ReasonHashMismatch, Msg: "Objekt ist kein Blob"}
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return 0, errors.New("store: unerwartete Größe von cat-file")
	}
	if size > max {
		return 0, &Invalid{Reason: contract.ReasonLibsTooLarge, Msg: "Wheel über der Grenze"}
	}
	var h hash.Hash
	if len(id)/2 == sha256.Size {
		h = sha256.New()
	} else {
		h = sha1.New() //nolint:gosec // siehe Import
	}
	_, _ = io.WriteString(h, "blob "+strconv.FormatInt(size, 10)+"\x00")
	n, err := io.CopyN(io.MultiWriter(dst, h), b.out, size)
	if err != nil {
		return n, err
	}
	if nl, err := b.out.ReadByte(); err != nil || nl != '\n' {
		return n, errors.New("store: unerwartetes Ende der Antwort von cat-file")
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != id {
		return n, &Invalid{Reason: contract.ReasonHashMismatch, Msg: "Objekt passt nicht zu seinem Hash"}
	}
	return n, nil
}
