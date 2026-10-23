package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func goldenPackage(t *testing.T) (packageHead, []byte) {
	t.Helper()
	b, e := os.ReadFile("../../contract/golden/package_sha256.bin")
	if e != nil {
		t.Fatal(e)
	}
	var h packageHead
	n := binary.BigEndian.Uint32(b[:4])
	if e = json.Unmarshal(b[4:4+n], &h); e != nil {
		t.Fatal(e)
	}
	return h, b
}

func TestFalscherHashBrichtVorDemStartAb(t *testing.T) {
	h, b := goldenPackage(t)
	for _, kind := range []string{"blob", "commit", "extra", "symlink", "missing"} {
		t.Run(kind, func(t *testing.T) {
			raw := append([]byte(nil), b...)
			want := h.Commit
			switch kind {
			case "blob":
				raw[len(raw)-2] ^= 1
			case "commit":
				want = string(bytes.Repeat([]byte("0"), 64))
			case "extra":
				hh := h
				hh.Objects++
				header := encode(hh)
				frames := raw[4+binary.BigEndian.Uint32(raw[:4]):]
				raw = make([]byte, 4)
				binary.BigEndian.PutUint32(raw, uint32(len(header)))
				raw = append(raw, header...)
				raw = append(raw, frames...)
				raw = append(raw, 0, 0, 0, 2, 'b', 'x')
			case "symlink":
				want, raw = testfixture.Package(map[string][]byte{"main.py": []byte("pass")}, "120000")
			case "missing":
				hh := h
				hh.Objects--
				header := encode(hh)
				frames := raw[4+binary.BigEndian.Uint32(raw[:4]):]
				frames = frames[4+binary.BigEndian.Uint32(frames[:4]):]
				raw = make([]byte, 4)
				binary.BigEndian.PutUint32(raw, uint32(len(header)))
				raw = append(raw, header...)
				raw = append(raw, frames...)
			}
			starts := 0
			pin := Pin{Pin: "demo", Algo: h.Algo, Commit: want, Path: h.Path}
			st, _ := OpenState(t.TempDir(), testCipher{})
			dir := envKey(pin)
			os.MkdirAll(filepath.Join(st.Root, "packages"), 0700)
			os.WriteFile(filepath.Join(st.Root, "packages", dir), raw, 0600)
			c := Config{ExpectedAccount: "own"}
			c.StartPolicy.Mode = "always"
			engine := Engine{State: st, Config: c, Session: func() (Session, error) { return Session{Active: true, Account: "own"}, nil }, Spawn: func(context.Context, Launch) (Process, error) { starts++; return nil, ErrProtocol }}
			result := engine.Execute(context.Background(), Result{Commit: want}, Environment{Origin: pin, Dir: dir}, json.RawMessage(`{}`), "", 1, 0)
			if result.End != "refused" || result.Detail != "hash" || starts != 0 {
				t.Fatalf("result=%+v starts=%d", result, starts)
			}
		})
	}
}

func TestUmgebungVorJedemStart(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "main.py")
	if e := os.WriteFile(p, []byte("pass"), 0600); e != nil {
		t.Fatal(e)
	}
	seal, e := Snapshot(root)
	if e != nil {
		t.Fatal(e)
	}
	if e = VerifyEnvironment(root, seal); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, []byte("evil"), 0600); e != nil {
		t.Fatal(e)
	}
	if VerifyEnvironment(root, seal) != ErrHash {
		t.Fatal("manipulierte Umgebung akzeptiert")
	}
}

func TestPythonAllowlistUndWheel(t *testing.T) {
	if _, e := CheckPython("python-3.12.10-embed-amd64.zip", []byte("evil")); e != ErrRuntime {
		t.Fatal(e)
	}
	if e := CheckWheels([]byte("demo==1.0 --hash=sha256:"+string(bytes.Repeat([]byte("0"), 64))), map[string][]byte{"demo-1.0-cp312-none-any.whl": []byte("evil")}, "3.12.10"); e != ErrHash {
		t.Fatal(e)
	}
}

func TestOfflineRegel(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		tools []string
		last  time.Time
		want  bool
	}{
		{nil, now.Add(-time.Hour), true}, {[]string{"tool"}, now.Add(-time.Hour), false},
		{nil, now.Add(-2 * time.Hour), false}, {nil, time.Time{}, false}, {nil, now.Add(time.Second), false},
	} {
		if got := OfflineAllowed(Pin{DeclaredTools: tt.tools}, 2, tt.last, now); got != tt.want {
			t.Fatalf("got %v want %v", got, tt.want)
		}
	}
}
