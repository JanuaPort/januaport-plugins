package client

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

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
	if !textSicher(b) || spekulativesLaden(b) {
		return nil, errHead
	}
	s, ok := scanHead(b)
	if !ok || s.cspMetas != 0 {
		return nil, errHead
	}
	prefix := fenster.HeadPrefix()
	out := make([]byte, 0, len(b)+len(prefix))
	out = append(out, b[:s.headEnd]...)
	out = append(out, prefix...)
	out = append(out, b[s.headEnd:]...)
	if !parserSiehtLiteral(out) {
		return nil, errHead
	}
	return out, nil
}

// textSicher ist die zweite Schicht gegen Zeichensatz-Tricks (SEC-R4 F4): Der Browser dekodiert vor dem Parsen,
// wir lesen Bytes. Ein ESC-Byte (ISO-2022-Umschaltung), ein NUL oder ungültiges UTF-8 kommt nicht in die Ablage.
func textSicher(b []byte) bool {
	return utf8.Valid(b) && bytes.IndexByte(b, 0x1b) < 0 && bytes.IndexByte(b, 0) < 0
}

// leadingCSP prüft beim Öffnen: genau eine CSP-Meta im Dokument, sie ist genau unser Literal direkt
// hinter dem <head>-Starttag, und der HTML-Parser ordnet sie als erstes Element in <head> ein.
func leadingCSP(b []byte) bool {
	if !textSicher(b) {
		return false
	}
	s, ok := scanHead(b)
	return ok && s.cspMetas == 1 && bytes.HasPrefix(b[s.headEnd:], []byte(fenster.HeadPrefix())) && parserSiehtLiteral(b)
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
	var first []*html.Node
	for c := head.FirstChild; c != nil && len(first) < 2; c = c.NextSibling {
		if c.Type == html.ElementNode {
			first = append(first, c)
		}
	}
	return len(first) == 2 && nodeIsCharsetUTF8(first[0]) && first[1].Data == "meta" && nodeIsCSPMeta(first[1]) && nodeContent(first[1]) == fenster.CSP
}

// nodeIsCharsetUTF8: die Zeichensatz-Meta des Läufers, genau <meta charset="utf-8"> ohne weitere Attribute.
func nodeIsCharsetUTF8(n *html.Node) bool {
	return n.Data == "meta" && len(n.Attr) == 1 && n.Attr[0].Key == "charset" && n.Attr[0].Val == "utf-8"
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

// spekulativesLaden erkennt Seiten, die den Browser zum Vorabholen auffordern: <link rel> mit prerender,
// prefetch, preconnect oder dns-prefetch (Token, Groß- und Kleinschreibung egal) und <script
// type="speculationrules">. Das ist die zweite Schicht neben den Browserargumenten und dem Ressourcenfilter;
// dynamisch per Skript erzeugte Link-Elemente deckt sie nicht ab, dafür halten die Schichten der Oberfläche.
func spekulativesLaden(b []byte) bool {
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return true
	}
	found := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found {
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "link":
				for _, a := range n.Attr {
					if a.Key != "rel" {
						continue
					}
					for _, token := range strings.Fields(strings.ToLower(a.Val)) {
						switch token {
						case "prerender", "prefetch", "preconnect", "dns-prefetch":
							found = true
						}
					}
				}
			case "script":
				for _, a := range n.Attr {
					if a.Key == "type" && strings.EqualFold(strings.TrimSpace(a.Val), "speculationrules") {
						found = true
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}
