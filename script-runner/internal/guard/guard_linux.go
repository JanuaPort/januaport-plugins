//go:build linux

package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// Run ist der Wächter als PID 1. Rückgabe ist der Exit-Code des Containers.
func Run(cfg Config) int {
	start := time.Now()
	// M2: vor allem anderen — das Skript läuft mit derselben UID und darf
	// weder /proc/1/environ noch /proc/1/fd noch /proc/1/mem öffnen.
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0); errno != 0 {
		fmt.Fprintln(os.Stderr, "guard: PR_SET_DUMPABLE fehlgeschlagen")
		return 1
	}
	// kill(-1) trifft nur als Init des eigenen PID-Namespace genau das
	// Richtige. Läuft der Wächter nicht als PID 1, bedient er nicht.
	if os.Getpid() != 1 {
		fmt.Fprintln(os.Stderr, "guard: nicht PID 1")
		return 1
	}
	hello := frame.Hello{Type: frame.TypeHello, Instance: newInstanceID(), Isolation: unameIsolation(), Seccomp: selfSeccomp()}
	conn, job := waitForJob(cfg.SocketPath, hello)
	r := &runState{conn: conn}
	r.run(cfg, job)
	if finish(r, start, MinLifetime) {
		return 2
	}
	return 0
}

func unameIsolation() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return "standard"
	}
	return isolation(utsString(u.Release[:]))
}

func utsString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func selfSeccomp() int {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	return parseSeccomp(string(b))
}

// waitForJob verbindet sich, meldet hello und wartet auf den job. Schließt
// der vermittler vor dem job (Neustart, V1-Regel „erst nach EOF“), versucht
// der Wächter es erneut — in dieser Instanz lief noch kein Skript.
func waitForJob(sock string, hello frame.Hello) (net.Conn, frame.Job) {
	for {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err := frame.Write(conn, hello); err == nil {
			if payload, err := frame.Read(conn, MaxJobFrame); err == nil {
				var job frame.Job
				if json.Unmarshal(payload, &job) == nil && job.Type == frame.TypeJob {
					return conn, job
				}
			}
		}
		_ = conn.Close()
		time.Sleep(200 * time.Millisecond)
	}
}

// runState hält einen Lauf und setzt die finishOps um.
type runState struct {
	conn     net.Conn
	writeMu  sync.Mutex
	aborted  bool
	exited   *frame.Exited
	errMu    sync.Mutex
	scriptEr *frame.ScriptError
}

func (r *runState) send(v any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return frame.Write(r.conn, v)
}

func (r *runState) run(cfg Config, job frame.Job) {
	pinDir := filepath.Join(cfg.WorkDir, "pin")
	if err := writeFiles(pinDir, job.Files); err != nil {
		r.exited = &frame.Exited{Type: frame.TypeExited, Code: 1}
		return
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		r.exited = &frame.Exited{Type: frame.TypeExited, Code: 1}
		return
	}
	ours := os.NewFile(uintptr(fds[0]), "bootstrap-guard")
	theirs := os.NewFile(uintptr(fds[1]), "bootstrap-child")
	defer ours.Close()

	cmd := exec.Command(cfg.Python, "-I", cfg.Bootstrap, pinDir, job.Entry)
	cmd.Dir = pinDir
	cmd.Env = childEnv()
	cmd.ExtraFiles = []*os.File{theirs}
	input := job.Input
	if len(input) == 0 {
		input = []byte("{}")
	}
	cmd.Stdin = bytes.NewReader(input)
	// Eigene Pipe statt StdoutPipe: cmd.Wait schließt so nicht das Leseende,
	// bevor die Ausgabe ganz gelesen ist.
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		r.exited = &frame.Exited{Type: frame.TypeExited, Code: 1}
		return
	}
	defer stdout.Close()
	cmd.Stdout = stdoutW
	// stderr bleibt leer (/dev/null): Tracebacks verlassen die Sandbox nie (M6).
	err = cmd.Start()
	_ = stdoutW.Close()
	_ = theirs.Close()
	if err != nil {
		r.exited = &frame.Exited{Type: frame.TypeExited, Code: 1}
		return
	}

	outDone := make(chan struct{})
	go func() { r.pumpOutput(stdout); close(outDone) }()
	bootDone := make(chan struct{})
	go func() { r.pumpBootstrap(ours); close(bootDone) }()
	connDone := make(chan struct{})
	go func() { r.pumpMediator(ours); close(connDone) }()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	select {
	case err := <-waitDone:
		// Kurze Gnadenfrist für den Rest der Ausgabe; Enkelprozesse, die
		// stdout offen halten, sterben gleich mit kill(-1).
		grace, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		for _, done := range []chan struct{}{outDone, bootDone} {
			select {
			case <-done:
			case <-grace.Done():
			}
		}
		cancel()
		r.exited = r.exitFrame(cmd, err)
	case <-connDone:
		r.aborted = true
	}
}

