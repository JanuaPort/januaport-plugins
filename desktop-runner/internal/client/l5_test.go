package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
)

var l5PNG = []byte{137, 80, 78, 71, 13, 10, 26, 10, 0}

func l5Bed(t *testing.T, test bool, pin string) *bed {
	t.Helper()
	b := haltBed(t)
	b.env.Origin.Pin = pin
	// Package metadata must bind the test pin too, just like a real job.
	manifest := "name: " + pin + "\ndescription: test\nentry: main.py\ninput_schema: {type: object}\ntools: []\ntimeout_s: 10\npython: python.zip\n"
	commit, raw := testfixture.Package(map[string][]byte{"main.py": []byte("pass"), "jnpt-desktop.yaml": []byte(manifest)}, "")
	b.env.Origin.Commit = commit
	b.env.Dir = envKey(b.env.Origin)
	newDir := filepath.Join(b.st.Root, "envs", b.env.Dir)
	if newDir != b.envDir {
		if err := os.Rename(b.envDir, newDir); err != nil {
			t.Fatal(err)
		}
	}
	b.envDir = newDir
	b.result.Commit = commit
	if err := os.WriteFile(filepath.Join(b.st.Root, "packages", b.env.Dir), raw, 0600); err != nil {
		t.Fatal(err)
	}
	m := l1Manifest()
	m.TestDevice = test
	m.Pins = []Pin{b.env.Origin}
	signed, cred := l1Sign(t, encode(m), m.DeviceID)
	b.engine.Credential = cred
	b.engine.bundle.Manifest.Envelope = signed
	b.result.RunKey = ""
	b.result.JobID = "12345678-1234-4234-8234-123456789abc"
	o := NewTestOrder(signed, Job{JobID: b.result.JobID, Pin: pin, Commit: b.result.Commit, Path: b.env.Origin.Path, Algo: b.env.Origin.Algo, ManifestSeq: b.result.ManifestSeq})
	if err := b.st.Save("test-"+o.JobID, o); err != nil {
		t.Fatal(err)
	}
	b.engine.Capture = func(context.Context) ([]byte, error) { return nil, errors.New("no desktop in tests") }
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP", r.URL.Path)
		w.WriteHeader(500)
	})
	return b
}

type l5Input struct {
	sync.Mutex
	bytes.Buffer
}

func (w *l5Input) Write(b []byte) (int, error) { w.Lock(); defer w.Unlock(); return w.Buffer.Write(b) }
func (w *l5Input) Close() error                { return nil }

type l5Process struct {
	Process
	input io.WriteCloser
}

func (p *l5Process) Input() io.WriteCloser { return p.input }
func l5Raw(b *bed, frames ...string) *l5Input {
	out := &l5Input{}
	b.engine.Spawn = func(ctx context.Context, l Launch) (Process, error) {
		p, e := b.spawn(ctx, l)
		return &l5Process{p, out}, e
	}
	b.exit = func(w *io.PipeWriter) {
		for _, raw := range frames {
			var h [4]byte
			binary.BigEndian.PutUint32(h[:], uint32(len(raw)))
			if _, e := w.Write(h[:]); e != nil {
				return
			}
			if _, e := w.Write([]byte(raw)); e != nil {
				return
			}
		}
		WriteFrame(w, map[string]any{"type": "exited", "code": 0})
	}
	return out
}
func l5Replies(t *testing.T, in *l5Input) []map[string]any {
	t.Helper()
	in.Lock()
	defer in.Unlock()
	var replies []map[string]any
	for in.Len() > 0 {
		raw, e := ReadFrame(&in.Buffer)
		if e != nil {
			t.Fatal(e)
		}
		var m map[string]any
		if e = json.Unmarshal(raw, &m); e != nil {
			t.Fatal(e)
		}
		replies = append(replies, m)
	}
	return replies[1:] // launch input
}

