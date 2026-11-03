//go:build windows

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	panel "github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
	"golang.org/x/sys/windows"
)

// Probe the actual handle table, not a guessed small numeric handle range.
func namedPipeHandles(t *testing.T) []string {
	t.Helper()
	query := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQuerySystemInformation")
	size := uint32(1 << 16)
	var data []byte
	for {
		data = make([]byte, size)
		var needed uint32
		r, _, _ := query.Call(64, uintptr(unsafe.Pointer(&data[0])), uintptr(size), uintptr(unsafe.Pointer(&needed)))
		if r == 0 {
			break
		}
		if uint32(r) != 0xc0000004 {
			t.Fatalf("handle query: %x", r)
		}
		size = max(size*2, needed)
		if size > 64<<20 {
			t.Fatal("handle table too large")
		}
	}
	type entry struct {
		Object, PID, Handle  uintptr
		Access               uint32
		Backtrace, Type      uint16
		Attributes, Reserved uint32
	}
	count := *(*uintptr)(unsafe.Pointer(&data[0]))
	offset := 2 * unsafe.Sizeof(uintptr(0))
	var names []string
	objectName := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryObject")
	for i := uintptr(0); i < count; i++ {
		pos := offset + i*unsafe.Sizeof(entry{})
		if pos+unsafe.Sizeof(entry{}) > uintptr(len(data)) {
			t.Fatal("handle table truncated")
		}
		e := (*entry)(unsafe.Pointer(&data[pos]))
		if e.PID != uintptr(os.Getpid()) {
			continue
		}
		typ, err := windows.GetFileType(windows.Handle(e.Handle))
		if err != nil || typ != windows.FILE_TYPE_PIPE {
			continue
		}
		buf := make([]byte, 65536)
		var needed uint32
		r, _, _ := objectName.Call(e.Handle, 1, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&needed)))
		if r != 0 {
			// FILE_TYPE_PIPE also includes unnamed/unqueryable endpoints.
			// NtQueryObject can return STATUS_OBJECT_PATH_INVALID for those.
			// The parent and inherited-handle controls below must still find
			// the named panel endpoint, so an empty probe cannot pass silently.
			continue
		}
		u := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
		if u.Length != 0 {
			names = append(names, u.String())
		}
	}
	return names
}

