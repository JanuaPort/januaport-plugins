package client

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1392: Reine Python-Wheels (py3 / py2.py3, ABI none, Plattform any) werden angenommen, wenn ihr Hash im Lock
// steht. Die Tag-Regel ist streng (SEC W3); native Wheels bleiben unverändert.

func lockFor(name string, data []byte) []byte {
	parts := strings.Split(strings.TrimSuffix(name, ".whl"), "-")
	return []byte(parts[0] + "==" + parts[1] + " --hash=sha256:" + sum(data))
}

func TestReineWheelsTagTabelle(t *testing.T) {
	data := []byte("wheel")
	for _, tc := range []struct {
		name string
		py   string
		ok   bool
	}{
		// angenommen: reine Wheels
		{"demo-1.0-py3-none-any.whl", "3.12.10", true},
		{"demo-1.0-py2.py3-none-any.whl", "3.12.10", true},
		{"demo-1.0-py3-none-any.whl", "3.13.9", true},
		{"demo-1.0-py2.py3-none-any.whl", "3.13.9", true},
		{"demo-1.0-1-py3-none-any.whl", "3.12.10", true}, // sechs Teile: Build-Tag
		// angenommen: unverändert native und cp312 ohne Plattform
		{"demo-1.0-cp312-none-any.whl", "3.12.10", true},
		{"demo-1.0-cp312-cp312-win_amd64.whl", "3.12.10", true},
		{"demo-1.0-cp312-abi3-win_amd64.whl", "3.13.9", true},
		{"demo-1.0-cp313-cp313-win_amd64.whl", "3.13.9", true},
		// abgelehnt: Ticket
		{"demo-1.0-py3-none-win_amd64.whl", "3.12.10", false},
		{"demo-1.0-py3-abi3-any.whl", "3.12.10", false},
		{"demo-1.0-py2-none-any.whl", "3.12.10", false},
		{"demo-1.0-py36-none-any.whl", "3.12.10", false},
		{"demo-1.0-cp311-none-any.whl", "3.12.10", false},
		// abgelehnt: W3 (streng zerlegt, Groß-/Kleinschreibung zählt, keine zusammengesetzte Plattform)
		{"demo-1.0-py2.py3-none-any.win32.whl", "3.12.10", false},
		{"demo-1.0-PY3-none-any.whl", "3.12.10", false},
		{"demo-1.0-py3.py36-none-any.whl", "3.12.10", false},
		{"demo-1.0-py3-none-Any.whl", "3.12.10", false},
		{"demo-1.0-py3-None-any.whl", "3.12.10", false},
		{"demo-1.0-py3-none-any.any.whl", "3.12.10", false},
		{"demo-1.0-py3-none.any-any.whl", "3.12.10", false},
		{"demo-1.0-py3.-none-any.whl", "3.12.10", false},
		{"demo-1.0-py2.py3.py4-none-any.whl", "3.12.10", false},
		{"demo-1.0-py3-cp312-any.whl", "3.12.10", false},
		{"demo-1.0-py3-none-manylinux_x86_64.whl", "3.12.10", false},
		// abgelehnt: Anzahl der Teile
		{"demo-1.0-none-any.whl", "3.12.10", false},
		{"demo-1.0-1-2-py3-none-any.whl", "3.12.10", false},
		{"demo-1.0-py3-none-any.tar.gz", "3.12.10", false},
	} {
		t.Run(tc.name+"/"+tc.py, func(t *testing.T) {
			err := CheckWheels(lockFor("demo-1.0-x.whl", data), map[string][]byte{tc.name: data}, tc.py)
			if tc.ok && err != nil {
				t.Fatalf("abgelehnt: %v", err)
			}
			if !tc.ok && err != ErrHash {
				t.Fatalf("angenommen oder falscher Fehler: %v", err)
			}
		})
	}
}

// Auch ein reines Wheel braucht seinen Hash im Lock; die Tag-Regel ersetzt ihn nicht.
func TestReinesWheelBrauchtHashImLock(t *testing.T) {
	name := "demo-1.0-py3-none-any.whl"
	if e := CheckWheels(lockFor(name, []byte("anders")), map[string][]byte{name: []byte("wheel")}, "3.12.10"); e != ErrHash {
		t.Fatal("reines Wheel ohne passenden Hash angenommen:", e)
	}
	if e := CheckWheels(lockFor(name, []byte("wheel")), map[string][]byte{name: []byte("wheel")}, "3.12.10"); e != nil {
		t.Fatal("positive Kontrolle:", e)
	}
}

