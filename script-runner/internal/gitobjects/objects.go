package gitobjects

import "encoding/binary"

// Object is an immutable view into a fully verified package's bytes.
type Object struct {
	Type string
	Data []byte
}

// Objects exposes the graph only after the checked contract verifier succeeds.
func Objects(b []byte, commit, path string) (map[string]Object, error) {
	if e := Verify(b, commit, path); e != nil {
		return nil, e
	}
	b = b[4+int(binary.BigEndian.Uint32(b)):]
	objects := map[string]Object{}
	for len(b) > 0 {
		n := int(binary.BigEndian.Uint32(b))
		o := object{b[4], b[5 : 4+n]}
		id, e := objectID("sha256", o)
		if e != nil {
			return nil, ErrHash
		}
		objects[id] = Object{Type: map[byte]string{'c': "commit", 't': "tree", 'b': "blob"}[o.kind], Data: o.data}
		b = b[4+n:]
	}
	return objects, nil
}
