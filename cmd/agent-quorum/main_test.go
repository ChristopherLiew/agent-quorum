package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("run(version) = %d (stderr %q), want 0", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != version {
		t.Fatalf("run(version) stdout = %q, want %q", out.String(), version)
	}
}

// TestHookFailsOpen protects the property that matters above all: a broken
// agent-quorum is invisible. Every failure path of `hook` yields exit 0 and
// empty stdout.
func TestHookFailsOpen(t *testing.T) {
	t.Setenv("AGENT_QUORUM_HOME", shortTemp(t))
	t.Setenv("AGENT_QUORUM_NO_START", "1") // never spawn the test binary as a daemon
	edit := `{"session_id":"s1","cwd":"/tmp","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/tmp/x.go"}}`
	cases := map[string]string{
		"no session":   `{"hook_event_name":"PreToolUse","tool_name":"Edit"}`,
		"no daemon":    edit,
		"invalid json": `{"hook_event_name":`,
		"empty":        ``,
		"unknown tool": `{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/tmp/x"}}`,
		"wrong types":  `{"session_id":42,"hook_event_name":["x"],"tool_input":"nope"}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			var out, errb bytes.Buffer
			start := time.Now()
			if code := run([]string{"hook", "--agent", "claude"}, strings.NewReader(in), &out, &errb); code != 0 {
				t.Errorf("run(hook %s) = %d, want 0", name, code)
			}
			if out.Len() != 0 {
				t.Errorf("run(hook %s) stdout = %q, want empty", name, out.String())
			}
			if d := time.Since(start); d > 400*time.Millisecond {
				t.Errorf("run(hook %s) took %v, want <= 400ms", name, d)
			}
		})
	}
}

func TestHookPanicFailsOpen(t *testing.T) {
	orig := hookClaude
	t.Cleanup(func() { hookClaude = orig })
	hookClaude = func([]byte) []byte { panic("boom") }
	var out, errb bytes.Buffer
	if code := run([]string{"hook", "--agent", "claude"}, strings.NewReader(`{}`), &out, &errb); code != 0 || out.Len() != 0 {
		t.Fatalf("run(hook, panicking adapter) = %d, stdout %q, want 0 and empty", code, out.String())
	}
}

func TestUnknownAgentFailsOpen(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"hook", "--agent", "nope"}, strings.NewReader(`{}`), &out, &errb); code != 0 || out.Len() != 0 {
		t.Fatalf("run(hook --agent nope) = %d, stdout %q, want 0 and empty", code, out.String())
	}
}

// ---- end to end: the real binary, a real daemon, a real git repo ----

// macOS limits unix socket paths to about 104 bytes, so t.TempDir() is too long.
func shortTemp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "aq")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type e2e struct {
	t    *testing.T
	bin  string
	home string
	repo string
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	e := &e2e{t: t, home: shortTemp(t)}
	e.bin = filepath.Join(shortTemp(t), "agent-quorum")
	if out, err := exec.Command("go", "build", "-o", e.bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build err = %v, output %s, want nil", err, out)
	}
	repo, err := filepath.EvalSymlinks(shortTemp(t))
	if err != nil {
		t.Fatal(err)
	}
	e.repo = repo
	e.git("init", "-q")
	e.git("config", "user.email", "t@example.com")
	e.git("config", "user.name", "t")
	for _, f := range []string{"a.go", "b.go"} {
		e.write(f, "package x\n")
	}
	e.git("add", "a.go", "b.go")
	e.git("commit", "-q", "-m", "init")
	t.Cleanup(func() { e.cli(nil, "stop") })
	return e
}

func (e *e2e) git(args ...string) {
	e.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = e.repo
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %v err = %v, output %s, want nil", args, err, out)
	}
}

func (e *e2e) write(name, content string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.repo, name), []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *e2e) cli(env []string, args ...string) (stdout string, code int) {
	e.t.Helper()
	return e.runBin("", env, args...)
}

func (e *e2e) runBin(stdin string, env []string, args ...string) (string, int) {
	e.t.Helper()
	out, code, err := e.tryBin(stdin, env, args...)
	if err != nil {
		e.t.Fatal(err)
	}
	return out, code
}

// tryBin runs the binary and returns its stdout and exit code. Unlike runBin
// it never calls t.Fatal, so goroutines other than the test's may use it.
func (e *e2e) tryBin(stdin string, env []string, args ...string) (stdout string, code int, err error) {
	cmd := exec.Command(e.bin, args...)
	cmd.Dir = e.repo
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(append(os.Environ(), "AGENT_QUORUM_HOME="+e.home), env...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), ee.ExitCode(), nil
	}
	if err != nil {
		return out.String(), 0, fmt.Errorf("run(%v) err = %w, want nil or ExitError", args, err)
	}
	return out.String(), 0, nil
}

// tryHook is hook without t.Fatal, so goroutines other than the test's may
// use it.
func (e *e2e) tryHook(sid string, pid int, payload map[string]any) (string, error) {
	payload["session_id"] = sid
	payload["cwd"] = e.repo
	raw, err := sonic.ConfigStd.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal hook payload: %w", err)
	}
	out, code, err := e.tryBin(string(raw), []string{fmt.Sprintf("AGENT_QUORUM_PID=%d", pid)}, "hook", "--agent", "claude")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("hook exit = %d, want 0", code)
	}
	return out, nil
}

// hook runs one hook call as session sid whose agent process is pid.
func (e *e2e) hook(sid string, pid int, payload map[string]any) string {
	e.t.Helper()
	out, err := e.tryHook(sid, pid, payload)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *e2e) editPayload(file string) map[string]any {
	return map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": filepath.Join(e.repo, file)},
	}
}

func (e *e2e) edit(sid string, pid int, file string) string {
	e.t.Helper()
	return e.hook(sid, pid, e.editPayload(file))
}

func (e *e2e) bash(sid string, pid int, command string) string {
	e.t.Helper()
	return e.hook(sid, pid, map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": command},
	})
}

// sleeper stands in for an agent process: alive until killed.
func sleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

func wantDeny(t *testing.T, out, contains string) {
	t.Helper()
	var v struct {
		H struct {
			Event    string `json:"hookEventName"`
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := sonic.ConfigStd.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("Unmarshal(%q) err = %v, want a deny JSON object", out, err)
	}
	if v.H.Decision != "deny" || v.H.Event != "PreToolUse" || !strings.Contains(v.H.Reason, contains) {
		t.Fatalf("hook output = %q, want a PreToolUse deny mentioning %q", out, contains)
	}
}

func wantGrant(t *testing.T, out string) {
	t.Helper()
	if out != "" {
		t.Fatalf("hook output = %q, want empty (grant)", out)
	}
}

func TestEndToEnd(t *testing.T) {
	e := newE2E(t)
	a, b := sleeper(t), sleeper(t)
	pa, pb := a.Process.Pid, b.Process.Pid

	// Session start: A alone hears nothing; the daemon starts on demand.
	wantGrant(t, e.hook("A", pa, map[string]any{"hook_event_name": "SessionStart", "source": "startup"}))
	e.hook("A", pa, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "refactor token refresh"})

	// A edits a.go: granted silently, never "allow".
	wantGrant(t, e.edit("A", pa, "a.go"))
	e.write("a.go", "package x // A\n")
	wantGrant(t, e.edit("A", pa, "a.go"))

	// B starts and is told about A.
	ctx := e.hook("B", pb, map[string]any{"hook_event_name": "SessionStart", "source": "startup"})
	if !strings.Contains(ctx, "additionalContext") || !strings.Contains(ctx, "claude:A") || !strings.Contains(ctx, "a.go") {
		t.Fatalf("B session start output = %q, want additionalContext naming claude:A and a.go", ctx)
	}

	// B is refused a.go, with the holder and its note in the reason; b.go is free.
	wantDeny(t, e.edit("B", pb, "a.go"), "claude:A")
	wantDeny(t, e.edit("B", pb, "a.go"), "refactor token refresh")
	wantGrant(t, e.edit("B", pb, "b.go"))
	e.write("b.go", "package x // B\n")

	// Git guard: tree rewrites and index sweeps are refused while the other holds files.
	wantDeny(t, e.bash("B", pb, "git stash"), "a.go")
	wantDeny(t, e.bash("B", pb, "cd . && git add -A && git commit -m x"), "git add <paths>")
	wantDeny(t, e.bash("A", pa, "git checkout ."), "b.go")
	wantGrant(t, e.bash("B", pb, "git add b.go && git commit -m mine"))
	wantGrant(t, e.bash("B", pb, "git status"))

	// status shows both sessions and their files.
	st, _ := e.cli(nil, "status")
	for _, want := range []string{"claude:A", "claude:B", "a.go", "b.go", "refactor token refresh"} {
		if !strings.Contains(st, want) {
			t.Fatalf("status = %q, want it to contain %q", st, want)
		}
	}

	// Pre-commit backstop: B staging A's file is caught; staging its own is fine.
	e.git("add", "a.go")
	if _, code := e.cli([]string{"AGENT_QUORUM_SESSION=claude:B"}, "check", "--staged"); code != 1 {
		t.Fatalf("check --staged by B with A's file staged = exit %d, want 1", code)
	}
	if _, code := e.cli([]string{"AGENT_QUORUM_SESSION=claude:A"}, "check", "--staged"); code != 0 {
		t.Fatalf("check --staged by the holder = exit %d, want 0", code)
	}
	e.git("restore", "--staged", "a.go")

	// The daemon survives SIGKILL-style loss: stop it, the next hook restarts it with claims intact.
	e.cli(nil, "stop")
	wantDeny(t, e.edit("B", pb, "a.go"), "claude:A")

	// B commits b.go: the file is clean, but stays B's for the grace period
	// (covered in core tests with a fake clock). A's a.go stays held while dirty.
	// Session A dies without SessionEnd: its files free up with no manual step.
	_ = a.Process.Kill()
	_ = a.Wait()
	wantGrant(t, e.edit("B", pb, "a.go"))
	wantGrant(t, e.bash("B", pb, "git stash"))

	// SessionEnd releases everything.
	e.hook("B", pb, map[string]any{"hook_event_name": "SessionEnd", "reason": "exit"})
	st, _ = e.cli(nil, "status")
	if strings.Contains(st, "claude:") {
		t.Fatalf("status after SessionEnd = %q, want no sessions", st)
	}
}

// TestConcurrentColdStart covers two hooks racing to start the daemon, which
// is normal: both must get an answer.
func TestConcurrentColdStart(t *testing.T) {
	e := newE2E(t)
	p := sleeper(t).Process.Pid
	const n = 8
	type result struct {
		out string
		err error
	}
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		go func() {
			// Not e.edit: t.Fatal must not be called off the test goroutine.
			out, err := e.tryHook(fmt.Sprintf("S%d", i), p, e.editPayload("a.go"))
			results <- result{out: out, err: err}
		}()
	}
	grants := 0
	for i := 0; i < n; i++ {
		r := <-results
		if r.err != nil {
			t.Errorf("concurrent edit hook err = %v, want nil", r.err)
			continue
		}
		if r.out == "" {
			grants++
		}
	}
	// Exactly one session wins a.go. A hook that failed open would also print
	// nothing, so more than one "grant" means a lost request.
	if grants != 1 {
		t.Fatalf("concurrent claimants granted = %d of %d, want 1", grants, n)
	}
}