// exitFrame baut das exited aus dem Prozessstatus und der Fehlermeldung des
// Bootstraps.
func (r *runState) exitFrame(cmd *exec.Cmd, _ error) *frame.Exited {
	ex := &frame.Exited{Type: frame.TypeExited, Code: -1}
	if st, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		switch {
		case st.Signaled():
			ex.Signal = int(st.Signal())
		case st.Exited():
			ex.Code = st.ExitStatus()
		}
	}
	r.errMu.Lock()
	if r.scriptEr != nil {
		ex.ErrorType, ex.ErrorAt = r.scriptEr.ErrorType, r.scriptEr.ErrorAt
	}
	r.errMu.Unlock()
	return ex
}

// pumpOutput schickt stdout in Abschnitten; ein angebrochenes UTF-8-Zeichen
// wandert in den nächsten Abschnitt.
func (r *runState) pumpOutput(stdout io.Reader) {
	buf := make([]byte, 64<<10)
	var carry []byte
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			data := append(carry, buf[:n]...)
			cut := incompleteTail(data)
			carry = append([]byte(nil), data[cut:]...)
			if cut > 0 && r.send(frame.Output{Type: frame.TypeOutput, Data: string(data[:cut])}) != nil {
				return
			}
		}
		if err != nil {
			if len(carry) > 0 {
				_ = r.send(frame.Output{Type: frame.TypeOutput, Data: string(carry)})
			}
			return
		}
	}
}

// pumpBootstrap reicht tool_call an den vermittler durch und merkt sich die
// Fehlermeldung des Bootstraps. Andere Rahmentypen verwirft er.
func (r *runState) pumpBootstrap(ch io.Reader) {
	for {
		payload, err := frame.Read(ch, MaxJobFrame)
		if err != nil {
			return
		}
		typ, err := frame.TypeOf(payload)
		if err != nil {
			continue
		}
		switch typ {
		case frame.TypeToolCall:
			r.writeMu.Lock()
			err := writeRaw(r.conn, payload)
			r.writeMu.Unlock()
			if err != nil {
				return
			}
		case frame.TypeError:
			var se frame.ScriptError
			if json.Unmarshal(payload, &se) == nil {
				r.errMu.Lock()
				r.scriptEr = &se
				r.errMu.Unlock()
			}
		}
	}
}

// pumpMediator reicht tool_result an den Bootstrap durch. Endet die
// Verbindung, ist der Lauf vorbei (timeout, Grenze, V1).
func (r *runState) pumpMediator(ch io.Writer) {
	for {
		payload, err := frame.Read(r.conn, MaxJobFrame)
		if err != nil {
			return
		}
		if typ, err := frame.TypeOf(payload); err == nil && typ == frame.TypeToolResult {
			if writeRaw(ch, payload) != nil {
				return
			}
		}
	}
}

func writeRaw(w io.Writer, payload []byte) error {
	var hdr [4]byte
	n := len(payload)
	hdr[0], hdr[1], hdr[2], hdr[3] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// incompleteTail liefert die Länge von data ohne ein angebrochenes
// UTF-8-Zeichen am Ende.
func incompleteTail(data []byte) int {
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if utf8.FullRune(data[i:]) {
				return len(data)
			}
			return i
		}
	}
	return len(data)
}

// finishOps

func (r *runState) SendExited() {
	if r.aborted || r.exited == nil {
		return
	}
	_ = r.send(r.exited)
}

func (r *runState) KillAll() { _ = syscall.Kill(-1, syscall.SIGKILL) }

func (r *runState) ReapAll() {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var st syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &st, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.ECHILD) {
			return
		}
		if pid <= 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func (r *runState) OthersAlive() bool { return others("/proc") }

func (r *runState) Close() { _ = r.conn.Close() }

func (r *runState) SleepUntil(t time.Time) { time.Sleep(time.Until(t)) }