func testScreenshotRaw(t *testing.T) {
	for _, tc := range []struct {
		name, pin string
		test      bool
	}{
		{"allowed", "test_aaaaaaaa", true}, {"no-test-device", "test_aaaaaaaa", false}, {"normal-pin", "demo", true}, {"foreign-pin", "test_bbbbbbbb", true},
		{"signature", "test_aaaaaaaa", true}, {"job-binding", "test_aaaaaaaa", true}, {"offline", "test_aaaaaaaa", true}, {"setup", "test_aaaaaaaa", true}, {"missing-order", "test_aaaaaaaa", true}, {"environment-pin", "test_aaaaaaaa", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := l5Bed(t, tc.test, tc.pin)
			switch tc.name {
			case "signature":
				var o TestOrder
				b.st.Load("test-"+b.result.JobID, &o)
				o.Envelope.Sig = "broken"
				b.st.Save("test-"+b.result.JobID, o)
			case "job-binding":
				b.result.ManifestSeq++
			case "offline":
				b.result.RanOffline = true
			case "setup":
				b.result.RunKey = "setup/test_aaaaaaaa/room/nonce"
			case "missing-order":
				b.result.JobID = "12345678-1234-4234-8234-123456789abd"
			case "environment-pin":
				var o TestOrder
				b.st.Load("test-"+b.result.JobID, &o)
				o.Pin = "demo"
				b.st.Save("test-"+b.result.JobID, o)
			}
			var captures, uploads atomic.Int32
			b.engine.Capture = func(context.Context) ([]byte, error) { captures.Add(1); return l5PNG, nil }
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
				uploads.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/api/device/v1/jobs/"+b.result.JobID+"/screenshot" || r.Header.Get("Content-Type") != "image/png" || !bytes.Equal(body, l5PNG) {
					t.Error("upload", r.URL.Path)
				}
				w.WriteHeader(204)
			})
			in := l5Raw(b, `{"type":"screenshot"}`)
			r := b.execute()
			replies := l5Replies(t, in)
			want := int32(0)
			if tc.name == "allowed" {
				want = 2
			}
			if r.End != "ok" || captures.Load() != want || uploads.Load() != want {
				t.Fatal("raw capture gate", r, captures.Load(), uploads.Load())
			}
			if len(replies) != 1 || (replies[0]["type"] == "screenshot") != (want > 0) {
				t.Fatal(replies)
			}
		})
	}
}

func testResultRaw(t *testing.T) {
	for _, tc := range []struct {
		name, pin string
		test      bool
	}{{"allowed", "test_aaaaaaaa", true}, {"no-test-device", "test_aaaaaaaa", false}, {"normal-pin", "demo", true}, {"foreign-pin", "test_bbbbbbbb", true}} {
		t.Run(tc.name, func(t *testing.T) {
			b := l5Bed(t, tc.test, tc.pin)
			l5Raw(b, `{"type":"ergebnis","wert":1}`, `{"type":"ergebnis","wert":{"last":2}}`)
			r := b.execute()
			want := ""
			if tc.name == "allowed" {
				want = `{"last":2}`
			}
			if r.End != "ok" || string(r.Result) != want {
				t.Fatal("raw result gate/last wins", r)
			}
			var seen atomic.Int32
			b.engine.API = l1API(t, func(w http.ResponseWriter, req *http.Request) {
				seen.Add(1)
				body, _ := io.ReadAll(req.Body)
				var v map[string]json.RawMessage
				json.Unmarshal(body, &v)
				if string(v["result"]) != want {
					t.Error("result wire", string(body))
				}
				w.WriteHeader(204)
			})
			if e := b.engine.report(r); e != nil {
				t.Fatal(e)
			}
			b.engine.deliverOne(context.Background())
			if seen.Load() != 1 || len(b.st.Journal().Results) != 0 {
				t.Fatal("journal/HTTP")
			}
		})
	}
}

func TestL5ErgebnisNullUndGrenzen(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		ok          bool
	}{{"null", "null", true}, {"64KiB", `"` + strings.Repeat("x", 65534) + `"`, true}, {"too-large", `"` + strings.Repeat("x", 65535) + `"`, false}, {"escaped-too-large", `"` + strings.Repeat("<", 11000) + `"`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			b := l5Bed(t, true, "test_aaaaaaaa")
			l5Raw(b, `{"type":"ergebnis","wert":42}`, `{"type":"ergebnis","wert":`+tc.value+`}`)
			r := b.execute()
			if (r.End == "ok") != tc.ok {
				t.Fatal(r.End)
			}
			if tc.name == "null" {
				if len(r.Result) != 0 || bytes.Contains(encode(r), []byte(`"result"`)) {
					t.Fatal("null must omit result", r)
				}
				b.engine.API = l1API(t, func(w http.ResponseWriter, req *http.Request) {
					body, _ := io.ReadAll(req.Body)
					if bytes.Contains(body, []byte(`"result"`)) {
						t.Error("null on wire")
					}
					w.WriteHeader(204)
				})
				b.engine.report(r)
				b.engine.deliverOne(context.Background())
				if len(b.st.Journal().Results) != 0 {
					t.Fatal("null replay")
				}
			}
			if tc.name == "64KiB" && string(r.Result) != tc.value {
				t.Fatal("boundary lost")
			}
		})
	}
}

