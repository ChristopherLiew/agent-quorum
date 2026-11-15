package daemon_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bytedance/sonic"

	"github.com/ChristopherLiew/agent-quorum/internal/client"
	"github.com/ChristopherLiew/agent-quorum/internal/daemon"
	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

// fakeHandler echoes the note back and records what it saw.
type fakeHandler struct {
	mu   sync.Mutex
	reqs []proto.Request
}

func (h *fakeHandler) Handle(req proto.Request) proto.Response {
	h.mu.Lock()
	h.reqs = append(h.reqs, req)
	h.mu.Unlock()
	switch req.Op {
	case "panic":
		panic("boom")
	case proto.OpClaim:
		return proto.Response{OK: true, Decision: proto.Deny, Reason: "held by someone"}
	}
	return proto.Response{OK: true, Context: "echo:" + req.Note}
}

func (h *fakeHandler) ops() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ops []string
	for _, r := range h.reqs {
		ops = append(ops, r.Op)
	}
	return ops
}

// fakePersister's state is the list of notes it was told about.
type fakePersister struct {
	mu        sync.Mutex
	state     string
	exports   int
	imported  []string
	importErr error
}

func (p *fakePersister) Export() ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exports++
	return []byte(p.state), nil
}

func (p *fakePersister) Import(b []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.imported = append(p.imported, string(b))
	return p.importErr
}

func (p *fakePersister) set(s string) {
	p.mu.Lock()
	p.state = s
	p.mu.Unlock()
}

type logger struct {
	mu    sync.Mutex
	lines []string
}

func (l *logger) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logger) contains(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// tempHome returns a short path: macOS caps unix socket paths at ~104 bytes,
// which t.TempDir() can exceed.
func tempHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "aq")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type running struct {
	cancel context.CancelFunc
	done   chan error
}

// stop cancels the daemon and returns Serve's result.
func (r *running) stop(t *testing.T) error {
	t.Helper()
	r.cancel()
	return r.wait(t)
}

func (r *running) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		r.done <- err // keep it for a later stop/cleanup
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
		return nil
	}
}

func start(t *testing.T, o daemon.Options) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- daemon.Serve(ctx, o) }()
	t.Cleanup(func() { _ = r.stop(t) })
	waitUp(t, o.Home, r)
	return r
}

func waitUp(t *testing.T, home string, r *running) {
	t.Helper()
	sock := filepath.Join(home, daemon.SocketFile)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-r.done:
			r.done <- err
			t.Fatalf("Serve() = %v before listening, want it to keep serving", err)
		default:
		}
		if c, err := net.Dial("unix", sock); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Dial(%s) did not succeed within 5s, want daemon listening", sock)
}

func do(t *testing.T, home string, req proto.Request) proto.Response {
	t.Helper()
	resp, err := client.Do(req, client.Options{Home: home, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("client.Do(%s) err = %v, want nil", req.Op, err)
	}
	return resp
}

func TestRoundTrip(t *testing.T) {
	home := tempHome(t)
	h := &fakeHandler{}
	start(t, daemon.Options{Home: home, Handler: h})

	resp := do(t, home, proto.Request{Op: proto.OpNote, Agent: "claude", Session: "s1", Note: "hi", Paths: []string{"/a", "/b"}})
	if !resp.OK || resp.Context != "echo:hi" {
		t.Fatalf("Do(note) = %+v, want OK with Context %q", resp, "echo:hi")
	}
	h.mu.Lock()
	got := h.reqs[0]
	h.mu.Unlock()
	if got.V != proto.Version || got.Agent != "claude" || got.Session != "s1" || len(got.Paths) != 2 {
		t.Fatalf("handler request = %+v, want V %d, agent claude, session s1, 2 paths", got, proto.Version)
	}
}

func TestSilentClientDoesNotBlockOthers(t *testing.T) {
	home := tempHome(t)
	start(t, daemon.Options{Home: home, Handler: &fakeHandler{}})

	silent, err := net.Dial("unix", filepath.Join(home, daemon.SocketFile))
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	// A second one that sends half a line and stalls.
	half, err := net.Dial("unix", filepath.Join(home, daemon.SocketFile))
	if err != nil {
		t.Fatal(err)
	}
	defer half.Close()
	_, _ = fmt.Fprint(half, `{"v":1,"op":"no`)

	begin := time.Now()
	for i := 0; i < 5; i++ {
		if resp := do(t, home, proto.Request{Op: proto.OpStatus}); !resp.OK {
			t.Fatalf("Do(status) = %+v, want OK", resp)
		}
	}
	if el := time.Since(begin); el > time.Second {
		t.Fatalf("5 requests behind a silent client took %v, want <= 1s", el)
	}
}

func raw(t *testing.T, home, payload string) proto.Response {
	t.Helper()
	c, err := net.Dial("unix", filepath.Join(home, daemon.SocketFile))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		t.Fatalf("ReadBytes() = %q, %v, want a full line and nil error", line, err)
	}
	var resp proto.Response
	if err := sonic.ConfigStd.Unmarshal(line, &resp); err != nil {
		t.Fatalf("Unmarshal(%q) err = %v, want nil", line, err)
	}
	return resp
}

