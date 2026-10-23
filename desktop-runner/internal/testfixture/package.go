// Package testfixture constructs synthetic Git streams for adapter tests only.
package testfixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type obj struct {
	kind byte
	data []byte
}

func Package(files map[string][]byte, modeOverride string) (string, []byte) {
	var objects []obj
	add := func(kind byte, data []byte) string {
		typ := map[byte]string{'b': "blob", 'c': "commit", 't': "tree"}[kind]
		h := sha256.Sum256(append([]byte(fmt.Sprintf("%s %d\x00", typ, len(data))), data...))
		objects = append(objects, obj{kind, data})
		return hex.EncodeToString(h[:])
	}
	var tree func(string) string
	tree = func(prefix string) string {
		names := map[string]bool{}
		for file := range files {
			if strings.HasPrefix(file, prefix) {
				name, _, _ := strings.Cut(strings.TrimPrefix(file, prefix), "/")
				names[name] = true
			}
		}
		sorted := make([]string, 0, len(names))
		for n := range names {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		var raw bytes.Buffer
		for _, name := range sorted {
			data, ok := files[prefix+name]
			mode := "40000"
			var id string
			if ok {
				mode = "100644"
				if modeOverride != "" {
					mode = modeOverride
				}
				id = add('b', data)
			} else {
				id = tree(prefix + name + "/")
			}
			raw.WriteString(mode + " " + name + "\x00")
			b, _ := hex.DecodeString(id)
			raw.Write(b)
		}
		return add('t', raw.Bytes())
	}
	pin := tree("")
	b, _ := hex.DecodeString(pin)
	root := add('t', append([]byte("40000 demo\x00"), b...))
	commit := add('c', []byte("tree "+root+"\n\ntest\n"))
	head, _ := json.Marshal(map[string]any{"format": "gitobjects", "version": 1, "algo": "sha256", "commit": commit, "path": "demo", "objects": len(objects)})
	var out bytes.Buffer
	write := func(b []byte) { binary.Write(&out, binary.BigEndian, uint32(len(b))); out.Write(b) }
	write(head)
	for _, o := range objects {
		write(append([]byte{o.kind}, o.data...))
	}
	return commit, out.Bytes()
}
