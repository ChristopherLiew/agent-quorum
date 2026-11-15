package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bytedance/gg/gslice"
	"github.com/bytedance/sonic"

	"github.com/ChristopherLiew/agent-quorum/internal/client"
	"github.com/ChristopherLiew/agent-quorum/internal/core"
	"github.com/ChristopherLiew/agent-quorum/internal/daemon"
	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

// cliTimeout bounds each daemon call made by the human-facing commands.
const cliTimeout = 3 * time.Second

func runServe(stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	st := core.New(daemon.RealEnv{}, core.Config{})
	logf := func(format string, args ...any) {
		fmt.Fprintf(stderr, time.Now().Format(time.RFC3339)+" "+format+"\n", args...)
	}
	logf("agent-quorum %s serving in %s (pid %d)", version, client.Home(), os.Getpid())
	err := daemon.Serve(ctx, daemon.Options{Home: client.Home(), Handler: st, Persister: st, Logf: logf})
	switch {
	case err == nil:
		return 0
	case errors.Is(err, daemon.ErrAlreadyRunning):
		return 0 // losing the start race is normal
	default:
		logf("serve: %v", err)
		return 1
	}
}

// do sends one request without starting a daemon. down=true means there is
// no daemon to talk to, which for the CLI means "nothing is held".
func do(req proto.Request) (resp proto.Response, down bool, err error) {
	resp, err = client.Do(req, client.Options{Timeout: cliTimeout})
	if err != nil {
		return resp, true, err
	}
	if !resp.OK {
		return resp, false, errors.New(resp.Error)
	}
	return resp, false, nil
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	asJSON := len(args) > 0 && args[0] == "--json"
	resp, down, err := do(proto.Request{Op: proto.OpStatus})
	if err != nil && !down {
		fmt.Fprintf(stderr, "agent-quorum: %v\n", err)
		return 1
	}
	if asJSON {
		if resp.Sessions == nil {
			resp.Sessions = []proto.Holder{}
		}
		// A failed write to stdout cannot be reported anywhere useful.
		_ = sonic.ConfigStd.NewEncoder(stdout).Encode(struct {
			Daemon   bool           `json:"daemon"`
			Sessions []proto.Holder `json:"sessions"`
		}{Daemon: !down, Sessions: resp.Sessions})
		return 0
	}
	if down {
		fmt.Fprintln(stdout, "agent-quorum: daemon not running (no sessions registered)")
		return 0
	}
	if len(resp.Sessions) == 0 {
		fmt.Fprintln(stdout, "agent-quorum: no live sessions")
		return 0
	}
	for _, s := range resp.Sessions {
		line := fmt.Sprintf("%s  pid %d  idle %s  %s", s.Session, s.PID, idle(s.IdleS), s.CWD)
		if s.Note != "" {
			line += fmt.Sprintf("  %q", s.Note)
		}
		fmt.Fprintln(stdout, line)
		if len(s.Paths) == 0 {
			fmt.Fprintln(stdout, "    (holds no files)")
		}
		for _, p := range s.Paths {
			fmt.Fprintln(stdout, "    "+p)
		}
	}
	return 0
}

func idle(s int) string { return (time.Duration(s) * time.Second).String() }

