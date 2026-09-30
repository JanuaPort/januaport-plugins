// Package store ist die Senke (R6, M5): Sie liest einen gepinnten Pfad per
// Tree-Walk aus der Objekt-DB des Bare-Stores — nie aus einem Checkout — und
// rechnet dabei den Hash JEDES gelesenen Objekts selbst nach, vom Commit über
// jeden Baum bis zu jedem Blob. Symlinks (120000) und Gitlinks (160000) weist
// sie ab, ebenso Namen, die aus dem Pin-Ordner führen könnten.
package store

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // Git-Objekt-IDs im SHA-1-Format; Kollisionsschutz liefert git selbst (sha1dc)
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

// Grenzen v1 für einen gepinnten Baum. Der job muss in einen Rahmen passen.
const (
	MaxTreeBytes = 4 << 20
	MaxFiles     = 1000
)

// File ist eine Datei im Pin-Ordner; Path ist relativ zu ihm.
type File struct {
	Path   string
	Data   []byte
	BlobID string
}

// Tree ist der geprüfte Inhalt eines Pin-Ordners.
type Tree struct{ Files []File }

// File sucht eine Datei nach relativem Pfad.
func (t Tree) File(p string) *File {
	for i := range t.Files {
		if t.Files[i].Path == p {
			return &t.Files[i]
		}
	}
	return nil
}

// Invalid ist ein abgewiesener Baum mit Grund aus Vertrag §2.
type Invalid struct {
	Reason string
	Msg    string
}

func (e *Invalid) Error() string { return "store: " + e.Msg }

// Store liest den Bare-Store. Er schreibt nie.
type Store struct{ gitDir string }

// Open öffnet den Store unter gitDir (…/store.git).
func Open(gitDir string) *Store { return &Store{gitDir: gitDir} }

// HasCommit sagt, ob der Commit im Store liegt.
func (s *Store) HasCommit(ctx context.Context, commit string) (bool, error) {
	if !gitref.ValidCommit(commit) {
		return false, errors.New("store: ungültiger Commit")
	}
	b, err := s.batch(ctx)
	if err != nil {
		return false, err
	}
	defer b.close()
	typ, _, err := b.get(commit)
	if errors.Is(err, errMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return typ == "commit", nil
}

// Load liest den Pin-Ordner path aus commit und prüft jedes Objekt.
func (s *Store) Load(ctx context.Context, commit, path string) (Tree, error) {
	if !gitref.ValidCommit(commit) || !gitref.ValidPinPath(path) {
		return Tree{}, &Invalid{Reason: contract.ReasonBadPath, Msg: "Commit oder Pfad ungültig"}
	}
	b, err := s.batch(ctx)
	if err != nil {
		return Tree{}, err
	}
	defer b.close()
	w := &walker{b: b, hashLen: len(commit) / 2}
	raw, err := w.read(commit, "commit")
	if err != nil {
		return Tree{}, err
	}
	treeID, err := commitTree(raw, len(commit))
	if err != nil {
		return Tree{}, err
	}
	for _, seg := range strings.Split(path, "/") {
		treeID, err = w.child(treeID, seg)
		if err != nil {
			return Tree{}, err
		}
	}
	var t Tree
	if err := w.walk(treeID, "", &t); err != nil {
		return Tree{}, err
	}
	return t, nil
}

type walker struct {
	b       *batch
	hashLen int
	total   int
}

func (w *walker) read(id, wantType string) ([]byte, error) {
	typ, data, err := w.b.get(id)
	if errors.Is(err, errMissing) {
		return nil, &Invalid{Reason: contract.ReasonHashMismatch, Msg: "Objekt fehlt"}
	}
	if err != nil {
		return nil, err
	}
	if typ != wantType || objectID(typ, data, w.hashLen) != id {
		return nil, &Invalid{Reason: contract.ReasonHashMismatch, Msg: "Objekt passt nicht zu seinem Hash"}
	}
	return data, nil
}

// child sucht im Baum treeID den Unterordner name.
func (w *walker) child(treeID, name string) (string, error) {
	data, err := w.read(treeID, "tree")
	if err != nil {
		return "", err
	}
	entries, err := parseTree(data, w.hashLen)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.name != name {
			continue
		}
		switch e.mode {
		case "40000":
			return e.id, nil
		case "120000":
			return "", &Invalid{Reason: contract.ReasonSymlink, Msg: "Symlink im Pin-Pfad"}
		case "160000":
			return "", &Invalid{Reason: contract.ReasonGitlink, Msg: "Gitlink im Pin-Pfad"}
		}
		return "", &Invalid{Reason: contract.ReasonBadPath, Msg: "Pin-Pfad ist kein Ordner"}
	}
	return "", &Invalid{Reason: contract.ReasonBadPath, Msg: "Pin-Pfad fehlt im Commit"}
}

