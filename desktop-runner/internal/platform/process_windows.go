package platform

import (
	"context"
	"fmt"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"
)

type process struct {
	job           windows.Handle
	proc          windows.Handle
	input, output *os.File
	done          chan struct{}
	once          sync.Once
	waitErr       error
	mu            sync.Mutex
	closed        bool
}

func (p *process) Input() io.WriteCloser { return p.input }
func (p *process) Output() io.ReadCloser { return p.output }
func (p *process) Wait() error {
	p.once.Do(func() {
		_, e := windows.WaitForSingleObject(p.proc, windows.INFINITE)
		if e != nil {
			p.waitErr = winError("WaitForSingleObject", e)
		}
		var code uint32
		if e == nil {
			e = windows.GetExitCodeProcess(p.proc, &code)
			if e != nil {
				p.waitErr = winError("GetExitCodeProcess", e)
			}
		}
		if e == nil && code != 0 {
			p.waitErr = fmt.Errorf("windows_operation: ProcessExit code=%d", code)
		}
		close(p.done)
	})
	return p.waitErr
}
func (p *process) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	return windows.TerminateJobObject(p.job, 1)
}
func (p *process) Close() error {
	p.Kill()
	p.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	p.input.Close()
	p.output.Close()
	windows.CloseHandle(p.proc)
	return windows.CloseHandle(p.job)
}

func Spawn(ctx context.Context, l client.Launch) (client.Process, error) { return spawn(ctx, l, nil) }
func spawn(ctx context.Context, l client.Launch, beforeAssign func(windows.ProcessInformation) error) (client.Process, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	failed := true
	defer func() {
		if failed {
			windows.CloseHandle(job)
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		return nil, e
	}
	inRead, inWrite, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	defer inRead.Close()
	outRead, outWrite, e := os.Pipe()
	if e != nil {
		inWrite.Close()
		return nil, e
	}
	defer outWrite.Close()
	nul, e := os.OpenFile("NUL", os.O_WRONLY, 0)
	if e != nil {
		inWrite.Close()
		outRead.Close()
		return nil, e
	}
	defer nul.Close()
	defer func() {
		if failed {
			inWrite.Close()
			outRead.Close()
		}
	}()
	handles := []windows.Handle{windows.Handle(inRead.Fd()), windows.Handle(outWrite.Fd()), windows.Handle(nul.Fd())}
	for _, h := range handles {
		if e = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); e != nil {
			return nil, e
		}
	}
	attrs, e := windows.NewProcThreadAttributeList(1)
	if e != nil {
		return nil, e
	}
	defer attrs.Delete()
	if e = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); e != nil {
		return nil, e
	}
	si := windows.StartupInfoEx{}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput = handles[0]
	si.StdOutput = handles[1]
	si.StdErr = handles[2]
	si.ProcThreadAttributeList = attrs.List()
	argv := []string{l.Exe}
	argv = append(argv, l.Args...)
	for i, s := range argv {
		if strings.ContainsRune(s, 0) {
			return nil, winError("ProcessArgumentNUL", nil)
		}
		argv[i] = windows.EscapeArg(s)
	}
	cmd, e := windows.UTF16PtrFromString(strings.Join(argv, " "))
	if e != nil {
		return nil, e
	}
	exe, e := windows.UTF16PtrFromString(l.Exe)
	if e != nil {
		return nil, e
	}
	dir, e := windows.UTF16PtrFromString(l.Dir)
	if e != nil {
		return nil, e
	}
	env := utf16.Encode([]rune(strings.Join(l.Env, "\x00") + "\x00\x00"))
	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_NO_WINDOW)
	if e = windows.CreateProcess(exe, cmd, nil, nil, true, flags, &env[0], dir, &si.StartupInfo, &pi); e != nil {
		return nil, e
	}
	defer windows.CloseHandle(pi.Thread)
	abort := func() {
		windows.TerminateProcess(pi.Process, 1)
		windows.WaitForSingleObject(pi.Process, 5000)
		windows.CloseHandle(pi.Process)
	}
	if beforeAssign != nil {
		if e = beforeAssign(pi); e != nil {
			abort()
			return nil, e
		}
	}
	if e = windows.AssignProcessToJobObject(job, pi.Process); e != nil {
		abort()
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		abort()
		return nil, e
	}
	if _, e = windows.ResumeThread(pi.Thread); e != nil {
		abort()
		return nil, e
	}
	p := &process{job: job, proc: pi.Process, input: inWrite, output: outRead, done: make(chan struct{})}
	failed = false
	go func() {
		select {
		case <-ctx.Done():
			p.Kill()
		case <-p.done:
		}
	}()
	return p, nil
}
