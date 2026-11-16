// Package daemon is the impure shell around the core: the Unix socket server,
// the single-instance lock, the state snapshot and the real Env.
package daemon

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/bytedance/sonic"

	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

// SocketFile, LockFile, StateFile and LogFile are the file names used inside
// Options.Home.
const (
	SocketFile = "agent-quorum.sock"
	LockFile   = "daemon.lock"
	StateFile  = "state.json"
	LogFile    = "daemon.log"
)

// connDeadline bounds one connection, maxLine caps a request and drainWait
// is how long Serve waits for in-flight connections on shutdown.
const (
	connDeadline = 2 * time.Second
	maxLine      = 1 << 20 // request size cap
	drainWait    = time.Second
)

// ErrAlreadyRunning is returned by Serve when another daemon holds the lock
// for the same home.
var ErrAlreadyRunning = errors.New("daemon: already running")

// Handler answers one request. core.State satisfies it.
type Handler interface {
	Handle(proto.Request) proto.Response
}

// Persister turns the state into a snapshot and back.
type Persister interface {
	Export() ([]byte, error)
	Import([]byte) error
}

// Options configures a daemon.
type Options struct {
	Home      string                           // runtime directory holding the socket, lock and snapshot; required
	Handler   Handler                          // answers every request except OpShutdown; required
	Persister Persister                        // may be nil: no snapshots
	Logf      func(format string, args ...any) // may be nil: no logging
}

type server struct {
	o        Options
	logf     func(format string, args ...any)
	snapMu   sync.Mutex
	stopOnce sync.Once
	stop     chan struct{}
}

// Serve runs the daemon until ctx is cancelled or a shutdown request arrives,
// then returns nil. It returns ErrAlreadyRunning if another daemon owns Home.
func Serve(ctx context.Context, o Options) error {
	if o.Home == "" {
		return errors.New("daemon: empty Home")
	}
	if o.Handler == nil {
		return errors.New("daemon: nil Handler")
	}
	s := &server{o: o, logf: o.Logf, stop: make(chan struct{})}
	if s.logf == nil {
		s.logf = func(string, ...any) {}
	}

	if err := os.MkdirAll(o.Home, 0o700); err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	if err := os.Chmod(o.Home, 0o700); err != nil {
		return fmt.Errorf("daemon: %w", err)
	}

	// Single instance. The lock is held until Serve returns (or the process
	// dies); the descriptor is close-on-exec, so git children never inherit it.
	lock, err := os.OpenFile(filepath.Join(o.Home, LockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	defer lock.Close()

	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrAlreadyRunning
		}
		return fmt.Errorf("daemon: flock: %w", err)
	}

	s.load()

	// We hold the lock, so any socket file left behind is stale.
	sock := filepath.Join(o.Home, SocketFile)
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("daemon: remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	defer os.Remove(sock)
	defer ln.Close()
	if err := os.Chmod(sock, 0o600); err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	s.logf("listening on %s (pid %d)", sock, os.Getpid())

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-s.stop:
		case <-done:
		}
		_ = ln.Close() // unblocks Accept; the error adds nothing
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			s.logf("accept: %v", err)
			select { // e.g. EMFILE: back off instead of spinning
			case <-time.After(50 * time.Millisecond):
			case <-ctx.Done():
			case <-s.stop:
			}
			continue
		}
		wg.Go(func() { s.serveConn(conn) })
	}

	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(drainWait):
	}
	s.logf("stopped")
	return nil
}

// load reads the snapshot, if any. A missing or corrupt file means an empty
// state, never a failed start.
func (s *server) load() {
	if s.o.Persister == nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(s.o.Home, StateFile))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logf("snapshot: read: %v", err)
		}
		return
	}
	if err := s.o.Persister.Import(data); err != nil {
		s.logf("snapshot: ignoring corrupt %s: %v", StateFile, err)
	}
}

// persist writes the snapshot: temp file, then rename. Failures are logged.
func (s *server) persist() {
	if s.o.Persister == nil {
		return
	}
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	data, err := s.o.Persister.Export()
	if err != nil {
		s.logf("snapshot: export: %v", err)
		return
	}
	path := filepath.Join(s.o.Home, StateFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		s.logf("snapshot: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		s.logf("snapshot: %v", err)
	}
}

func (s *server) serveConn(conn net.Conn) {
	defer conn.Close()
	defer func() {
		if r := recover(); r != nil {
			s.logf("connection: panic: %v", r)
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(connDeadline))

	line, err := bufio.NewReader(io.LimitReader(conn, maxLine+1)).ReadBytes('\n')
	if err != nil {
		if len(line) == 0 {
			return // silent or vanished client
		}
		if len(line) > maxLine {
			reply(conn, proto.Response{Error: "request too large"})
			return
		}
		// Unterminated line: fall through and try to decode what arrived.
	}
	var req proto.Request
	if err := sonic.ConfigStd.Unmarshal(bytes.TrimSpace(line), &req); err != nil {
		reply(conn, proto.Response{Error: "bad request: " + err.Error()})
		return
	}

	if req.Op == proto.OpShutdown {
		reply(conn, proto.Response{OK: true})
		s.logf("shutdown requested")
		s.stopOnce.Do(func() { close(s.stop) })
		return
	}

	resp, panicked := s.handle(req)
	if !panicked && req.Op != proto.OpStatus && req.Op != proto.OpCheck {
		// Before the answer goes out, so an acknowledged mutation survives a
		// SIGKILL right after it.
		s.persist()
	}
	if resp.Decision == proto.Deny {
		session := req.Session
		if req.Agent != "" {
			session = req.Agent + ":" + req.Session
		}
		s.logf("deny op=%s session=%s reason=%q", req.Op, session, resp.Reason)
	}
	reply(conn, resp)
}

func (s *server) handle(req proto.Request) (resp proto.Response, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("handler panic on op=%s: %v", req.Op, r)
			resp = proto.Response{Error: fmt.Sprintf("internal error: %v", r)}
			panicked = true
		}
	}()
	return s.o.Handler.Handle(req), false
}

func reply(conn net.Conn, resp proto.Response) {
	data, err := sonic.ConfigStd.Marshal(resp)
	if err != nil {
		data = []byte(`{"ok":false,"error":"encode failed"}`)
	}
	_, _ = conn.Write(append(data, '\n'))
}
