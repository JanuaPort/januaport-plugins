package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

const DesktopMaxBytes = 128 << 20
const desktopMaxDepth = 256

var ErrDesktopTooLarge = errors.New("store: Desktop-Paket über der Grenze")

type desktopHead struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Algo    string `json:"algo"`
	Commit  string `json:"commit"`
	Path    string `json:"path"`
	Objects int    `json:"objects"`
}

type desktopObject struct {
	id, typ string
	data    []byte
}

// DesktopTree keeps the verified object graph instead of expanding shared
// subtrees into an exponentially large list of files. File follows at most
// 256 edges and never consults Git again.
type DesktopTree struct {
	objects map[string]desktopObject
	root    string
	hashLen int
}

func (t DesktopTree) File(path string) *File {
	parts := strings.Split(path, "/")
	if !gitref.DesktopPath(path) || len(parts) > desktopMaxDepth {
		return nil
	}
	id := t.root
	for i, seg := range parts {
		o, ok := t.objects[id]
		if !ok || o.typ != "tree" {
			return nil
		}
		entries, err := parseTree(o.data, t.hashLen)
		if err != nil {
			return nil
		}
		id = ""
		for _, e := range entries {
			if e.name == seg {
				id = e.id
				break
			}
		}
		if id == "" {
			return nil
		}
		if i == len(parts)-1 {
			o, ok = t.objects[id]
			if ok && o.typ == "blob" {
				return &File{Path: path, Data: o.data, BlobID: id}
			}
		}
	}
	return nil
}

// DesktopPackage is the desktop M5/R6 sink. It returns only a completely
// checked, bounded package, never a partially verified HTTP stream. Order is
// commit, ancestor trees, then depth-first Git tree order; duplicate IDs occur
// once. Parents and sibling objects are never read.
func (s *Store) DesktopPackage(ctx context.Context, commit, path, algo string) ([]byte, DesktopTree, error) {
	fail := func(err error) ([]byte, DesktopTree, error) { return nil, DesktopTree{}, err }
	if !gitref.ValidCommit(commit) || !gitref.DesktopPath(path) || !(algo == "sha1" && len(commit) == 40 || algo == "sha256" && len(commit) == 64) {
		return fail(desktopInvalid(contract.ReasonBadPath))
	}
	b, err := s.batch(ctx)
	if err != nil {
		return fail(err)
	}
	defer b.close()
	h := desktopHead{"gitobjects", 1, algo, commit, path, 0}
	header, _ := json.Marshal(h)
	w := desktopWalk{b: b, hashLen: len(commit) / 2, objects: map[string]desktopObject{}, heights: map[string]int{}, remaining: DesktopMaxBytes - int64(4+len(header))}
	raw, err := w.read(commit, "commit")
	if err != nil {
		return fail(err)
	}
	id, err := commitTree(raw, len(commit))
	if err != nil {
		return fail(err)
	}
	segments := strings.Split(path, "/")
	if len(segments) > desktopMaxDepth {
		return fail(desktopInvalid(contract.ReasonBadPath))
	}
	for _, seg := range segments {
		entries, err := w.tree(id)
		if err != nil {
			return fail(err)
		}
		id = ""
		for _, e := range entries {
			if e.name == seg && e.mode == "40000" {
				id = e.id
				break
			}
		}
		if id == "" {
			return fail(desktopInvalid(contract.ReasonBadPath))
		}
	}
	if _, err := w.walk(id, len(segments)); err != nil {
		return fail(err)
	}
	h.Objects = len(w.order)
	header, _ = json.Marshal(h)
	total := int64(4 + len(header))
	for _, o := range w.order {
		total += int64(5 + len(o.data))
	}
	if total > DesktopMaxBytes {
		return fail(ErrDesktopTooLarge)
	}
	var out bytes.Buffer
	out.Grow(int(total))
	_ = binary.Write(&out, binary.BigEndian, uint32(len(header)))
	out.Write(header)
	for _, o := range w.order {
		_ = binary.Write(&out, binary.BigEndian, uint32(1+len(o.data)))
		out.WriteByte(map[string]byte{"commit": 'c', "tree": 't', "blob": 'b'}[o.typ])
		out.Write(o.data)
	}
	return out.Bytes(), DesktopTree{objects: w.objects, root: id, hashLen: w.hashLen}, nil
}

