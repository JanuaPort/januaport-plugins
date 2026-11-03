//go:build windows

package platform

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	panel "github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
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
			t.Fatalf("pipe object name: %x", r)
		}
		u := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
		names = append(names, u.String())
	}
	return names
}

func TestWindowsPythonKindOhneBedienfeldPipe(t *testing.T) {
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
