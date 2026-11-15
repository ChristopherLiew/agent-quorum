// Package client is the socket client used by the hook and the CLI: one
// request per connection, a hard deadline, and daemon start on demand.
package client

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bytedance/sonic"

	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

const (
	// Kept equal to daemon.SocketFile and daemon.LogFile (a test checks);
	// duplicated so the hook path does not depend on the daemon package.
	socketFile = "agent-quorum.sock"
	logFile    = "daemon.log"

	defaultTimeout = 250 * time.Millisecond
	redialEvery    = 10 * time.Millisecond
	maxResponse    = 16 << 20
)

// Home returns the runtime directory: $AGENT_QUORUM_HOME, else ~/.agent-quorum.
func Home() string {
	if h := os.Getenv("AGENT_QUORUM_HOME"); h != "" {
		return h
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, ".agent-quorum")
}

// Options configures Do.
type Options struct {
	Home    string        // "" -> Home()
	Timeout time.Duration // total budget for the whole call including daemon start; 0 -> 250ms
	Start   bool          // start the daemon on demand when it is not reachable
	Self    string        // binary to spawn for `serve`; "" -> os.Executable()
}

// Do sends one request and returns the daemon's response. Every failure is an
// error (callers fail open); it never blocks past the deadline.
func Do(req proto.Request, o Options) (resp proto.Response, err error) {
	defer func() {
		if r := recover(); r != nil {
			resp, err = proto.Response{}, fmt.Errorf("client: panic: %v", r)
		}
	}()
	if o.Home == "" {
		o.Home = Home()
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	if req.V == 0 {
		req.V = proto.Version
	}
	deadline := time.Now().Add(o.Timeout)
	sock := filepath.Join(o.Home, socketFile)

	conn, err := dial(sock, deadline)
	if err != nil {
		if !o.Start {
			return proto.Response{}, fmt.Errorf("client: %w", err)
		}
		if err := spawn(o); err != nil {
			return proto.Response{}, fmt.Errorf("client: start daemon: %w", err)
		}
		for {
			if conn, err = dial(sock, deadline); err == nil {
				break
			}
			if time.Until(deadline) <= redialEvery {
				return proto.Response{}, fmt.Errorf("client: daemon did not come up: %w", err)
			}
			time.Sleep(redialEvery)
		}
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return proto.Response{}, fmt.Errorf("client: %w", err)
	}

	data, err := sonic.ConfigStd.Marshal(req)
	if err != nil {
		return proto.Response{}, fmt.Errorf("client: %w", err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return proto.Response{}, fmt.Errorf("client: write: %w", err)
	}
	line, err := bufio.NewReader(io.LimitReader(conn, maxResponse)).ReadBytes('\n')
	if err != nil && (len(line) == 0 || !errors.Is(err, io.EOF)) {
		return proto.Response{}, fmt.Errorf("client: read: %w", err)
	}
	if err := sonic.ConfigStd.Unmarshal(line, &resp); err != nil {
		return proto.Response{}, fmt.Errorf("client: bad response: %w", err)
	}
	return resp, nil
}

func dial(sock string, deadline time.Time) (net.Conn, error) {
	if !time.Now().Before(deadline) {
		return nil, os.ErrDeadlineExceeded
	}
	d := net.Dialer{Deadline: deadline}
	return d.Dial("unix", sock)
}

// spawn starts `Self serve` in its own session so it outlives this process
// and its terminal. Losing the race to another starter is fine: the daemon's
// lock makes the extra one exit.
func spawn(o Options) error {
	exe := o.Self
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(o.Home, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(o.Home, logFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "serve")
	cmd.Stdin = nil
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Env = append(os.Environ(), "AGENT_QUORUM_HOME="+o.Home)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// NormalizePath cleans an absolute path and resolves symlinks, so that every
// session names a file the same way. The file need not exist: the nearest
// existing ancestor is resolved and the rest re-joined. It never fails; on
// any error the cleaned path is returned. Relative input is only cleaned.
func NormalizePath(p string) string {
	if p == "" {
		return ""
	}
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) {
		return clean
	}
	dir, rest := clean, ""
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return clean
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}
