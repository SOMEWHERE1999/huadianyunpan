//go:build windows

package npipe

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"ncepupan/hdd/internal/ipc"
)

const (
	pipeAccessDuplex       = 0x00000003
	pipeTypeMessage        = 0x00000004
	pipeReadmodeMessage    = 0x00000002
	pipeWait               = 0x00000000
	pipeUnlimitedInstances = 255
	genericRead            = 0x80000000
	genericWrite           = 0x40000000
	openExisting           = 3
	errorPipeConnected     = 535
	errorSemTimeout        = 121
)

const (
	PipePath = "\\\\.\\pipe\\huadian-drive"

	defaultConnTimeout = 5 * time.Second
	defaultRWTimeout   = 10 * time.Second
)

var (
	ErrTimeout    = errors.New("npipe: operation timed out")
	ErrConnClosed = errors.New("npipe: connection closed")
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procCreateNamedPipeW    = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe    = kernel32.NewProc("ConnectNamedPipe")
	procCreateFileW         = kernel32.NewProc("CreateFileW")
	procDisconnectNamedPipe = kernel32.NewProc("DisconnectNamedPipe")
	procCancelIoEx          = kernel32.NewProc("CancelIoEx")
	procWaitNamedPipeW      = kernel32.NewProc("WaitNamedPipeW")
)

type Handler func(req ipc.Request) ipc.Response

type Server struct {
	path    string
	handler Handler
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	rwTO    time.Duration
	wg      sync.WaitGroup
}

func NewServer(path string, handler Handler) *Server {
	return &Server{
		path:    path,
		handler: handler,
		stopCh:  make(chan struct{}),
		rwTO:    defaultRWTimeout,
	}
}

func (s *Server) Serve() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("npipe: server already running")
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	for {
		h, err := s.createAndConnect()
		if err != nil {
			if errors.Is(err, ErrConnClosed) {
				return nil
			}
			return fmt.Errorf("npipe accept: %w", err)
		}
		s.mu.Lock()
		if !s.running {
			s.mu.Unlock()
			syscall.CloseHandle(h)
			return nil
		}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.handleConn(h)
	}
}

func (s *Server) Shutdown() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	s.mu.Unlock()
	for i := 0; i < 100; i++ {
		time.Sleep(20 * time.Millisecond)
		conn, err := Dial(s.path, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
	}
	s.wg.Wait()
}

func (s *Server) createAndConnect() (syscall.Handle, error) {
	pathPtr, err := syscall.UTF16PtrFromString(s.path)
	if err != nil {
		return 0, err
	}

	r1, _, e1 := procCreateNamedPipeW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(pipeAccessDuplex),
		uintptr(pipeWait),
		uintptr(pipeUnlimitedInstances), 65536, 65536, 0, 0,
	)
	handle := syscall.Handle(r1)
	if handle == syscall.InvalidHandle {
		return 0, fmt.Errorf("CreateNamedPipeW: %w", e1)
	}

	r2, _, e2 := procConnectNamedPipe.Call(uintptr(handle), 0)
	if r2 != 0 {
		return handle, nil
	}
	errNo := e2.(syscall.Errno)
	if errNo == errorPipeConnected {
		return handle, nil
	}

	syscall.CloseHandle(handle)
	return 0, fmt.Errorf("ConnectNamedPipe: %w", e2)
}

func (s *Server) handleConn(h syscall.Handle) {
	defer s.wg.Done()
	defer procDisconnectNamedPipe.Call(uintptr(h))
	defer syscall.CloseHandle(h)

	f := os.NewFile(uintptr(h), "pipe")
	if f == nil {
		return
	}
	defer f.Close()

	conn := &pipeFile{f: f, h: h, rwTO: s.rwTO}

	for {
		var req ipc.Request
		if err := ipc.Decode(conn, &req); err != nil {
			return
		}

		resp := s.handler(req)
		if err := ipc.Encode(conn, resp); err != nil {
			return
		}

		if req.Type == "shutdown" {
			go s.Shutdown()
		}
	}
}

type pipeFile struct {
	f    *os.File
	h    syscall.Handle
	rwTO time.Duration
}

func (p *pipeFile) Read(b []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := p.f.Read(b)
		done <- result{n, err}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-time.After(p.rwTO):
		procCancelIoEx.Call(uintptr(p.h), 0)
		<-done
		return 0, ErrTimeout
	}
}

func (p *pipeFile) Write(b []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := p.f.Write(b)
		done <- result{n, err}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-time.After(p.rwTO):
		procCancelIoEx.Call(uintptr(p.h), 0)
		<-done
		return 0, ErrTimeout
	}
}

func Dial(path string, timeout time.Duration) (*ClientConn, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	ok, err := waitNamedPipe(pathPtr, timeout)
	if err != nil {
		return nil, fmt.Errorf("npipe dial wait: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("npipe dial: %w", ErrTimeout)
	}

	r1, _, e1 := procCreateFileW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(genericRead|genericWrite),
		0, 0,
		uintptr(openExisting),
		0, 0,
	)
	handle := syscall.Handle(r1)
	if handle == syscall.InvalidHandle {
		return nil, fmt.Errorf("CreateFileW: %w", e1)
	}

	f := os.NewFile(uintptr(handle), "pipe")
	if f == nil {
		syscall.CloseHandle(handle)
		return nil, errors.New("npipe dial: os.NewFile failed")
	}

	return &ClientConn{
		pf: &pipeFile{f: f, h: handle, rwTO: defaultRWTimeout},
	}, nil
}

type ClientConn struct {
	pf *pipeFile
}

func (c *ClientConn) Call(req ipc.Request) (ipc.Response, error) {
	if err := ipc.Encode(c.pf, req); err != nil {
		return ipc.Response{}, err
	}
	var resp ipc.Response
	if err := ipc.Decode(c.pf, &resp); err != nil {
		return ipc.Response{}, err
	}
	return resp, nil
}

func (c *ClientConn) Close() error {
	return c.pf.f.Close()
}

func waitNamedPipe(path *uint16, timeout time.Duration) (bool, error) {
	r, _, err := procWaitNamedPipeW.Call(
		uintptr(unsafe.Pointer(path)),
		uintptr(timeout.Milliseconds()),
	)
	if r == 0 {
		if errNo, ok := err.(syscall.Errno); ok && errNo == errorSemTimeout {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

var (
	_ io.Reader = (*pipeFile)(nil)
	_ io.Writer = (*pipeFile)(nil)
)