func TestGarbageInput(t *testing.T) {
	home := tempHome(t)
	h := &fakeHandler{}
	start(t, daemon.Options{Home: home, Handler: h})

	for _, payload := range []string{"this is not json\n", "[1,2,3]\n", "\n", "{\"op\":\n"} {
		resp := raw(t, home, payload)
		if resp.OK || resp.Error == "" {
			t.Errorf("payload %q: resp = %+v, want ok:false with error", payload, resp)
		}
	}
	if resp := raw(t, home, strings.Repeat("x", 1<<20+10)+"\n"); resp.OK || resp.Error == "" {
		t.Errorf("raw(oversized) = %+v, want ok:false with error", resp)
	}
	if len(h.ops()) != 0 {
		t.Errorf("handler ops after garbage = %v, want none", h.ops())
	}
	if resp := do(t, home, proto.Request{Op: proto.OpStatus}); !resp.OK {
		t.Fatalf("Do(status) after garbage = %+v, want OK", resp)
	}
}

func TestHandlerPanic(t *testing.T) {
	home := tempHome(t)
	p := &fakePersister{}
	start(t, daemon.Options{Home: home, Handler: &fakeHandler{}, Persister: p})

	resp := do(t, home, proto.Request{Op: "panic"})
	if resp.OK || resp.Error == "" {
		t.Fatalf("Do(panic) = %+v, want ok:false with error", resp)
	}
	if resp := do(t, home, proto.Request{Op: proto.OpStatus}); !resp.OK {
		t.Fatalf("Do(status) after panic = %+v, want OK", resp)
	}
}

func TestDenyIsLogged(t *testing.T) {
	home := tempHome(t)
	l := &logger{}
	start(t, daemon.Options{Home: home, Handler: &fakeHandler{}, Logf: l.logf})

	resp := do(t, home, proto.Request{Op: proto.OpClaim, Agent: "claude", Session: "s1"})
	if resp.Decision != proto.Deny {
		t.Fatalf("Do(claim).Decision = %q, want %q", resp.Decision, proto.Deny)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, "deny") && strings.Contains(line, "op=claim") &&
			strings.Contains(line, "session=claude:s1") && strings.Contains(line, "held by someone") {
			return
		}
	}
	t.Fatalf("log lines = %q, want a deny line for op=claim session=claude:s1", l.lines)
}

func TestSecondServeAlreadyRunning(t *testing.T) {
	home := tempHome(t)
	start(t, daemon.Options{Home: home, Handler: &fakeHandler{}})

	err := daemon.Serve(context.Background(), daemon.Options{Home: home, Handler: &fakeHandler{}})
	if !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Fatalf("second Serve() = %v, want ErrAlreadyRunning", err)
	}
	// The loser must not have disturbed the winner's socket.
	if resp := do(t, home, proto.Request{Op: proto.OpStatus}); !resp.OK {
		t.Fatalf("Do(status) after second Serve = %+v, want OK", resp)
	}
}

func TestTenServesOneSurvives(t *testing.T) {
	home := tempHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const n = 10
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errs <- daemon.Serve(ctx, daemon.Options{Home: home, Handler: &fakeHandler{}}) }()
	}
	for i := 0; i < n-1; i++ {
		select {
		case err := <-errs:
			if !errors.Is(err, daemon.ErrAlreadyRunning) {
				t.Fatalf("loser %d Serve() = %v, want ErrAlreadyRunning", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("losers returned = %d, want %d", i, n-1)
		}
	}
	// Exactly one is left, and it serves.
	resp, err := client.Do(proto.Request{Op: proto.OpStatus}, client.Options{Home: home, Timeout: 3 * time.Second})
	for i := 0; err != nil && i < 200; i++ { // the winner may still be binding
		time.Sleep(5 * time.Millisecond)
		resp, err = client.Do(proto.Request{Op: proto.OpStatus}, client.Options{Home: home, Timeout: 3 * time.Second})
	}
	if err != nil || !resp.OK {
		t.Fatalf("Do(status) = %+v, %v, want OK and nil error", resp, err)
	}
	select {
	case err := <-errs:
		t.Fatalf("tenth Serve() = %v, want it to keep serving", err)
	default:
	}
	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("survivor Serve() after cancel = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("survivor did not stop")
	}
}

func TestStaleSocketReplaced(t *testing.T) {
	home := tempHome(t)
	sock := filepath.Join(home, daemon.SocketFile)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("Lstat(stale socket) err = %v, want nil", err)
	}
	if _, err := client.Do(proto.Request{Op: proto.OpStatus}, client.Options{Home: home}); err == nil {
		t.Fatal("Do(status) over stale socket err = nil, want error")
	}

	start(t, daemon.Options{Home: home, Handler: &fakeHandler{}})
	if resp := do(t, home, proto.Request{Op: proto.OpStatus}); !resp.OK {
		t.Fatalf("Do(status) over stale socket = %+v, want OK", resp)
	}
}