func TestWindowsPipeKindsondeErkenntVererbtesHandle(t *testing.T) {
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\JanuaPort.Bedienfeld.probe.%d`, os.Getpid()))
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	h, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_BYTE|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 4096, 4096, 0, &sa)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsPipeChildHelper$", "--", "pipe-probe")
	cmd.Env = WindowsEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(h)}}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "Pipe-Handle im Kind") {
		t.Fatalf("inherited pipe was not rejected: %v %s", err, out)
	}
}

func TestWindowsKindOhneBedienfeldPipe(t *testing.T) {
	l, err := panel.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	found := false
	for _, n := range namedPipeHandles(t) {
		found = found || strings.Contains(n, "JanuaPort.Bedienfeld.")
	}
	if !found {
		t.Fatal("Probe sieht Eltern-Pipe nicht")
	}
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Same production Spawn and explicit handle list used for Python, with a native
	// test executable to inspect handles without requiring the Python ZIP.
	p, err := Spawn(ctx, client.Launch{Exe: exe, Args: []string{"-test.run=^TestWindowsPipeChildHelper$", "--", "pipe-probe"}, Dir: t.TempDir(), Env: WindowsEnv()})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	out, err := io.ReadAll(p.Output())
	if err != nil || p.Wait() != nil || !strings.Contains(string(out), "PIPE_CHILD_OK") {
		t.Fatal("Kindsonde", string(out), err)
	}
}

func TestWindowsPythonKindOhneBedienfeldPipe(t *testing.T) {
	py, err := os.ReadFile("../../.probe/python-3.12.10-embed-amd64.zip")
	if os.IsNotExist(err) {
		t.Skip("kuratierte ZIP in .probe erforderlich (Windows-CI)")
	}
	if err != nil {
		t.Fatal(err)
	}
	l, err := panel.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	found := false
	for _, n := range namedPipeHandles(t) {
		found = found || strings.Contains(n, "JanuaPort.Bedienfeld.")
	}
	if !found {
		t.Fatal("Eltern-Pipe fehlt")
	}
	// Keep the embedded runtime path short, as in the existing Python adapter
	// probe. A long testing.T.TempDir name can exceed the Python loader's limit.
	tmp, err := os.MkdirTemp("../../.probe", "h3-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	root, err := filepath.Abs(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err = SecureRoot(root); err != nil {
		t.Fatal(err)
	}
	st, err := client.OpenState(root, DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	script := []byte(`import ctypes as c, os, sys
from ctypes import wintypes as w
k = c.WinDLL("kernel32", use_last_error=True)
nt = c.WinDLL("ntdll")
k.GetCommandLineW.restype = w.LPWSTR
assert "jnpt" not in (str(dict(os.environ)) + k.GetCommandLineW()).lower()
assert "JanuaPort.Bedienfeld." not in str(dict(os.environ)) + k.GetCommandLineW()
class Entry(c.Structure):
    _fields_ = [("object", c.c_size_t), ("pid", c.c_size_t), ("handle", c.c_size_t), ("access", w.DWORD), ("backtrace", w.WORD), ("kind", w.WORD), ("attributes", w.DWORD), ("reserved", w.DWORD)]
class Unicode(c.Structure):
    _fields_ = [("length", w.WORD), ("maximum", w.WORD), ("buffer", c.c_void_p)]
nt.NtQuerySystemInformation.argtypes = [w.ULONG, c.c_void_p, w.ULONG, c.POINTER(w.ULONG)]
nt.NtQueryObject.argtypes = [w.HANDLE, w.ULONG, c.c_void_p, w.ULONG, c.POINTER(w.ULONG)]
k.GetFileType.argtypes = [w.HANDLE]
k.CreateNamedPipeW.argtypes = [w.LPCWSTR, w.DWORD, w.DWORD, w.DWORD, w.DWORD, w.DWORD, w.DWORD, c.c_void_p]
k.CreateNamedPipeW.restype = w.HANDLE
k.CloseHandle.argtypes = [w.HANDLE]
# A positive control inside this interpreter prevents an empty/broken name
# probe from silently passing after unqueryable handles have been skipped.
control_name = "JanuaPort.HandleProbe." + str(os.getpid())
control = k.CreateNamedPipeW("\\\\.\\pipe\\" + control_name, 3 | 0x80000, 8, 1, 4096, 4096, 0, None)
assert control != c.c_void_p(-1).value, c.get_last_error()
size = 65536
while True:
    data, needed = c.create_string_buffer(size), w.ULONG()
    status = nt.NtQuerySystemInformation(64, data, size, c.byref(needed)) & 0xffffffff
    if status == 0: break
    assert status == 0xc0000004, hex(status)
    size = max(size * 2, needed.value)
    assert size <= 64 * 1024 * 1024
count = c.c_size_t.from_buffer(data).value
found_control = False
for i in range(count):
    entry = Entry.from_buffer(data, 2*c.sizeof(c.c_size_t) + i*c.sizeof(Entry))
    if entry.pid != os.getpid() or k.GetFileType(entry.handle) != 3: continue
    name = c.create_string_buffer(65536)
    if nt.NtQueryObject(entry.handle, 1, name, len(name), c.byref(needed)) != 0: continue
    u = Unicode.from_buffer(name)
    text = c.wstring_at(u.buffer, u.length//2) if u.length else ""
    found_control = found_control or text.endswith("\\" + control_name)
    assert "JanuaPort.Bedienfeld." not in text
assert found_control, "Python handle probe missed its positive control"
assert k.CloseHandle(control)
`)
	manifest := []byte("name: demo\ndescription: test\nentry: main.py\ninput_schema: {type: object}\ntools: []\ntimeout_s: 20\npython: python-3.12.10-embed-amd64.zip\n")
	commit, raw := testfixture.Package(map[string][]byte{"main.py": script, "jnpt-desktop.yaml": manifest, "python-3.12.10-embed-amd64.zip": py}, "")
	pin := client.Pin{Pin: "demo", Commit: commit, Path: "demo", Algo: "sha256", DeclaredTools: []string{}, Schedules: []client.Schedule{}}
	env, err := client.Prepare(filepath.Join(root, "envs"), raw, pin)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "packages", env.Dir), raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := client.Config{ExpectedAccount: "test"}
	cfg.StartPolicy.Mode = "always"
	e := client.Engine{State: st, Config: cfg, Session: func() (client.Session, error) { return client.Session{Active: true, Account: "test"}, nil }, Spawn: Spawn, ChildEnv: WindowsEnv(), Temps: &client.RunTemps{Base: filepath.Join(root, "tmp"), Mkdir: MkdirPrivate}}
	r := e.Execute(context.Background(), client.Result{RunKey: "demo/daily/2026-11-03T10:00:00Z", ManifestSeq: 1, Commit: commit}, env, json.RawMessage(`{}`), "", 20, 0)
	if r.End != "ok" {
		t.Fatal("echtes Python-Kind verletzt H3", r)
	}
}

func TestWindowsPipeChildHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "pipe-probe" {
		return
	}
	for _, s := range append(os.Environ(), os.Args...) {
		if strings.Contains(strings.ToLower(s), "jnpt") || strings.Contains(s, "JanuaPort.Bedienfeld.") {
			t.Fatal("Träger in Kindumgebung/Kommandozeile")
		}
	}
	for _, n := range namedPipeHandles(t) {
		if strings.Contains(n, "JanuaPort.Bedienfeld.") {
			t.Fatal("Pipe-Handle im Kind")
		}
	}
	fmt.Println("PIPE_CHILD_OK")
}
