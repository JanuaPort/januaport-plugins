package libs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

const (
	h1 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	h2 = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

func TestParseLockValid(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Entry
	}{
		{"ein Hash mit Fortsetzungszeile", "numpy==2.5.4 \\\n    --hash=sha256:" + h1 + "\n",
			[]Entry{{Name: "numpy", Version: "2.5.4", Hashes: []string{h1}}}},
		{"zwei Hashes, mehrere Zeilen", "numpy==2.5.4 \\\n    --hash=sha256:" + h1 + " \\\n    --hash=sha256:" + h2 + "\n",
			[]Entry{{Name: "numpy", Version: "2.5.4", Hashes: []string{h1, h2}}}},
		{"alles in einer Zeile", "numpy==2.5.4 --hash=sha256:" + h1 + "\n",
			[]Entry{{Name: "numpy", Version: "2.5.4", Hashes: []string{h1}}}},
		{"Kommentare und Leerzeilen", "# erzeugt mit pip hash\n\nsix==1.17.0 \\\n    --hash=sha256:" + h1 + "\n    # via python-dateutil\n\n",
			[]Entry{{Name: "six", Version: "1.17.0", Hashes: []string{h1}}}},
		{"Kommentar nach dem Hash", "six==1.17.0 --hash=sha256:" + h1 + " # via x\n",
			[]Entry{{Name: "six", Version: "1.17.0", Hashes: []string{h1}}}},
		{"CRLF", "six==1.17.0 \\\r\n    --hash=sha256:" + h1 + "\r\n",
			[]Entry{{Name: "six", Version: "1.17.0", Hashes: []string{h1}}}},
		{"Name normalisiert (PEP 503)", "Python_Dateutil==2.9.0.post0 --hash=sha256:" + h1 + "\ntyping.Extensions==4.12.2 --hash=sha256:" + h2 + "\n",
			[]Entry{{Name: "python-dateutil", Version: "2.9.0.post0", Hashes: []string{h1}}, {Name: "typing-extensions", Version: "4.12.2", Hashes: []string{h2}}}},
		{"Version mit lokalem Teil", "torch==2.1.0+cpu --hash=sha256:" + h1 + "\n",
			[]Entry{{Name: "torch", Version: "2.1.0+cpu", Hashes: []string{h1}}}},
		{"ohne Zeilenende am Schluss", "six==1.17.0 --hash=sha256:" + h1,
			[]Entry{{Name: "six", Version: "1.17.0", Hashes: []string{h1}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lock, err := ParseLock([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(lock.Entries) != len(tt.want) {
				t.Fatalf("Einträge = %+v", lock.Entries)
			}
			for i, w := range tt.want {
				g := lock.Entries[i]
				if g.Name != w.Name || g.Version != w.Version || strings.Join(g.Hashes, ",") != strings.Join(w.Hashes, ",") {
					t.Errorf("Eintrag %d = %+v, want %+v", i, g, w)
				}
			}
			s := sha256.Sum256([]byte(tt.in))
			if lock.Hash != hex.EncodeToString(s[:]) {
				t.Errorf("Hash = %s, want SHA-256 der Datei", lock.Hash)
			}
		})
	}
}

func TestParseLockRejects(t *testing.T) {
	ok := "six==1.17.0 --hash=sha256:" + h1 + "\n"
	tests := []struct {
		name string
		in   string
	}{
		{"leer", ""},
		{"nur Kommentare", "# nichts\n\n"},
		{"ohne Hash", "six==1.17.0\n"},
		{"Hash allein", "--hash=sha256:" + h1 + "\n"},
		{"Hash-Zeile vor dem Eintrag", "--hash=sha256:" + h1 + "\n" + ok},
		{"größer gleich", "six>=1.0 --hash=sha256:" + h1 + "\n"},
		{"dreifaches Gleich", "six===1.17.0 --hash=sha256:" + h1 + "\n"},
		{"Platzhalter", "six==1.* --hash=sha256:" + h1 + "\n"},
		{"ohne Version", "six== --hash=sha256:" + h1 + "\n"},
		{"Extras", "six[extra]==1.17.0 --hash=sha256:" + h1 + "\n"},
		{"Marker", "six==1.17.0 ; python_version<'3.13' --hash=sha256:" + h1 + "\n"},
		{"Marker ohne Leerzeichen", "six==1.17.0;python_version<'3.13' --hash=sha256:" + h1 + "\n"},
		{"URL mit @", "six @ https://example.com/six-1.0-py3-none-any.whl --hash=sha256:" + h1 + "\n"},
		{"nackte URL", "https://example.com/six-1.0-py3-none-any.whl --hash=sha256:" + h1 + "\n"},
		{"index-url", "--index-url https://example.com/simple\n" + ok},
		{"extra-index-url", "--extra-index-url=https://example.com/simple\n" + ok},
		{"find-links", "--find-links /tmp/x\n" + ok},
		{"no-binary", "--no-binary :all:\n" + ok},
		{"require-hashes im Lock", "--require-hashes\n" + ok},
		{"-r", "-r other.txt\n" + ok},
		{"-e", "-e .\n" + ok},
		{"md5", "six==1.17.0 --hash=md5:0123456789abcdef0123456789abcdef\n"},
		{"sha512", "six==1.17.0 --hash=sha512:" + h1 + h2 + "\n"},
		{"Hash groß", "six==1.17.0 --hash=sha256:" + strings.ToUpper(h1) + "\n"},
		{"Hash zu kurz", "six==1.17.0 --hash=sha256:" + h1[:63] + "\n"},
		{"Hash zu lang", "six==1.17.0 --hash=sha256:" + h1 + "0\n"},
		{"Hash kein Hex", "six==1.17.0 --hash=sha256:" + strings.Repeat("g", 64) + "\n"},
		{"Hash doppelt im Eintrag", "six==1.17.0 --hash=sha256:" + h1 + " --hash=sha256:" + h1 + "\n"},
		{"Name doppelt", ok + "six==1.18.0 --hash=sha256:" + h2 + "\n"},
		{"Name doppelt nach Normalisierung", "Foo-Bar==1.0 --hash=sha256:" + h1 + "\nfoo_bar==1.0 --hash=sha256:" + h2 + "\n"},
		{"fremdes Token", "six==1.17.0 --hash=sha256:" + h1 + " --other\n"},
		{"Fortsetzung am Dateiende", "six==1.17.0 \\\n"},
		{"NUL", "six==1.17.0 --hash=sha256:" + h1 + "\x00\n"},
		{"Nicht-ASCII", "sïx==1.17.0 --hash=sha256:" + h1 + "\n"},
		{"ungültiges UTF-8", "six==1.17.0 --hash=sha256:" + h1 + "\xff\n"},
		{"Steuerzeichen", "six==1.17.0\x01 --hash=sha256:" + h1 + "\n"},
		{"Name beginnt mit Bindestrich", "-six==1.17.0 --hash=sha256:" + h1 + "\n"},
		{"Name mit Pfad", "../six==1.17.0 --hash=sha256:" + h1 + "\n"},
		{"Zeile über 4096 Zeichen", "six==1.17.0 --hash=sha256:" + h1 + " #" + strings.Repeat("x", 4100) + "\n"},
		{"mehr als 256 Einträge", manyEntries(257)},
		{"Datei über 1 MiB", ok + "#" + strings.Repeat("x", 1<<20) + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseLock([]byte(tt.in))
			if err == nil {
				t.Fatal("angenommen")
			}
			if r := reasonOf(err); r != contract.ReasonLibsInvalid {
				t.Fatalf("Grund = %q (%v), want %s", r, err, contract.ReasonLibsInvalid)
			}
		})
	}
}

func TestParseLockAcceptsExactly256Entries(t *testing.T) {
	lock, err := ParseLock([]byte(manyEntries(256)))
	if err != nil || len(lock.Entries) != 256 {
		t.Fatalf("%d Einträge, err = %v", len(lock.Entries), err)
	}
}

func manyEntries(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "pkg%d==1.0 --hash=sha256:%s\n", i, h1)
	}
	return b.String()
}