func TestL5ScreenshotGrenzen(t *testing.T) {
	for _, tc := range []string{"sixth", "automatic", "oversize", "bad-png", "capture-error", "upload-error"} {
		t.Run(tc, func(t *testing.T) {
			b := l5Bed(t, true, "test_aaaaaaaa")
			var captures, uploads atomic.Int32
			b.engine.Capture = func(context.Context) ([]byte, error) {
				captures.Add(1)
				switch tc {
				case "oversize":
					return append(append([]byte{}, l5PNG...), make([]byte, 4<<20)...), nil
				case "bad-png":
					return []byte("not PNG"), nil
				case "capture-error":
					return nil, errors.New("capture")
				}
				return l5PNG, nil
			}
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
				uploads.Add(1)
				if tc == "upload-error" {
					w.WriteHeader(503)
					w.Write([]byte(`{"error":{"class":"busy"}}`))
					return
				}
				w.WriteHeader(204)
			})
			frames := []string{}
			if tc != "automatic" {
				for i := 0; i < 6; i++ {
					frames = append(frames, `{"type":"screenshot"}`)
				}
			}
			in := l5Raw(b, frames...)
			r := b.execute()
			want := int32(5)
			if tc == "automatic" {
				want = 1
			}
			if r.End != "ok" || captures.Load() != want {
				t.Fatal(r, captures.Load())
			}
			up := want
			if tc == "oversize" || tc == "bad-png" || tc == "capture-error" {
				up = 0
			}
			if uploads.Load() != up {
				t.Fatal("upload count", uploads.Load(), up)
			}
			replies := l5Replies(t, in)
			if len(frames) > 0 {
				if len(replies) != 6 || replies[4]["type"] != "fehler" || replies[5]["type"] != "fehler" {
					t.Fatal(replies)
				}
				if tc != "sixth" && replies[0]["type"] != "fehler" {
					t.Fatal("error not returned", replies)
				}
			}
		})
	}
}

func TestL5RahmenStrikt(t *testing.T) {
	for _, raw := range []string{`{"type":"unknown"}`, `{"type":"screenshot","test_device":true}`, `{"type":"screenshot","type":"screenshot"}`, `{"type":"ergebnis"}`, `{"type":"ergebnis","wert":2,"extra":true}`, `{"type":"ergebnis","wert":1,"wert":2}`, `{"type":"ergebnis","wert":NaN}`, `{"type":"ergebnis","wert":1} {}`} {
		b := l5Bed(t, false, "demo")
		var captures atomic.Int32
		b.engine.Capture = func(context.Context) ([]byte, error) { captures.Add(1); return l5PNG, nil }
		l5Raw(b, raw)
		if r := b.execute(); r.End != "internal" || captures.Load() != 0 {
			t.Fatal(raw, r, captures.Load())
		}
	}
}

// Run the actual embedded bootstrap and user script through Execute's pipes.
// Only the process transport is substituted; no desktop or network in Python.
type l5Python struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  io.ReadCloser
	once sync.Once
	err  error
}

