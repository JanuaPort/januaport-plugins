package client

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"strings"
)

// PackageFiles exposes bytes only after the complete contract verifier passed.
// Traversal follows the script-runner store design, with no git subprocess.
func PackageFiles(b []byte, p Pin) (map[string][]byte, error) {
	if err := decodePackage(b, p.Algo, p.Commit, p.Path); err != nil {
		return nil, ErrHash
	}
	objects := map[string]object{}
	off := 4 + int(binary.BigEndian.Uint32(b[:4]))
	for off < len(b) {
		n := int(binary.BigEndian.Uint32(b[off : off+4]))
		off += 4
		o := object{b[off], b[off+1 : off+n]}
		id, _ := objectID(p.Algo, o)
		objects[id] = o
		off += n
	}
	c := objects[p.Commit]
	first, _, _ := strings.Cut(string(c.data), "\n")
	root := strings.TrimPrefix(first, "tree ")
	width := len(p.Commit) / 2
	files := map[string][]byte{}
	count := 0
	total := 0
	var walk func(string, []string, string) error
	walk = func(id string, remaining []string, prefix string) error {
		count++
		if count > 30000 {
			return ErrHash
		}
		data := objects[id].data
		for len(data) > 0 {
			n := bytes.IndexByte(data, 0)
			mode, name, _ := strings.Cut(string(data[:n]), " ")
			child := hex.EncodeToString(data[n+1 : n+1+width])
			data = data[n+1+width:]
			if len(remaining) > 0 {
				if name == remaining[0] {
					if err := walk(child, remaining[1:], ""); err != nil {
						return err
					}
				}
				continue
			}
			rel := prefix + name
			if mode == "40000" {
				if err := walk(child, nil, rel+"/"); err != nil {
					return err
				}
			} else {
				total += len(objects[child].data)
				if len(files) >= 30000 || total > 128<<20 {
					return ErrHash
				}
				files[rel] = objects[child].data
			}
		}
		return nil
	}
	if err := walk(root, strings.Split(p.Path, "/"), ""); err != nil {
		return nil, err
	}
	return files, nil
}
