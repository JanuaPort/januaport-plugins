package client

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
	"golang.org/x/net/html"
)

// packageObjects liest die Objekte eines bereits von decodePackage geprüften Pakets.
func packageObjects(b []byte, algo string) map[string]object {
	objects := map[string]object{}
	off := 4 + int(binary.BigEndian.Uint32(b[:4]))
	for off < len(b) {
		n := int(binary.BigEndian.Uint32(b[off : off+4]))
		off += 4
		o := object{b[off], b[off+1 : off+n]}
		id, _ := objectID(algo, o)
		objects[id] = o
		off += n
	}
	return objects
}

// hasExactCSPMeta verlangt genau ein CSP-Meta-Element mit dem Literal fenster.CSP, bevor irgendein
// Element erscheint, das Ressourcen laden könnte: Eine Meta-CSP gilt erst für Inhalt nach ihr.
func hasExactCSPMeta(b []byte) bool {
	z := html.NewTokenizer(bytes.NewReader(b))
	found := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return z.Err() == io.EOF && found == 1
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			if t.Data == "meta" {
				if !isCSPMeta(t) {
					continue
				}
				if found > 0 || !exactCSPContent(t) {
					return false
				}
				found++
				continue
			}
			if found == 0 && t.Data != "html" && t.Data != "head" && t.Data != "title" {
				return false
			}
		}
	}
}

func isCSPMeta(t html.Token) bool {
	for _, a := range t.Attr {
		if a.Key == "http-equiv" && strings.EqualFold(a.Val, "content-security-policy") {
			return true
		}
	}
	return false
}

func exactCSPContent(t html.Token) bool {
	values := 0
	exact := false
	for _, a := range t.Attr {
		if a.Key == "content" {
			values++
			exact = a.Val == fenster.CSP
		}
	}
	return values == 1 && exact
}
