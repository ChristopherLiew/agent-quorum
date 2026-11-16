package main

import (
	"io"
	"os"
	"time"

	"github.com/bytedance/sonic"

	"github.com/ChristopherLiew/agent-quorum/internal/adapter/antigravity"
	"github.com/ChristopherLiew/agent-quorum/internal/adapter/claude"
	"github.com/ChristopherLiew/agent-quorum/internal/adapter/codex"
	"github.com/ChristopherLiew/agent-quorum/internal/adapter/opencode"
	"github.com/ChristopherLiew/agent-quorum/internal/client"
	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

// hookTimeout and byeTimeout are the budgets for one hook call, including
// starting the daemon if needed. They are far above the usual cost (a few
// ms) because the slow path is the one that matters: a contended claim makes
// the daemon run `git status`, and timing out there would fail open exactly
// when a deny is due.
const (
	hookTimeout = 1500 * time.Millisecond
	byeTimeout  = 800 * time.Millisecond // SessionEnd hooks share a 1.5 s budget
)

// runHook is the hook entry point. It FAILS OPEN: whatever goes wrong (bad
// input, no daemon, timeout, panic) the result is exit 0 with empty stdout,
// which every supported agent reads as "no opinion".
func runHook(args []string, stdin io.Reader, stdout io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			code = 0
		}
	}()

	agent := "claude"
	for i := 0; i < len(args); i++ {
		if args[i] == "--agent" && i+1 < len(args) {
			agent = args[i+1]
			i++
		}
	}

	// A failed read leaves a partial payload; the parser rejects it and the
	// hook fails open, so the error needs no separate handling.
	payload, _ := io.ReadAll(io.LimitReader(stdin, 8<<20))
	capture(payload)

	// Built per call, not at package level: tests swap the hook variables.
	hooks := map[string]func([]byte) []byte{
		"claude":      hookClaude,
		"codex":       hookCodex,
		"antigravity": hookAntigravity,
		"opencode":    hookOpenCode,
	}
	hook, ok := hooks[agent]
	if !ok {
		return 0 // no adapter for this agent yet
	}
	// Nowhere to report a broken stdout; the hook fails open anyway.
	if out := hook(payload); len(out) > 0 {
		_, _ = stdout.Write(out)
	}
	return 0
}

var hookCodex = func(payload []byte) []byte {
	ev, err := codex.Parse(payload)
	if err != nil || !ev.HasReq {
		return nil
	}
	resp, ok := ask(ev.Req, agentPID())
	if !ok {
		return nil
	}
	return codex.Render(ev, resp).Stdout
}

var hookAntigravity = func(payload []byte) []byte {
	ev, err := antigravity.Parse(payload)
	if err != nil || !ev.HasReq {
		return nil
	}
	resp, ok := ask(ev.Req, agentPID())
	if !ok {
		return nil
	}
	return antigravity.Render(ev, resp).Stdout
}

var hookOpenCode = func(payload []byte) []byte {
	ev, err := opencode.Parse(payload)
	if err != nil || !ev.HasReq {
		return nil
	}
	resp, ok := ask(ev.Req, ev.PID)
	if !ok {
		return nil
	}
	return opencode.Render(ev, resp).Stdout
}

// hookClaude is swapped in tests to prove that a panicking adapter fails open.
var hookClaude = func(payload []byte) []byte {
	ev, err := claude.Parse(payload)
	if err != nil || !ev.HasReq {
		return nil
	}
	resp, ok := ask(ev.Req, agentPID())
	if !ok {
		return nil
	}
	return claude.Render(ev, resp).Stdout
}

// ask sends an adapter's request to the daemon with the impure fields filled
// in: the agent pid (0 when unknown) and normalized paths. It reports false when the hook
// should stay silent, either because notes are disabled or because the daemon
// could not be reached in time.
func ask(req proto.Request, pid int) (proto.Response, bool) {
	if os.Getenv("AGENT_QUORUM_NO_NOTES") == "1" {
		// Adapters that register on every prompt send the note with hello.
		if req.Op == proto.OpNote {
			return proto.Response{}, false
		}
		req.Note = ""
	}
	req.PID = pid
	for i, p := range req.Paths {
		req.Paths[i] = client.NormalizePath(p)
	}
	timeout := hookTimeout
	if req.Op == proto.OpBye {
		timeout = byeTimeout
	}
	resp, err := client.Do(req, client.Options{
		Timeout: timeout,
		// Never start a daemon just to say goodbye to it.
		Start: req.Op != proto.OpBye && os.Getenv("AGENT_QUORUM_NO_START") != "1",
	})
	if err != nil {
		return proto.Response{}, false
	}
	return resp, true
}

// agentPID is the pid of the agent process that ran this hook. The plugin's
// hooks use exec form and the launcher uses `exec`, so our parent is the
// agent itself. AGENT_QUORUM_PID overrides it (tests, unusual launchers).
func agentPID() int {
	if v := os.Getenv("AGENT_QUORUM_PID"); v != "" {
		var pid int
		if sonic.ConfigStd.Unmarshal([]byte(v), &pid) == nil && pid > 0 {
			return pid
		}
	}
	return os.Getppid()
}

// capture appends raw hook payloads to $AGENT_QUORUM_CAPTURE, one JSON object
// per line, to record real fixtures from your own agent versions.
func capture(payload []byte) {
	path := os.Getenv("AGENT_QUORUM_CAPTURE")
	if path == "" || len(payload) == 0 {
		return
	}
	rec := struct {
		TS      string                 `json:"ts"`
		PPID    int                    `json:"ppid"`
		Args    []string               `json:"args"`
		Payload sonic.NoCopyRawMessage `json:"payload"`
	}{
		TS:      time.Now().Format(time.RFC3339),
		PPID:    os.Getppid(),
		Args:    os.Args[1:],
		Payload: payload,
	}
	line, err := sonic.ConfigStd.Marshal(rec) // fails if the payload is not valid JSON
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	// Best effort: capture is a debugging aid and must never affect the hook.
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}
