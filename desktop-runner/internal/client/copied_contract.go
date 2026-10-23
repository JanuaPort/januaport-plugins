// Aus contract/contract_test.go übernommen; gleiche Funktionen werden durch TestCopiedContractFunctions bewacht.
package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/pjbgf/sha1cd"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type packageHead struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Algo    string `json:"algo"`
	Commit  string `json:"commit"`
	Path    string `json:"path"`
	Objects int    `json:"objects"`
}
type object struct {
	kind byte
	data []byte
}

func validID(algo, id string) bool {
	n := 40
	if algo == "sha256" {
		n = 64
	} else if algo != "sha1" {
		return false
	}
	b, err := hex.DecodeString(id)
	return err == nil && len(b)*2 == n && id == strings.ToLower(id)
}
func safeName(name string) bool {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || strings.TrimRight(name, ". ") != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	for i := 1; i <= 9; i++ {
		if base == fmt.Sprint("COM", i) || base == fmt.Sprint("LPT", i) {
			return false
		}
	}
	return true
}
func safePath(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if !safeName(s) {
			return false
		}
	}
	return true
}
func objectID(algo string, o object) (string, error) {
	types := map[byte]string{'c': "commit", 't': "tree", 'b': "blob"}
	typ, ok := types[o.kind]
	if !ok {
		return "", fmt.Errorf("Objekttyp")
	}
	b := append([]byte(fmt.Sprintf("%s %d\x00", typ, len(o.data))), o.data...)
	return digest(algo, b)
}

func digest(algo string, b []byte) (string, error) {
	if algo == "sha256" {
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:]), nil
	}
	if algo != "sha1" {
		return "", fmt.Errorf("Algorithmus")
	}
	h, collision := sha1cd.Sum(b) // SHA-1DC statt crypto/sha1 (SEC C3).
	if collision {
		return "", fmt.Errorf("SHA-1-Kollision")
	}
	return hex.EncodeToString(h[:]), nil
}

