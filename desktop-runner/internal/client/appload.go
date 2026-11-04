package client

import (
	"bytes"
	"encoding/binary"
	"errors"
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

var errHead = errors.New("html: kein sauberes head oder eigene CSP")

// headScan beschreibt, wo das Literal hingehört und ob schon eine CSP-Meta existiert.
type headScan struct {
	headEnd  int // Byteposition direkt hinter dem <head>-Starttag
	cspMetas int // Zahl der <meta http-equiv="content-security-policy"> im ganzen Dokument
}

// scanHead liest das Dokument mit dem HTML-Tokenizer. Sauber heißt: genau ein <head>-Starttag, davor
// nur Doctype, Kommentare, Leerraum und <html>. Alles andere (Text, Skript, Meta, Selbstschluss)
// vor dem Head macht das Dokument unsauber; der Browser würde es sonst anders gliedern als wir.
func scanHead(b []byte) (headScan, bool) {
	z := html.NewTokenizer(bytes.NewReader(b))
	var s headScan
	pos, heads := 0, 0
	for {
		tt := z.Next()
		raw := z.Raw()
		pos += len(raw)
		switch tt {
		case html.ErrorToken:
			return s, z.Err() == io.EOF && heads == 1
		case html.EndTagToken:
			if heads == 0 {
				return s, false
			}
		case html.TextToken:
			if heads == 0 && len(bytes.TrimSpace(raw)) != 0 {
				return s, false
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			switch {
			case t.Data == "head" && tt == html.StartTagToken && heads == 0:
				heads++
				s.headEnd = pos
			case t.Data == "head":
				return s, false
			case t.Data == "meta" && isCSPMeta(t):
				s.cspMetas++
			case heads == 0 && t.Data != "html":
				return s, false
			}
		}
	}
}

func isCSPMeta(t html.Token) bool {
	for _, a := range t.Attr {
		if a.Key == "http-equiv" && strings.EqualFold(strings.TrimSpace(a.Val), "content-security-policy") {
			return true
		}
	}
	return false
}

// injectCSP setzt das Literal als erstes Element in <head> ein. Dokumente mit eigener CSP-Meta
// oder ohne sauberes <head> werden abgewiesen. Der Tokenizer ist nur der Vorfilter: Die eigentliche
// Prüfung ist die Parser-Sicht auf das fertige Dokument (parserSiehtLiteral), denn Chromium
// verwirft eine CSP-Meta, die der Parser nicht in <head> einordnet (SEC-R4 F1).
func injectCSP(b []byte) ([]byte, error) {
	s, ok := scanHead(b)
	if !ok || s.cspMetas != 0 {
		return nil, errHead
	}
	meta := fenster.CSPMeta()
	out := make([]byte, 0, len(b)+len(meta))
	out = append(out, b[:s.headEnd]...)
	out = append(out, meta...)
	out = append(out, b[s.headEnd:]...)
	if !parserSiehtLiteral(out) {
		return nil, errHead
	}
	return out, nil
}

// leadingCSP prüft beim Öffnen: genau eine CSP-Meta im Dokument, sie ist genau unser Literal direkt
// hinter dem <head>-Starttag, und der HTML-Parser ordnet sie als erstes Element in <head> ein.
func leadingCSP(b []byte) bool {
	s, ok := scanHead(b)
	return ok && s.cspMetas == 1 && bytes.HasPrefix(b[s.headEnd:], []byte(fenster.CSPMeta())) && parserSiehtLiteral(b)
}

// parserSiehtLiteral baut den Baum wie ein Browser: Das erste Element-Kind von <head> ist die Meta mit
// genau fenster.CSP, und im ganzen Dokument gibt es genau eine CSP-Meta.
func parserSiehtLiteral(b []byte) bool {
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return false
	}
	var head *html.Node
	count := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "head" && head == nil && n.Parent != nil && n.Parent.Data == "html" {
				head = n
			}
			if n.Data == "meta" && nodeIsCSPMeta(n) {
				count++
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if head == nil || count != 1 {
		return false
	}
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		return c.Data == "meta" && nodeIsCSPMeta(c) && nodeContent(c) == fenster.CSP
	}
	return false
}

func nodeIsCSPMeta(n *html.Node) bool {
	for _, a := range n.Attr {
		if a.Key == "http-equiv" && strings.EqualFold(strings.TrimSpace(a.Val), "content-security-policy") {
			return true
		}
	}
	return false
}

// nodeContent liefert content nur, wenn das Attribut genau einmal vorkommt.
func nodeContent(n *html.Node) string {
	value, seen := "", 0
	for _, a := range n.Attr {
		if a.Key == "content" {
			value, seen = a.Val, seen+1
		}
	}
	if seen != 1 {
		return ""
	}
	return value
}
