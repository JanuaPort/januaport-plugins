//go:build windows

package pipe

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Listener struct {
	name   string
	handle windows.Handle
	ctx    context.Context
	cancel context.CancelFunc
	gate   sync.Mutex
	once   sync.Once
}

func currentSIDs() (string, string, error) {
	token := windows.GetCurrentProcessToken()
	u, err := token.GetTokenUser()
	if err != nil {
		return "", "", ErrDisturbance
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return "", "", ErrDisturbance
	}
	for _, g := range groups.AllGroups() {
		if g.Attributes&windows.SE_GROUP_LOGON_ID == windows.SE_GROUP_LOGON_ID {
			return u.User.Sid.String(), g.Sid.String(), nil
		}
	}
	return "", "", ErrDisturbance
}

func Listen() (*Listener, error) {
	user, logon, err := currentSIDs()
	if err != nil {
		return nil, err
	}
	return listenName(PipeName(user), user, logon)
}

func listenName(name, user, logon string) (*Listener, error) {
	sd, err := windows.SecurityDescriptorFromString(pipeSDDL(user, logon))
	if err != nil {
		return nil, ErrDisturbance
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd, InheritHandle: 0}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, ErrDisturbance
	}
	h, err := windows.CreateNamedPipe(p, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, MaxFrame+4, MaxFrame+4, 0, &sa)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, ErrOccupied
		}
		return nil, ErrDisturbance
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Listener{name: name, handle: h, ctx: ctx, cancel: cancel}, nil
}

// Keep the first instance handle for the entire listener lifetime, including
// reconnects. Connections use event-based overlapped I/O; no squatting gap.
// gate spans Accept through connection.Close, so the original is never closed
// while an operation or DisconnectNamedPipe is still using it.
func (l *Listener) Accept(ctx context.Context) (io.ReadWriteCloser, error) {
	l.gate.Lock()
	if l.ctx.Err() != nil || ctx.Err() != nil {
		l.gate.Unlock()
		return nil, ErrDisturbance
	}
	if err := l.connect(ctx); err != nil {
		l.gate.Unlock()
		return nil, err
	}
	var pid uint32
	if windows.GetNamedPipeClientProcessId(l.handle, &pid) != nil || checkClient(pid) != nil {
		windows.DisconnectNamedPipe(l.handle)
		l.gate.Unlock()
		return nil, ErrDisturbance
	}
	c := &connection{listener: l, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-l.ctx.Done():
			c.Close()
		case <-c.done:
		}
	}()
	return c, nil
}

func (l *Listener) connect(ctx context.Context) error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return ErrDisturbance
	}
	defer windows.CloseHandle(event)
	ov := windows.Overlapped{HEvent: event}
	err = windows.ConnectNamedPipe(l.handle, &ov)
	if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		return nil
	}
	if err == nil {
		return nil
	}
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return ErrDisturbance
	}
	finished, watched := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watched)
		select {
		case <-ctx.Done():
			windows.CancelIoEx(l.handle, &ov)
		case <-l.ctx.Done():
			windows.CancelIoEx(l.handle, &ov)
		case <-finished:
		}
	}()
	var n uint32
	err = windows.GetOverlappedResult(l.handle, &ov, &n, true)
	close(finished)
	<-watched
	if err != nil {
		return ErrDisturbance
	}
	return nil
}

type connection struct {
	listener        *Listener
	once            sync.Once
	done            chan struct{}
	mu              sync.Mutex
	closed          bool
	ops             sync.WaitGroup
	readMu, writeMu sync.Mutex
}

func (c *connection) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	return c.transfer(b, false)
}
func (c *connection) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.transfer(b, true)
}
func (c *connection) transfer(b []byte, write bool) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	c.ops.Add(1)
	c.mu.Unlock()
	defer c.ops.Done()
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	ov := windows.Overlapped{HEvent: event}
	var n uint32
	// Submit and test closed under one mutex, paired with Close's CancelIoEx.
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if write {
		err = windows.WriteFile(c.listener.handle, b, &n, &ov)
	} else {
		err = windows.ReadFile(c.listener.handle, b, &n, &ov)
	}
	c.mu.Unlock()
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(c.listener.handle, &ov, &n, true)
	}
	if errors.Is(err, windows.ERROR_BROKEN_PIPE) {
		err = io.EOF
	}
	return int(n), err
}
func (c *connection) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		windows.CancelIoEx(c.listener.handle, nil)
		c.mu.Unlock()
		c.ops.Wait()
		windows.DisconnectNamedPipe(c.listener.handle)
		close(c.done)
		c.listener.gate.Unlock()
	})
	return nil
}
func (l *Listener) Close() error {
	var err error
	l.once.Do(func() { l.cancel(); l.gate.Lock(); defer l.gate.Unlock(); err = windows.CloseHandle(l.handle) })
	return err
}

// Dial is the L4 client library. Never send a byte before PID, image, user SID
// and the trusted Authenticode publisher match. A squatter gets no credentials.
func Dial() (io.ReadWriteCloser, error) {
	user, _, err := currentSIDs()
	if err != nil {
		return nil, ErrDisturbance
	}
	name, _ := windows.UTF16PtrFromString(PipeName(user))
	h, err := windows.CreateFile(name, windows.FILE_GENERIC_READ|windows.FILE_WRITE_DATA, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
	if err != nil {
		return nil, ErrDisturbance
	}
	if err = checkServer(h); err != nil {
		windows.CloseHandle(h)
		return nil, ErrDisturbance
	}
	return os.NewFile(uintptr(h), "panel-pipe"), nil
}

func checkClient(pid uint32) error {
	own, err := os.Executable()
	if err != nil {
		return ErrDisturbance
	}
	p, err := processPeer(pid, false)
	if err != nil || !sameFolder(own, p.Image, "jnpt-bedienfeld.exe") {
		return ErrDisturbance
	}
	return nil
}

func checkServer(h windows.Handle) error {
	var pid uint32
	if windows.GetNamedPipeServerProcessId(h, &pid) != nil {
		return ErrDisturbance
	}
	server, err := processPeer(pid, true)
	if err != nil {
		return ErrDisturbance
	}
	own, err := processPeer(windows.GetCurrentProcessId(), true)
	if err != nil {
		return ErrDisturbance
	}
	return verifyServer(own, server)
}

func processPeer(pid uint32, signature bool) (Peer, error) {
	var p Peer
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return p, ErrDisturbance
	}
	defer windows.CloseHandle(proc)
	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(proc, 0, &buf[0], &n) != nil {
		return p, ErrDisturbance
	}
	p.Image = windows.UTF16ToString(buf[:n])
	var token windows.Token
	if windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token) != nil {
		return p, ErrDisturbance
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return p, ErrDisturbance
	}
	p.UserSID = user.User.Sid.String()
	if signature {
		p.Publisher, err = publisher(p.Image)
		if err != nil {
			return Peer{}, ErrDisturbance
		}
	}
	return p, nil
}
