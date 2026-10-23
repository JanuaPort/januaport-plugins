package client

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestWheelTagsLockUndManifestVerbote(t *testing.T) {
	data := []byte("wheel")
	lock := []byte("demo==1.0 --hash=sha256:" + sum(data))
	if e := CheckWheels(lock, map[string][]byte{"demo-1.0-cp312-none-any.whl": data}, "3.12.10"); e != nil {
		t.Fatal("positive Kontrolle", e)
	}
	for _, name := range []string{"demo-1.0-cp313-cp313-win_amd64.whl", "demo-1.0-cp312-cp313-win_amd64.whl", "demo-1.0-cp312-cp312-any.whl", "demo-1.0-cp312-none-manylinux_x86_64.whl", "demo-1.0-cp312-none-win_arm64.whl", "other-1.0-cp312-none-any.whl", "demo-2.0-cp312-none-any.whl", "demo.tar.gz"} {
		if e := CheckWheels(lock, map[string][]byte{name: data}, "3.12.10"); e != ErrHash {
			t.Fatal(name, e)
		}
	}
	if CheckWheels(lock, map[string][]byte{}, "3.12.10") != ErrHash {
		t.Fatal("fehlendes Wheel")
	}
	for _, text := range []string{"--index-url https://example.invalid", "demo>=1.0", "demo==1.0", "demo @ https://example.invalid/x", "-r other.lock", "demo==1.0 --hash=md5:" + sum(data)} {
		if _, e := ParseLock([]byte(text)); e == nil {
			t.Fatal("Lock akzeptiert", text)
		}
	}
	good := "name: demo\ndescription: test\nentry: main.py\ninput_schema: {type: object}\ntools: []\ntimeout_s: 10\npython: python.zip\n"
	pin := Pin{Pin: "demo", DeclaredTools: []string{}}
	for _, text := range []string{
		good + "unknown: true\n", good + "requirements: ''\n", good + "requirements: other.lock\n",
		strings.Replace(good, "tools: []", "tools: [secret_tool]", 1),
		strings.Replace(good, "entry: main.py", "entry: ../main.py", 1),
		strings.Replace(good, "timeout_s: 10", "timeout_s: 0", 1),
		strings.Replace(good, "timeout_s: 10", "timeout_s: 86401", 1),
		strings.Replace(good, "python: python.zip", "python: null", 1),
		strings.Replace(good, "python: python.zip\n", "", 1),
	} {
		if _, e := readPackageManifest(map[string][]byte{"jnpt-desktop.yaml": []byte(text), "main.py": []byte("pass")}, pin); e != ErrManifest {
			t.Fatal("Paketmanifest akzeptiert", e)
		}
	}
}

func TestUmgebungFehlendeZusaetzlicheDateien(t *testing.T) {
	for _, kind := range []string{"missing", "extra"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root+"/main.py", []byte("pass"))
			seal, e := Snapshot(root)
			if e != nil {
				t.Fatal(e)
			}
			if kind == "missing" {
				removeTestFile(t, root+"/main.py")
			} else {
				writeTestFile(t, root+"/extra.py", []byte("pass"))
			}
			if VerifyEnvironment(root, seal) != ErrHash {
				t.Fatal("Dateimenge akzeptiert")
			}
		})
	}
}

func TestRahmenDeckelMitVollstaendigemKoerper(t *testing.T) {
	b := make([]byte, (8<<20)+5)
	binary.BigEndian.PutUint32(b[:4], 8<<20+1)
	if _, e := ReadFrame(bytes.NewReader(b)); e != ErrProtocol {
		t.Fatal("zu grosser vollständiger Rahmen", e)
	}
}

func TestManifestGoldenAmProduktionsleser(t *testing.T) {
	raw := readTestFile(t, "../../contract/golden/manifest_response.json")
	var envelope struct{ Body SignedManifest }
	if e := json.Unmarshal(raw, &envelope); e != nil {
		t.Fatal(e)
	}
	m, e := ParseManifest(envelope.Body.Manifest)
	if e != nil {
		t.Fatal(e)
	}
	raw = readTestFile(t, "../../contract/golden/manifest_canonical.bin")
	got, e := canonical(envelope.Body.Manifest)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal("kanonische Bytes", e)
	}
	if m.Seq == 0 {
		t.Fatal("Sequenz")
	}
}
func writeTestFile(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func removeTestFile(t *testing.T, p string) {
	t.Helper()
	if e := os.Remove(p); e != nil {
		t.Fatal(e)
	}
}
func readTestFile(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
