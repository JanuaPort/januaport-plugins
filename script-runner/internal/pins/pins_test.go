package pins

import (
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

var (
	c1 = strings.Repeat("a", 40)
	c2 = strings.Repeat("b", 64)
)

func TestParseValid(t *testing.T) {
	f, err := Parse([]byte(`{"version":1,"repo_url":"ssh://git@git.example.com/org/scripts.git",
	 "pins":[{"name":"invoice_check","path":"invoice-check","commit":"` + c1 + `"},
	         {"name":"second","path":"tools/second","commit":"` + c2 + `"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.RepoURL != "ssh://git@git.example.com/org/scripts.git" {
		t.Fatalf("RepoURL = %q", f.RepoURL)
	}
	if len(f.Pins) != 2 {
		t.Fatalf("%d Pins", len(f.Pins))
	}
	for _, p := range f.Pins {
		if p.Invalid != "" {
			t.Errorf("Pin %s ungültig: %s", p.Name, p.Invalid)
		}
	}
}

// Nachtrag 2a: jede Regel des Lesers.
func TestParseRowRules(t *testing.T) {
	tests := []struct {
		name   string
		row    string
		reason string
	}{
		{"gültig", `{"name":"ok","path":"p","commit":"` + c1 + `"}`, ""},
		{"Name mit Bindestrich", `{"name":"bad-name","path":"p","commit":"` + c1 + `"}`, contract.ReasonManifestInvalid},
		{"Name zu lang", `{"name":"` + strings.Repeat("a", 49) + `","path":"p","commit":"` + c1 + `"}`, contract.ReasonManifestInvalid},
		{"Pfad absolut", `{"name":"ok","path":"/etc","commit":"` + c1 + `"}`, contract.ReasonBadPath},
		{"Pfad mit ..", `{"name":"ok","path":"a/../b","commit":"` + c1 + `"}`, contract.ReasonBadPath},
		{"Pfad mit .git", `{"name":"ok","path":"a/.git/hooks","commit":"` + c1 + `"}`, contract.ReasonBadPath},
		{"kurzer Hash", `{"name":"ok","path":"p","commit":"abc1234"}`, contract.ReasonManifestInvalid},
		{"Branch statt Hash", `{"name":"ok","path":"p","commit":"main"}`, contract.ReasonManifestInvalid},
		{"41 Hex", `{"name":"ok","path":"p","commit":"` + c1 + `a"}`, contract.ReasonManifestInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Parse([]byte(`{"version":1,"repo_url":"ssh://git@git.example.com/o/r.git","pins":[` + tt.row + `]}`))
			if err != nil {
				t.Fatal(err)
			}
			if len(f.Pins) != 1 {
				t.Fatalf("%d Pins", len(f.Pins))
			}
			if f.Pins[0].Invalid != tt.reason {
				t.Fatalf("Invalid = %q, want %q", f.Pins[0].Invalid, tt.reason)
			}
		})
	}
}

func TestParseInvalidRowOnlyAffectsItself(t *testing.T) {
	f, err := Parse([]byte(`{"version":1,"repo_url":"ssh://git@git.example.com/o/r.git","pins":[
	 {"name":"good","path":"p","commit":"` + c1 + `"},
	 {"name":"bad","path":"../x","commit":"` + c1 + `"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Pins[0].Invalid != "" || f.Pins[1].Invalid != contract.ReasonBadPath {
		t.Fatalf("Pins = %+v", f.Pins)
	}
}

func TestParseDuplicateNameInvalidatesBoth(t *testing.T) {
	f, err := Parse([]byte(`{"version":1,"repo_url":"ssh://git@git.example.com/o/r.git","pins":[
	 {"name":"dup","path":"a","commit":"` + c1 + `"},
	 {"name":"other","path":"b","commit":"` + c1 + `"},
	 {"name":"dup","path":"c","commit":"` + c2 + `"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{contract.ReasonManifestInvalid, "", contract.ReasonManifestInvalid}
	for i, p := range f.Pins {
		if p.Invalid != want[i] {
			t.Errorf("Pin %d (%s): Invalid = %q, want %q", i, p.Name, p.Invalid, want[i])
		}
	}
}

func TestParseWrongVersionInvalidatesAll(t *testing.T) {
	for _, v := range []string{`2`, `0`, `"1"`, `null`} {
		f, err := Parse([]byte(`{"version":` + v + `,"repo_url":"ssh://git@git.example.com/o/r.git","pins":[
		 {"name":"a","path":"a","commit":"` + c1 + `"}]}`))
		if err != nil {
			t.Fatalf("version %s: %v", v, err)
		}
		if len(f.Pins) != 1 || f.Pins[0].Invalid != contract.ReasonManifestInvalid {
			t.Errorf("version %s: Pins = %+v", v, f.Pins)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte(`not json`)); err == nil {
		t.Fatal("kein Fehler bei kaputtem JSON")
	}
}

func TestParseKeepsOrder(t *testing.T) {
	f, _ := Parse([]byte(`{"version":1,"repo_url":"ssh://git@git.example.com/o/r.git","pins":[
	 {"name":"z","path":"z","commit":"` + c1 + `"},{"name":"a","path":"a","commit":"` + c1 + `"}]}`))
	if f.Pins[0].Name != "z" || f.Pins[1].Name != "a" {
		t.Fatalf("Reihenfolge verloren: %+v", f.Pins)
	}
}