func (w *walker) walk(treeID, prefix string, t *Tree) error {
	data, err := w.read(treeID, "tree")
	if err != nil {
		return err
	}
	entries, err := parseTree(data, w.hashLen)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !gitref.ValidSegment(e.name) {
			return &Invalid{Reason: contract.ReasonBadPath, Msg: "unzulässiger Name im Baum"}
		}
		rel := prefix + e.name
		switch e.mode {
		case "100644", "100755":
			if err := w.addBlob(e.id, rel, t); err != nil {
				return err
			}
		case "40000":
			if err := w.walk(e.id, rel+"/", t); err != nil {
				return err
			}
		case "120000":
			return &Invalid{Reason: contract.ReasonSymlink, Msg: "Symlink im Baum"}
		case "160000":
			return &Invalid{Reason: contract.ReasonGitlink, Msg: "Gitlink im Baum"}
		default:
			return &Invalid{Reason: contract.ReasonBadPath, Msg: "unbekannter Modus im Baum"}
		}
	}
	return nil
}

func (w *walker) addBlob(id, rel string, t *Tree) error {
	data, err := w.read(id, "blob")
	if err != nil {
		return err
	}
	w.total += len(data)
	if w.total > MaxTreeBytes || len(t.Files) >= MaxFiles {
		return &Invalid{Reason: contract.ReasonManifestInvalid, Msg: "Pin-Ordner über den Grenzen v1"}
	}
	t.Files = append(t.Files, File{Path: rel, Data: data, BlobID: id})
	return nil
}

// ObjectID rechnet die Git-Objekt-ID nach (SHA-1 bei 20, SHA-256 bei 32
// Byte Hashlänge).
func ObjectID(typ string, data []byte, hashLen int) string { return objectID(typ, data, hashLen) }

func objectID(typ string, data []byte, hashLen int) string {
	var h hash.Hash
	if hashLen == sha256.Size {
		h = sha256.New()
	} else {
		h = sha1.New() //nolint:gosec // siehe Import
	}
	fmt.Fprintf(h, "%s %d\x00", typ, len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func commitTree(raw []byte, idLen int) (string, error) {
	line, _, _ := bytes.Cut(raw, []byte("\n"))
	id, ok := bytes.CutPrefix(line, []byte("tree "))
	if !ok || len(id) != idLen || !gitref.ValidCommit(string(id)) {
		return "", &Invalid{Reason: contract.ReasonHashMismatch, Msg: "Commit ohne gültigen Baum"}
	}
	return string(id), nil
}

type treeEntry struct {
	mode, name, id string
}

func parseTree(data []byte, hashLen int) ([]treeEntry, error) {
	var out []treeEntry
	for len(data) > 0 {
		sp := bytes.IndexByte(data, ' ')
		nul := bytes.IndexByte(data, 0)
		if sp < 1 || nul < sp+2 || len(data) < nul+1+hashLen {
			return nil, &Invalid{Reason: contract.ReasonHashMismatch, Msg: "Baum nicht lesbar"}
		}
		out = append(out, treeEntry{
			mode: string(data[:sp]),
			name: string(data[sp+1 : nul]),
			id:   hex.EncodeToString(data[nul+1 : nul+1+hashLen]),
		})
		data = data[nul+1+hashLen:]
	}
	return out, nil
}

var errMissing = errors.New("store: Objekt fehlt")

// batch ist eine Sitzung von `git cat-file --batch`. Die Umgebung schaltet
// System- und Nutzer-Konfiguration und Ersatz-Objekte ab: gelesen wird genau
// die Objekt-DB, sonst nichts.
type batch struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func (s *Store) batch(ctx context.Context) (*batch, error) {
	cmd := exec.CommandContext(ctx, "git", "--no-replace-objects", "--git-dir="+s.gitDir, "cat-file", "--batch")
	cmd.Env = []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME=/nonexistent", "PATH=/usr/bin:/bin", "GIT_NO_REPLACE_OBJECTS=1"}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &batch{cmd: cmd, in: in, out: bufio.NewReader(out)}, nil
}

func (b *batch) get(id string) (string, []byte, error) {
	if _, err := io.WriteString(b.in, id+"\n"); err != nil {
		return "", nil, err
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == "missing" {
		return "", nil, errMissing
	}
	if len(fields) != 3 || fields[0] != id {
		return "", nil, errors.New("store: unerwartete Antwort von cat-file")
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil || size < 0 || size > MaxTreeBytes+1 {
		return "", nil, &Invalid{Reason: contract.ReasonManifestInvalid, Msg: "Objekt über den Grenzen v1"}
	}
	data := make([]byte, size+1)
	if _, err := io.ReadFull(b.out, data); err != nil {
		return "", nil, err
	}
	return fields[1], data[:size], nil
}

func (b *batch) close() {
	_ = b.in.Close()
	_ = b.cmd.Wait()
}