// Die echten Dateinamen aus uia_probe: comtypes, pywinauto, six (alle reine Wheels), pywin32 nativ.
func TestUiaProbeWheelNamen(t *testing.T) {
	names := []string{
		"comtypes-1.4.11-py3-none-any.whl",
		"pywinauto-0.6.9-py2.py3-none-any.whl",
		"six-1.17.0-py2.py3-none-any.whl",
		"pywin32-312-cp312-cp312-win_amd64.whl",
	}
	wheels := map[string][]byte{}
	var lock strings.Builder
	for _, n := range names {
		b := []byte("inhalt " + n)
		wheels[n] = b
		lock.Write(lockFor(n, b))
		lock.WriteString("\n")
	}
	if e := CheckWheels([]byte(lock.String()), wheels, "3.12.10"); e != nil {
		t.Fatal(e)
	}
}

func zipBytes(t *testing.T, files map[string]string, order ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range order {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// SEC W2: Für reine Wheels gibt es keinen neuen Entpackweg. Die Pfadprüfung gilt wie bei jedem Wheel, und
// `.data/scripts` landet unverändert unter libs, nirgends sonst.
func TestReinesWheelEntpacktNurUnterLibs(t *testing.T) {
	name := "demo-1.0-py3-none-any.whl"
	t.Run("../-Eintrag wird abgelehnt", func(t *testing.T) {
		for _, evil := range []string{"../evil.py", "demo/../../evil.py", "/evil.py", "..\\evil.py"} {
			whl := zipBytes(t, map[string]string{"demo/__init__.py": "", evil: "boese"}, "demo/__init__.py", evil)
			if e := CheckWheels(lockFor(name, whl), map[string][]byte{name: whl}, "3.12.10"); e != nil {
				t.Fatalf("Tag-Regel müsste das Wheel durchlassen: %v", e)
			}
			base := t.TempDir()
			libs := filepath.Join(base, "libs")
			if err := os.Mkdir(libs, 0o700); err != nil {
				t.Fatal(err)
			}
			var total int64
			if e := extract(libs, whl, map[string]string{}, &total); e != ErrHash {
				t.Fatalf("%q: Entpacken lehnt nicht ab: %v", evil, e)
			}
			entries, _ := os.ReadDir(base)
			if len(entries) != 1 || entries[0].Name() != "libs" {
				t.Fatalf("%q: Datei außerhalb von libs: %v", evil, entries)
			}
		}
	})
	t.Run("data/scripts bleibt unter libs", func(t *testing.T) {
		whl := zipBytes(t, map[string]string{
			"demo/__init__.py":               "",
			"demo-1.0.data/scripts/tool.py":  "print(1)",
			"demo-1.0.data/purelib/extra.py": "",
			"demo.pth":                       "import os\n",
		}, "demo/__init__.py", "demo-1.0.data/scripts/tool.py", "demo-1.0.data/purelib/extra.py", "demo.pth")
		base := t.TempDir()
		libs := filepath.Join(base, "libs")
		if err := os.Mkdir(libs, 0o700); err != nil {
			t.Fatal(err)
		}
		var total int64
		if e := extract(libs, whl, map[string]string{}, &total); e != nil {
			t.Fatal(e)
		}
		for _, want := range []string{"demo/__init__.py", "demo-1.0.data/scripts/tool.py", "demo-1.0.data/purelib/extra.py", "demo.pth"} {
			if _, err := os.Stat(filepath.Join(libs, filepath.FromSlash(want))); err != nil {
				t.Errorf("%s fehlt unter libs: %v", want, err)
			}
		}
		entries, _ := os.ReadDir(base)
		if len(entries) != 1 {
			t.Fatalf("etwas liegt neben libs: %v", entries)
		}
	})
}

// SEC W1: Dass `.pth` in Wheels wirkungslos bleibt, trägt allein die `._pth` des Läufers. Sie ist festgepinnt:
// keine `import`-Zeile, nur die bekannten Pfade.
func TestPthDateiIstFestgepinnt(t *testing.T) {
	for _, tc := range []struct {
		py, name, want string
	}{
		{"3.12.10", "python312._pth", "python312.zip\n.\n../libs\n"},
		{"3.13.9", "python313._pth", "python313.zip\n.\n../libs\n"},
	} {
		name, content := pthFile(tc.py)
		if name != tc.name || string(content) != tc.want {
			t.Fatalf("%s: %q %q", tc.py, name, content)
		}
		for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "import") || strings.Contains(line, "site") {
				t.Fatalf("%s: Zeile %q schaltet site oder Code ein", tc.py, line)
			}
		}
	}
}