func runRelease(args []string, stdout, stderr io.Writer) int {
	req := proto.Request{Op: proto.OpRelease, Session: os.Getenv("AGENT_QUORUM_SESSION")}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--session" && i+1 < len(args):
			req.Session = args[i+1]
			i++
		case args[i] == "--all":
			req.All = true
		default:
			req.Paths = append(req.Paths, absPath(args[i]))
		}
	}
	if req.Session == "" || (!req.All && len(req.Paths) == 0) {
		fmt.Fprintln(stderr, "usage: agent-quorum release [--session ID] --all | PATH...   (ids are shown by `agent-quorum status`)")
		return 2
	}
	if _, down, err := do(req); err != nil && !down {
		fmt.Fprintf(stderr, "agent-quorum: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "released")
	return 0
}

func runStop(stdout, stderr io.Writer) int {
	if _, down, err := do(proto.Request{Op: proto.OpShutdown}); err != nil && !down {
		fmt.Fprintf(stderr, "agent-quorum: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "stopped")
	return 0
}

// runCheck exits 1 when a path is held by a session other than the caller's.
// It fails open (exit 0) when the daemon is down or git misbehaves.
func runCheck(args []string, _, stderr io.Writer) int {
	var paths []string
	for _, a := range args {
		if a == "--staged" {
			paths = append(paths, stagedPaths()...)
		} else {
			paths = append(paths, absPath(a))
		}
	}
	if len(paths) == 0 {
		return 0
	}
	resp, _, err := do(proto.Request{Op: proto.OpCheck, Session: callerSession(), Paths: paths})
	if err != nil || resp.Decision != proto.Deny {
		return 0
	}
	fmt.Fprintln(stderr, "agent-quorum: these files are held by another agent session:")
	for _, h := range resp.Holders {
		for _, p := range h.Paths {
			fmt.Fprintf(stderr, "  %s  (%s)\n", p, h.Session)
		}
	}
	fmt.Fprintln(stderr, "Unstage them (git restore --staged <path>) and commit only your own files, or ask the user.")
	return 1
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	return client.NormalizePath(p)
}

func gitOut(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	return string(out), err
}

func stagedPaths() []string {
	root, err := gitOut("rev-parse", "--show-toplevel")
	if err != nil {
		return nil
	}
	names, err := gitOut("diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil
	}
	return gslice.FilterMap(strings.Split(names, "\x00"), func(n string) (string, bool) {
		if n == "" {
			return "", false
		}
		return client.NormalizePath(filepath.Join(strings.TrimSpace(root), n)), true
	})
}

// callerSession works out which session is running this command: explicitly
// through $AGENT_QUORUM_SESSION, otherwise by finding a registered session
// whose agent process is one of our ancestors (an agent running `git commit`
// from its shell tool). "" means a human or an unknown agent.
func callerSession() string {
	if s := os.Getenv("AGENT_QUORUM_SESSION"); s != "" {
		return s
	}
	resp, _, err := do(proto.Request{Op: proto.OpStatus})
	if err != nil || len(resp.Sessions) == 0 {
		return ""
	}
	byPID := map[int]string{}
	for _, s := range resp.Sessions {
		if s.PID > 0 {
			byPID[s.PID] = s.Session
		}
	}
	pid := os.Getppid()
	for hop := 0; hop < 20 && pid > 1; hop++ {
		if s, ok := byPID[pid]; ok {
			return s
		}
		out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return ""
		}
		next, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil {
			return ""
		}
		pid = next
	}
	return ""
}

const preCommit = `#!/bin/sh
# Installed by agent-quorum: refuse to commit files held by another agent session.
command -v agent-quorum >/dev/null 2>&1 || exit 0
exec agent-quorum check --staged
`

func runInstallGitHook(stdout, stderr io.Writer) int {
	dir, err := gitOut("rev-parse", "--git-path", "hooks")
	if err != nil {
		fmt.Fprintln(stderr, "agent-quorum: not inside a git repository")
		return 1
	}
	hook := filepath.Join(strings.TrimSpace(dir), "pre-commit")
	if _, err := os.Stat(hook); err == nil {
		fmt.Fprintf(stderr, "agent-quorum: %s already exists; add this line to it yourself:\n  agent-quorum check --staged || exit 1\n", hook)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		fmt.Fprintf(stderr, "agent-quorum: %v\n", err)
		return 1
	}
	if err := os.WriteFile(hook, []byte(preCommit), 0o755); err != nil {
		fmt.Fprintf(stderr, "agent-quorum: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s (needs agent-quorum on PATH)\n", hook)
	return 0
}