var (
	normNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	hashRe     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Der Parser nimmt nichts an, was die Eigenschaften unten verletzt, und
// stürzt bei keiner Eingabe ab.
func FuzzParseLock(f *testing.F) {
	for _, s := range []string{
		"six==1.17.0 --hash=sha256:" + h1 + "\n",
		"numpy==2.5.4 \\\n    --hash=sha256:" + h1 + " \\\n    --hash=sha256:" + h2 + "\n# via x\n",
		"--index-url https://x\n",
		"a==1 --hash=sha256:" + h1 + "\nb==2 --hash=sha256:" + h2,
		"\\\n\\\n",
		"x==1 \\\r\n --hash=sha256:" + h1,
		"",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		lock, err := ParseLock(data)
		if err != nil {
			if r := reasonOf(err); r != contract.ReasonLibsInvalid {
				t.Fatalf("Fehler ohne libs_invalid: %v", err)
			}
			return
		}
		s := sha256.Sum256(data)
		if lock.Hash != hex.EncodeToString(s[:]) {
			t.Fatalf("Hash weicht ab")
		}
		if len(lock.Entries) == 0 || len(lock.Entries) > 256 {
			t.Fatalf("%d Einträge angenommen", len(lock.Entries))
		}
		seen := map[string]bool{}
		for _, e := range lock.Entries {
			if !normNameRe.MatchString(e.Name) || e.Version == "" || seen[e.Name] {
				t.Fatalf("ungültiger Eintrag %+v", e)
			}
			seen[e.Name] = true
			if len(e.Hashes) == 0 {
				t.Fatalf("Eintrag ohne Hash: %+v", e)
			}
			hs := map[string]bool{}
			for _, h := range e.Hashes {
				if !hashRe.MatchString(h) || hs[h] {
					t.Fatalf("ungültiger Hash %q in %+v", h, e)
				}
				hs[h] = true
			}
		}
	})
}