func (p *l5Python) Input() io.WriteCloser { return p.in }
func (p *l5Python) Output() io.ReadCloser { return p.out }
func (p *l5Python) Kill() error           { return p.cmd.Process.Kill() }
func (p *l5Python) Wait() error           { p.once.Do(func() { p.err = p.cmd.Wait() }); return p.err }
func (p *l5Python) Close() error          { p.Kill(); p.Wait(); p.in.Close(); return p.out.Close() }
func l5PythonScript(t *testing.T, b *bed, script string) {
	t.Helper()
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("python3 required for bootstrap adapter")
	}
	os.MkdirAll(filepath.Join(b.envDir, "pin"), 0700)
	for name, body := range map[string][]byte{"runtime/bootstrap.py": bootstrap, "pin/main.py": []byte(script)} {
		if e = os.WriteFile(filepath.Join(b.envDir, filepath.FromSlash(name)), body, 0600); e != nil {
			t.Fatal(e)
		}
	}
	b.env.Files, e = Snapshot(b.envDir)
	if e != nil {
		t.Fatal(e)
	}
	b.engine.Spawn = func(ctx context.Context, l Launch) (Process, error) {
		cmd := exec.CommandContext(ctx, python, l.Args...)
		cmd.Dir = l.Dir
		cmd.Env = l.Env
		in, e := cmd.StdinPipe()
		if e != nil {
			return nil, e
		}
		out, e := cmd.StdoutPipe()
		if e != nil {
			return nil, e
		}
		if e = cmd.Start(); e != nil {
			return nil, e
		}
		return &l5Python{cmd: cmd, in: in, out: out}, nil
	}
}
func TestFrageNurLokal(t *testing.T) {
	for _, mode := range []string{"no-panel", "deadline", "answer"} {
		t.Run(mode, func(t *testing.T) {
			b := l5Bed(t, false, "demo")
			var hits atomic.Int32
			b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) })
			want := "None"
			if mode != "no-panel" {
				events, detach, e := b.engine.Questions.Attach()
				if e != nil {
					t.Fatal(e)
				}
				defer detach()
				if mode == "answer" {
					want = "1"
					go func() { q := <-events; b.engine.Questions.Answer(q.ID, 1) }()
				}
			}
			l5PythonScript(t, b, "import jnpt\nassert jnpt.frage('Weiter?', ['Ja','Nein'], 5) == "+want+"\n")
			start := time.Now()
			r := b.execute()
			if r.End != "ok" || hits.Load() != 0 {
				t.Fatal(r, hits.Load())
			}
			if mode == "deadline" && time.Since(start) < 5*time.Second {
				t.Fatal("deadline bypassed")
			}
		})
	}
}
func TestL5BootstrapModule(t *testing.T) {
	b := l5Bed(t, true, "test_aaaaaaaa")
	l5PythonScript(t, b, "import jnpt, januaport\nassert januaport.input == {}\nassert jnpt.screenshot() is None\njnpt.ergebnis({'first': 1})\njnpt.ergebnis({'last': 2})\n")
	r := b.execute()
	if r.End != "ok" || !l1JSONEqual(r.Result, []byte(`{"last":2}`)) {
		t.Fatal(r)
	}
}

func TestL5TestauftragAmHTTPAdapter(t *testing.T) {
	b := l5Bed(t, true, "test_aaaaaaaa")
	// Exercise the actual admission path that stores the signed historical
	// authorization, rather than relying on the raw-frame fixture's grant.
	if err := os.Remove(filepath.Join(b.st.Root, "test-"+b.result.JobID)); err != nil {
		t.Fatal(err)
	}
	b.engine.bundle.Environments = map[string]Environment{b.env.Dir: b.env}
	var starts, captures, uploads, reports atomic.Int32
	b.engine.Capture = func(context.Context) ([]byte, error) { captures.Add(1); return l5PNG, nil }
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/device/v1/jobs/" + b.result.JobID + "/start":
			starts.Add(1)
			json.NewEncoder(w).Encode(StartReply{"synthetic", time.Now().Add(time.Minute).UTC().Format(time.RFC3339), "/api/device/v1/mcp"})
		case "/api/device/v1/jobs/" + b.result.JobID + "/screenshot":
			uploads.Add(1)
			w.WriteHeader(204)
		case "/api/device/v1/runs/result":
			reports.Add(1)
			body, _ := io.ReadAll(r.Body)
			var result Result
			if json.Unmarshal(body, &result) != nil || result.JobID != b.result.JobID || string(result.Result) != `{"ok":true}` {
				t.Error("result binding", string(body))
			}
			w.WriteHeader(204)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	l5Raw(b, `{"type":"ergebnis","wert":{"ok":true}}`, `{"type":"screenshot"}`)
	j := Job{JobID: b.result.JobID, Pin: b.env.Origin.Pin, Commit: b.result.Commit, Path: b.env.Origin.Path, Algo: b.env.Origin.Algo, ManifestSeq: b.result.ManifestSeq, Input: json.RawMessage(`{}`), StartBy: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}
	j.Limits.WallS = 10
	if err := b.engine.adhoc(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	b.engine.deliverOne(context.Background())
	if starts.Load() != 1 || captures.Load() != 2 || uploads.Load() != 2 || reports.Load() != 1 || len(b.st.Journal().Results) != 0 {
		t.Fatal(starts.Load(), captures.Load(), uploads.Load(), reports.Load(), b.st.Journal())
	}
}
