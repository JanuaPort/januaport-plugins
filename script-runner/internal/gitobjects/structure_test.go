package gitobjects

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"sort"
	"testing"
)

// Construct correctly hashed packages: rejection must come from the graph
// and naming rules, not an unrelated broken signature or frame.
func TestStoragePackageForbiddenGraphs(t *testing.T) {
	for _, kind := range []string{"valid", "symlink", "gitlink", "mode", "unsafe_name", "case_collision", "duplicate", "extra", "missing", "wrong_type", "bad_frame", "absent_pin"} {
		t.Run(kind, func(t *testing.T) {
			blob := object{'b', []byte("print('ok')\n")}
			id, e := objectID("sha256", blob)
			if e != nil {
				t.Fatal(e)
			}
			idb, _ := hex.DecodeString(id)
			mode, name := "100644", "main.py"
			switch kind {
			case "symlink":
				mode = "120000"
			case "gitlink":
				mode = "160000"
			case "mode":
				mode = "100600"
			case "unsafe_name":
				name = "../escape"
			}
			data := append([]byte(mode+" "+name+"\x00"), idb...)
			if kind == "case_collision" {
				data = append(data, append([]byte("100644 MAIN.py\x00"), idb...)...)
			}
			if kind == "bad_frame" {
				data = data[:len(data)-1]
			}
			tree := object{'t', data}
			treeID, _ := objectID("sha256", tree)
			treeBytes, _ := hex.DecodeString(treeID)
			root := object{'t', append([]byte("40000 scripts\x00"), treeBytes...)}
			rootID, _ := objectID("sha256", root)
			commit := object{'c', []byte("tree " + rootID + "\n\nmessage\n")}
			commitID, _ := objectID("sha256", commit)
			objects := []object{blob, tree, root, commit}
			switch kind {
			case "duplicate":
				objects = append(objects, blob)
			case "extra":
				objects = append(objects, object{'b', []byte("EXTRA-CANARY")})
			case "missing":
				objects = objects[1:]
			case "wrong_type":
				objects[0].kind = 't'
			}
			sort.Slice(objects, func(i, j int) bool {
				a, _ := objectID("sha256", objects[i])
				b, _ := objectID("sha256", objects[j])
				return a < b
			})
			path := "scripts"
			if kind == "absent_pin" {
				path = "absent"
			}
			h, _ := json.Marshal(packageHead{"gitobjects", 1, "sha256", commitID, path, len(objects)})
			var out bytes.Buffer
			frame := func(b []byte) {
				if e := binary.Write(&out, binary.BigEndian, uint32(len(b))); e != nil {
					t.Fatal(e)
				}
				out.Write(b)
			}
			frame(h)
			for _, o := range objects {
				frame(append([]byte{o.kind}, o.data...))
			}
			err := Verify(out.Bytes(), commitID, path)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("unexpected graph result: %v", err)
			}
		})
	}
}

func TestCanonicalRejectsAmbiguousInput(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"a":1,"a":2}`), []byte(`{"a":"\ud800"}`), []byte(`{"a":"\udc00"}`), []byte("\"\xff\""), []byte(`{} {}`)} {
		if _, e := Canonical(raw); e == nil {
			t.Fatal("ambiguous input accepted")
		}
	}
	b, e := Canonical([]byte(`{"b":"<>&","a":1.0}`))
	if e != nil || string(b) != `{"a":1.0,"b":"<>&"}` {
		t.Fatal("canonical bytes", string(b), e)
	}
}