func TestSnapshot(t *testing.T) {
	home := tempHome(t)
	statePath := filepath.Join(home, daemon.StateFile)

	p1 := &fakePersister{}
	r1 := start(t, daemon.Options{Home: home, Handler: &fakeHandler{}, Persister: p1})
	if len(p1.imported) != 0 {
		t.Fatalf("imported with no snapshot on disk = %q, want none", p1.imported)
	}

	// Read-only ops do not snapshot.
	do(t, home, proto.Request{Op: proto.OpStatus})
	do(t, home, proto.Request{Op: proto.OpCheck})
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(state.json) after read-only ops err = %v, want ErrNotExist", err)
	}

	p1.set(`{"claims":1}`)
	do(t, home, proto.Request{Op: proto.OpNote, Note: "x"})
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"claims":1}` {
		t.Fatalf("state.json = %q, want %q", data, `{"claims":1}`)
	}
	fi, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("state.json mode = %v, want 0600", fi.Mode().Perm())
	}
	if _, err := os.Stat(statePath + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(state.json.tmp) err = %v, want ErrNotExist", err)
	}

	p1.set(`{"claims":2}`)
	do(t, home, proto.Request{Op: proto.OpClaim})
	if err := r1.stop(t); err != nil {
		t.Fatal(err)
	}

	p2 := &fakePersister{}
	start(t, daemon.Options{Home: home, Handler: &fakeHandler{}, Persister: p2})
	p2.mu.Lock()
	defer p2.mu.Unlock()
	if len(p2.imported) != 1 || p2.imported[0] != `{"claims":2}` {
		t.Fatalf("imported on restart = %q, want [%q]", p2.imported, `{"claims":2}`)
	}
}

func TestCorruptSnapshotIsNotFatal(t *testing.T) {
	home := tempHome(t)
	if err := os.WriteFile(filepath.Join(home, daemon.StateFile), []byte("{{{ nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &logger{}
	p := &fakePersister{importErr: errors.New("invalid character")}
	start(t, daemon.Options{Home: home, Handler: &fakeHandler{}, Persister: p, Logf: l.logf})

	if resp := do(t, home, proto.Request{Op: proto.OpStatus}); !resp.OK {
		t.Fatalf("Do(status) with corrupt snapshot = %+v, want OK", resp)
	}
	if !l.contains("corrupt") {
		t.Fatalf("log lines = %q, want one mentioning corrupt", l.lines)
	}
}

func TestShutdownOp(t *testing.T) {
	home := tempHome(t)
	h := &fakeHandler{}
	r := start(t, daemon.Options{Home: home, Handler: h})

	resp := do(t, home, proto.Request{Op: proto.OpShutdown})
	if !resp.OK {
		t.Fatalf("Do(shutdown) = %+v, want OK", resp)
	}
	if err := r.wait(t); err != nil {
		t.Fatalf("Serve = %v, want nil", err)
	}
	if _, err := os.Lstat(filepath.Join(home, daemon.SocketFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(socket) after stop err = %v, want ErrNotExist", err)
	}
	if len(h.ops()) != 0 {
		t.Fatalf("handler ops after shutdown = %v, want none", h.ops())
	}
	// The lock is free again.
	start(t, daemon.Options{Home: home, Handler: h})
}

func TestContextCancelRemovesSocket(t *testing.T) {
	home := tempHome(t)
	r := start(t, daemon.Options{Home: home, Handler: &fakeHandler{}})
	if err := r.stop(t); err != nil {
		t.Fatalf("Serve = %v, want nil", err)
	}
	if _, err := os.Lstat(filepath.Join(home, daemon.SocketFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(socket) after stop err = %v, want ErrNotExist", err)
	}
}

func TestModes(t *testing.T) {
	home := filepath.Join(tempHome(t), "h")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	start(t, daemon.Options{Home: home, Handler: &fakeHandler{}})

	fi, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("home mode = %v, want 0700", fi.Mode().Perm())
	}
	fi, err = os.Stat(filepath.Join(home, daemon.SocketFile))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestRealEnv(t *testing.T) {
	env := daemon.RealEnv{}
	if !env.Alive(os.Getpid()) {
		t.Error("Alive(own pid) = false, want true")
	}
	if env.Alive(0) || env.Alive(-1) {
		t.Error("Alive(0) or Alive(-1) = true, want false")
	}
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip("cannot run `true`:", err)
	}
	if env.Alive(cmd.Process.Pid) {
		t.Errorf("Alive(%d) = true for reaped pid, want false", cmd.Process.Pid)
	}
	if time.Since(env.Now()) > time.Minute {
		t.Error("time.Since(Now()) > 1m, want about 0")
	}
	if got := env.RepoRoot(t.TempDir()); got != "" {
		t.Errorf("RepoRoot(non-repo) = %q, want empty", got)
	}
	if _, known := env.Dirty("", "/x"); known {
		t.Error("Dirty(\"\", \"/x\") known = true, want false")
	}
}