func decodePackage(b []byte, algo, commit, pinPath string) error {
	if len(b) > 128*1024*1024 {
		return fmt.Errorf("Paketgröße")
	}
	r := bytes.NewReader(b)
	next := func() ([]byte, error) {
		var n uint32
		if err := binary.Read(r, binary.BigEndian, &n); err != nil {
			return nil, err
		}
		if n == 0 || uint64(n) > uint64(r.Len()) {
			return nil, fmt.Errorf("Rahmenlänge")
		}
		b := make([]byte, int(n))
		_, err := io.ReadFull(r, b)
		return b, err
	}
	b, err := next()
	if err != nil {
		return err
	}
	var h packageHead
	if err := strict(b, &h); err != nil {
		return err
	}
	if h.Format != "gitobjects" || h.Version != 1 || h.Algo != algo || h.Commit != commit || h.Path != pinPath || !validID(algo, commit) || !safePath(pinPath) || h.Objects < 1 || h.Objects > r.Len()/5 {
		return fmt.Errorf("Paketkopf")
	}
	objects := map[string]object{}
	for i := 0; i < h.Objects; i++ {
		b, err := next()
		if err != nil {
			return err
		}
		o := object{b[0], b[1:]}
		id, err := objectID(algo, o)
		if err != nil {
			return err
		}
		if _, ok := objects[id]; ok {
			return fmt.Errorf("doppeltes Objekt")
		}
		objects[id] = o
	}
	if r.Len() != 0 {
		return fmt.Errorf("Daten nach Paket")
	}
	used := map[string]bool{}
	get := func(id string, kind byte) (object, error) {
		o, ok := objects[id]
		if !ok || o.kind != kind {
			return o, fmt.Errorf("fehlendes/falsches Objekt")
		}
		used[id] = true
		return o, nil
	}
	c, err := get(commit, 'c')
	if err != nil {
		return err
	}
	first, _, _ := strings.Cut(string(c.data), "\n")
	if !strings.HasPrefix(first, "tree ") {
		return fmt.Errorf("Commitbaum")
	}
	root := strings.TrimPrefix(first, "tree ")
	if !validID(algo, root) {
		return fmt.Errorf("Baum-ID")
	}
	parts := strings.Split(pinPath, "/")
	// D4 addition: bound repeated DAG expansion as well as depth.
	visits := 0
	var walk func(string, []string, int) error
	walk = func(id string, remaining []string, depth int) error {
		visits++
		if depth > 256 || visits > 30000 {
			return fmt.Errorf("Baumtiefe")
		}
		o, err := get(id, 't')
		if err != nil {
			return err
		}
		data := o.data
		names := map[string]bool{}
		found := false
		width := 20
		if algo == "sha256" {
			width = 32
		}
		for len(data) > 0 {
			n := bytes.IndexByte(data, 0)
			if n < 0 || len(data) < n+1+width {
				return fmt.Errorf("Baumrahmen")
			}
			mode, name, ok := strings.Cut(string(data[:n]), " ")
			if !ok || !safeName(name) {
				return fmt.Errorf("Name")
			}
			fold := strings.Map(func(r rune) rune { return unicode.ToLower(unicode.ToUpper(r)) }, name)
			if names[fold] {
				return fmt.Errorf("Namenskollision")
			}
			names[fold] = true
			if mode != "40000" && mode != "100644" && mode != "100755" {
				return fmt.Errorf("Modus")
			}
			child := hex.EncodeToString(data[n+1 : n+1+width])
			data = data[n+1+width:]
			if len(remaining) > 0 {
				if name != remaining[0] {
					continue
				}
				if mode != "40000" {
					return fmt.Errorf("Pin ist kein Baum")
				}
				found = true
				if err := walk(child, remaining[1:], depth+1); err != nil {
					return err
				}
			} else if mode == "40000" {
				if err := walk(child, nil, depth+1); err != nil {
					return err
				}
			} else if _, err := get(child, 'b'); err != nil {
				return err
			}
		}
		if len(remaining) > 0 && !found {
			return fmt.Errorf("Pin fehlt")
		}
		return nil
	}
	if err := walk(root, parts, 0); err != nil {
		return err
	}
	if len(used) != len(objects) {
		return fmt.Errorf("überzählige Objekte")
	}
	return nil
}
func canonical(b []byte) ([]byte, error) {
	if !utf8.Valid(b) || !validUnicodeEscapes(b) {
		return nil, fmt.Errorf("UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value func() (any, error)
	value = func() (any, error) {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch tok {
		case json.Delim('{'):
			obj := map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, ok := key.(string)
				if !ok {
					return nil, fmt.Errorf("Schlüssel")
				}
				if _, exists := obj[k]; exists {
					return nil, fmt.Errorf("doppelter Schlüssel")
				}
				v, err := value()
				if err != nil {
					return nil, err
				}
				obj[k] = v
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("Objektende")
			}
			return obj, nil
		case json.Delim('['):
			arr := []any{}
			for d.More() {
				v, err := value()
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("Listenende")
			}
			return arr, nil
		default:
			switch tok.(type) {
			case nil, bool, string, json.Number:
				return tok, nil
			}
			return nil, fmt.Errorf("JSON-Wert")
		}
	}
	v, err := value()
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("Daten nach JSON")
	}
	var out bytes.Buffer
	quote := func(s string) {
		var q bytes.Buffer
		e := json.NewEncoder(&q)
		e.SetEscapeHTML(false)
		_ = e.Encode(s)
		out.Write(bytes.TrimSuffix(q.Bytes(), []byte{'\n'}))
	}
	var emit func(any)
	emit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					out.WriteByte(',')
				}
				quote(k)
				out.WriteByte(':')
				emit(x[k])
			}
			out.WriteByte('}')
		case []any:
			out.WriteByte('[')
			for i, item := range x {
				if i > 0 {
					out.WriteByte(',')
				}
				emit(item)
			}
			out.WriteByte(']')
		case string:
			quote(x)
		case json.Number:
			out.WriteString(x.String())
		case bool:
			if x {
				out.WriteString("true")
			} else {
				out.WriteString("false")
			}
		case nil:
			out.WriteString("null")
		}
	}
	emit(v)
	return out.Bytes(), nil
}
func validUnicodeEscapes(b []byte) bool {
	inString := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || b[i] != '\\' {
			continue
		}
		if i+1 >= len(b) {
			return false
		}
		if b[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(b) {
			return false
		}
		n, err := strconv.ParseUint(string(b[i+2:i+6]), 16, 16)
		if err != nil {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+12 > len(b) || b[i+6] != '\\' || b[i+7] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(b[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 11
		} else {
			if n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			i += 5
		}
	}
	return true
}