type desktopWalk struct {
	b         *batch
	hashLen   int
	objects   map[string]desktopObject
	order     []desktopObject
	heights   map[string]int
	remaining int64
}

func desktopInvalid(reason string) error {
	return &Invalid{Reason: reason, Msg: "Desktop-Objekt ungültig"}
}

func (w *desktopWalk) read(id, typ string) ([]byte, error) {
	if o, ok := w.objects[id]; ok {
		if o.typ != typ {
			return nil, desktopInvalid(contract.ReasonHashMismatch)
		}
		return o.data, nil
	}
	if _, err := io.WriteString(w.b.in, id+"\n"); err != nil {
		return nil, err
	}
	header, err := w.b.out.ReadString('\n')
	if err != nil {
		return nil, err
	}
	f := strings.Fields(header)
	if len(f) != 3 || f[0] != id || f[1] != typ {
		return nil, desktopInvalid(contract.ReasonHashMismatch)
	}
	size, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || size < 0 {
		return nil, desktopInvalid(contract.ReasonHashMismatch)
	}
	// Includes the type and frame length, before allocation or object reading.
	if size+5 > w.remaining {
		return nil, ErrDesktopTooLarge
	}
	data := make([]byte, int(size))
	if _, err := io.ReadFull(w.b.out, data); err != nil {
		return nil, err
	}
	if nl, err := w.b.out.ReadByte(); err != nil || nl != '\n' {
		return nil, desktopInvalid(contract.ReasonHashMismatch)
	}
	if objectID(typ, data, w.hashLen) != id {
		return nil, desktopInvalid(contract.ReasonHashMismatch)
	}
	o := desktopObject{id, typ, data}
	w.objects[id] = o
	w.order = append(w.order, o)
	w.remaining -= size + 5
	return data, nil
}

func (w *desktopWalk) tree(id string) ([]treeEntry, error) {
	raw, err := w.read(id, "tree")
	if err != nil {
		return nil, err
	}
	entries, err := parseTree(raw, w.hashLen)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		fold := gitref.DesktopFold(e.name)
		if !gitref.DesktopSegment(e.name) || seen[fold] {
			return nil, desktopInvalid(contract.ReasonBadPath)
		}
		seen[fold] = true
		switch e.mode {
		case "40000", "100644", "100755":
		case "120000":
			return nil, desktopInvalid(contract.ReasonSymlink)
		case "160000":
			return nil, desktopInvalid(contract.ReasonGitlink)
		default:
			return nil, desktopInvalid(contract.ReasonBadPath)
		}
	}
	return entries, nil
}

func (w *desktopWalk) walk(id string, depth int) (int, error) {
	if height, ok := w.heights[id]; ok {
		if depth+height > desktopMaxDepth {
			return 0, desktopInvalid(contract.ReasonBadPath)
		}
		return height, nil
	}
	if depth > desktopMaxDepth {
		return 0, desktopInvalid(contract.ReasonBadPath)
	}
	entries, err := w.tree(id)
	if err != nil {
		return 0, err
	}
	height := 0
	for _, e := range entries {
		childHeight := 0
		if e.mode == "40000" {
			childHeight, err = w.walk(e.id, depth+1)
		} else {
			if depth+1 > desktopMaxDepth {
				return 0, desktopInvalid(contract.ReasonBadPath)
			}
			_, err = w.read(e.id, "blob")
		}
		if err != nil {
			return 0, err
		}
		height = max(height, childHeight+1)
	}
	w.heights[id] = height
	return height, nil
}
