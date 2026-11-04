// Kopie aus script-runner/internal/apphost/package.go (JanuaPort/januaport#1048 K6b-1): MIME-Liste,
// Geheimnismuster und Markupprüfung sind wortgleich; loadAppPackage ist die Anpassung von loadPackage
// an die Läufer-Pakete (Vertrag 5, decodePackage statt gitobjects.Objects, rohe Bytes statt Assets).
// Die CSP der apphost (HTTP-Header) wird nicht übernommen; stattdessen setzt der Läufer nach der
// Paketprüfung das Meta-Literal fenster.CSP als erstes Element in jedes <head> (appload.go).
// Wächter: TestCopiedApphostFunctions.
package client

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

var mimeTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".css": "text/css; charset=utf-8",
	".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
	".svg": "image/svg+xml", ".json": "application/json", ".map": "application/json",
	".txt": "text/plain; charset=utf-8", ".webmanifest": "application/manifest+json",
}

// Public, deliberately small policy; not a copy of any gateway redactor.
var secretPattern = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----|-----BEGIN CERTIFICATE-----|jnpt[a-zA-Z0-9]*_[a-zA-Z0-9_-]+|AKIA[A-Z0-9]+|ghp_[a-zA-Z0-9]+|xox[a-zA-Z]-[a-zA-Z0-9-]+|sk-[a-zA-Z0-9_-]+`)

// loadAppPackage verifiziert das Gitobjekt-Paket vollständig (SHA-256 bzw. SHA-1DC, Objektmenge,
// Namen) und liefert danach die Dateien des App-Baums, jede nach den apphost-Regeln geprüft.
func loadAppPackage(b []byte, app App) (map[string][]byte, error) {
	if !validAppEntry(app) || decodePackage(b, "sha256", app.Commit, app.Path) != nil {
		return nil, fmt.Errorf("package: integrity")
	}
	objects := packageObjects(b, "sha256")
	first, _, _ := strings.Cut(string(objects[app.Commit].data), "\n")
	root := strings.TrimPrefix(first, "tree ")
	files := map[string][]byte{}
	total, count := 0, 0
	var walk func(string, []string, string) error
	walk = func(id string, remaining []string, prefix string) error {
		data := objects[id].data
		for len(data) > 0 {
			n := bytes.IndexByte(data, 0)
			if n < 0 || len(data) < n+33 {
				return fmt.Errorf("package: tree")
			}
			mode, name, _ := strings.Cut(string(data[:n]), " ")
			child := hex.EncodeToString(data[n+1 : n+33])
			data = data[n+33:]
			if len(remaining) > 0 {
				if name == remaining[0] {
					if e := walk(child, remaining[1:], ""); e != nil {
						return e
					}
				}
				continue
			}
			file := prefix + name
			if mode == "40000" {
				if e := walk(child, nil, file+"/"); e != nil {
					return e
				}
				continue
			}
			if mode != "100644" {
				return fmt.Errorf("%s: mode", file)
			}
			content := objects[child].data
			total += len(content)
			count++
			if total > 4<<20 {
				return fmt.Errorf("%s: bytes", file)
			}
			if count > 1000 {
				return fmt.Errorf("%s: files", file)
			}
			if _, ok := mimeTypes[path.Ext(file)]; !ok {
				return fmt.Errorf("%s: mime", file)
			}
			if secretPattern.Match(content) {
				return fmt.Errorf("%s: secret", file)
			}
			if path.Ext(file) == ".html" || path.Ext(file) == ".svg" {
				if !safeMarkup(content) {
					return fmt.Errorf("%s: active_markup", file)
				}
			}
			if path.Ext(file) == ".html" {
				var err error
				if content, err = injectCSP(content); err != nil {
					return fmt.Errorf("%s: csp", file)
				}
			}
			if path.Base(file) != "jnpt.app.json" {
				files[file] = content
			}
		}
		return nil
	}
	if err := walk(root, strings.Split(app.Path, "/"), ""); err != nil {
		return nil, err
	}
	if _, ok := files["index.html"]; !ok {
		return nil, fmt.Errorf("index.html: required")
	}
	return files, nil
}

func safeMarkup(b []byte) bool {
	z := html.NewTokenizer(bytes.NewReader(b))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return z.Err() == io.EOF
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			src := false
			for _, a := range t.Attr {
				if strings.HasPrefix(strings.ToLower(a.Key), "on") {
					return false
				}
				value := strings.Map(func(r rune) rune {
					if unicode.IsSpace(r) || unicode.IsControl(r) {
						return -1
					}
					return unicode.ToLower(r)
				}, a.Val)
				if strings.HasPrefix(value, "javascript:") {
					return false
				}
				if a.Key == "src" && strings.TrimSpace(a.Val) != "" {
					src = true
				}
			}
			if t.Data == "script" && !src {
				return false
			}
		}
	}
}
