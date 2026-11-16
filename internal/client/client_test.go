package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ChristopherLiew/agent-quorum/internal/daemon"
	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

type echoHandler struct{}

func (echoHandler) Handle(req proto.Request) proto.Response {
	return proto.Response{OK: true, Context: "echo:" + req.Note, Reason: strconv.Itoa(req.V)}
}

// tempHome returns a short path: macOS caps unix socket paths at ~104 bytes.
func tempHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "aq")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestFileNamesMatchDaemon(t *testing.T) {
	if socketFile != daemon.SocketFile || logFile != daemon.LogFile {
		t.Fatalf("client file names = (%q, %q), want daemon's (%q, %q)",
			socketFile, logFile, daemon.SocketFile, daemon.LogFile)
	}
}

func TestHome(t *testing.T) {
	t.Setenv("AGENT_QUORUM_HOME", "/some/where")
	if got := Home(); got != "/some/where" {
		t.Fatalf("Home() = %q, want %q", got, "/some/where")
	}
	t.Setenv("AGENT_QUORUM_HOME", "")
	t.Setenv("HOME", "/users/x")
	if got := Home(); got != "/users/x/.agent-quorum" {
		t.Fatalf("Home() = %q, want %q", got, "/users/x/.agent-quorum")
	}
}

func TestDoAgainstRealDaemon(t *testing.T) {
	home := tempHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(ctx, daemon.Options{Home: home, Handler: echoHandler{}}) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("daemon stopped within 5s = false, want true")
		}
	})

	var resp proto.Response
	var err error
	for i := 0; i < 500; i++ {
		if resp, err = Do(proto.Request{Op: proto.OpNote, Note: "hi"}, Options{Home: home, Timeout: time.Second}); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Context != "echo:hi" {
		t.Fatalf("Do(note) = %+v, want OK with Context %q", resp, "echo:hi")
	}
	if resp.Reason != strconv.Itoa(proto.Version) {
		t.Fatalf("Do(note).Reason = %s, want %d", resp.Reason, proto.Version)
	}

	// Home falls back to $AGENT_QUORUM_HOME.
	t.Setenv("AGENT_QUORUM_HOME", home)
	if resp, err = Do(proto.Request{Op: proto.OpStatus}, Options{}); err != nil || !resp.OK {
		t.Fatalf("Do(status) via env = %+v, %v, want OK and nil error", resp, err)
	}
}

func TestNoDaemonFailsFast(t *testing.T) {
	home := tempHome(t)
	for _, name := range []string{"missing socket", "missing home"} {
		h := home
		if name == "missing home" {
			h = filepath.Join(home, "nope")
		}
		begin := time.Now()
		_, err := Do(proto.Request{Op: proto.OpStatus}, Options{Home: h})
		if err == nil {
			t.Fatalf("Do(%s) err = nil, want error", name)
		}
		if el := time.Since(begin); el > 50*time.Millisecond {
			t.Fatalf("Do(%s) took %v, want <= 50ms", name, el)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Stat(nope) after Do without Start = exists, want ErrNotExist")
	}
}

func TestDeadlineAgainstMuteListener(t *testing.T) {
	home := tempHome(t)
	ln, err := net.Listen("unix", filepath.Join(home, socketFile))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var held []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c) // accept, never answer
			mu.Unlock()
		}
	}()
	defer func() {
		mu.Lock()
		for _, c := range held {
			_ = c.Close()
		}
		mu.Unlock()
	}()

	for _, o := range []Options{
		{Home: home, Timeout: 200 * time.Millisecond},
		{Home: home, Timeout: 200 * time.Millisecond, Start: true, Self: "/nonexistent/binary"},
		{Home: home}, // default 250ms
	} {
		timeout := o.Timeout
		if timeout == 0 {
			timeout = 250 * time.Millisecond
		}
		begin := time.Now()
		_, err := Do(proto.Request{Op: proto.OpStatus}, o)
		el := time.Since(begin)
		if err == nil {
			t.Fatalf("Do(%+v) err = nil against a mute daemon, want error", o)
		}
		if el < timeout-20*time.Millisecond || el > timeout+150*time.Millisecond {
			t.Fatalf("Do(%+v) returned after %v, want about %v", o, el, timeout)
		}
	}
}

func TestStartWithBadSelfErrors(t *testing.T) {
	home := tempHome(t)
	begin := time.Now()
	_, err := Do(proto.Request{Op: proto.OpStatus}, Options{Home: home, Start: true, Self: "/nonexistent/binary", Timeout: 300 * time.Millisecond})
	if err == nil {
		t.Fatal("Do(bad Self) err = nil, want error")
	}
	if el := time.Since(begin); el > 450*time.Millisecond {
		t.Fatalf("Do(bad Self) took %v, want <= 450ms", el)
	}
}

func TestNormalizePath(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(realDir, "f.go")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	fileLink := filepath.Join(base, "f-link.go")
	if err := os.Symlink(file, fileLink); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(base, "dangling")
	if err := os.Symlink(filepath.Join(base, "gone"), dangling); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, in, want string }{
		{name: "existing file", in: file, want: file},
		{name: "existing file through symlinked dir", in: filepath.Join(link, "f.go"), want: file},
		{name: "symlink to file", in: fileLink, want: file},
		{name: "new file in symlinked dir", in: filepath.Join(link, "new.go"), want: filepath.Join(realDir, "new.go")},
		{name: "new file in new dirs under symlinked dir", in: filepath.Join(link, "a", "b", "new.go"), want: filepath.Join(realDir, "a", "b", "new.go")},
		{name: "dotdot segments", in: link + "/sub/../f.go", want: file},
		{name: "dotdot and dot, not existing", in: link + "/sub/./x/../new.go", want: filepath.Join(realDir, "sub", "new.go")},
		{name: "trailing slash", in: link + "/", want: realDir},
		{name: "dangling symlink", in: dangling, want: dangling},
		{name: "root", in: "/", want: "/"},
		{name: "relative", in: "a/../b/./c.go", want: "b/c.go"},
		{name: "relative dot", in: "./x.go", want: "x.go"},
		{name: "empty", in: "", want: ""},
	} {
		if got := NormalizePath(tc.in); got != tc.want {
			t.Errorf("%s: NormalizePath(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// --- start on demand, against a real spawned process ---

var (
	helperOnce sync.Once
	helperBin  string
	helperErr  error
	helperDir  string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if helperDir != "" {
		_ = os.RemoveAll(helperDir)
	}
	os.Exit(code)
}

// helper builds testdata/helper, a stand-in for `agent-quorum serve`.
func helper(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	helperOnce.Do(func() {
		helperDir, helperErr = os.MkdirTemp("", "aq-helper")
		if helperErr != nil {
			return
		}
		helperBin = filepath.Join(helperDir, "helper")
		out, err := exec.Command("go", "build", "-o", helperBin, "./testdata/helper").CombinedOutput()
		if err != nil {
			helperErr = fmt.Errorf("go build helper: %w\n%s", err, out)
		}
	})
	if helperErr != nil {
		t.Fatal(helperErr)
	}
	return helperBin
}

// stopDaemons shuts down whatever daemon serves home and makes sure the pids
// seen during the test are gone.
func stopDaemons(t *testing.T, home string, pids ...string) {
	t.Helper()
	_, _ = Do(proto.Request{Op: proto.OpShutdown}, Options{Home: home, Timeout: 2 * time.Second})
	for _, s := range pids {
		pid, err := strconv.Atoi(s)
		if err != nil || pid <= 0 {
			continue
		}
		gone := false
		for i := 0; i < 300; i++ {
			if err := syscall.Kill(pid, 0); err != nil {
				gone = true
				break
			}
			// A daemon we spawned stays a zombie until reaped: reap it.
			var ws syscall.WaitStatus
			if wpid, _ := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil); wpid == pid {
				gone = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !gone {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Errorf("daemon pid %d after shutdown = alive, want gone (killed)", pid)
		}
	}
}

func listeningLines(t *testing.T, home string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, logFile))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "listening on")
}

func TestStartOnDemand(t *testing.T) {
	bin := helper(t)
	home := filepath.Join(tempHome(t), "h") // not created yet
	o := Options{Home: home, Timeout: 5 * time.Second, Start: true, Self: bin}

	resp, err := Do(proto.Request{Op: proto.OpNote, Note: "first"}, o)
	t.Cleanup(func() { stopDaemons(t, home, resp.Context) })
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Reason != "first" {
		t.Fatalf("Do(note first) = %+v, want OK with Reason %q", resp, "first")
	}
	if resp.Context == strconv.Itoa(os.Getpid()) {
		t.Fatal("daemon pid = test process pid, want a separate process")
	}
	if fi, err := os.Stat(home); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("Stat(home) = %v, %v, want mode 0700 and nil error", fi, err)
	}

	// Second call reuses it, without Start.
	resp2, err := Do(proto.Request{Op: proto.OpNote}, Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.Context != resp.Context {
		t.Fatalf("second call pid = %s, want first call's %s", resp2.Context, resp.Context)
	}
	if n := listeningLines(t, home); n != 1 {
		t.Fatalf("daemons listening = %d, want 1", n)
	}
}

func TestConcurrentStartOneDaemon(t *testing.T) {
	bin := helper(t)
	home := tempHome(t)
	o := Options{Home: home, Timeout: 5 * time.Second, Start: true, Self: bin}

	const n = 2
	resps := make([]proto.Response, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			resps[i], errs[i] = Do(proto.Request{Op: proto.OpNote}, o)
		})
	}
	wg.Wait()
	t.Cleanup(func() { stopDaemons(t, home, resps[0].Context, resps[1].Context) })

	for i := range n {
		if errs[i] != nil || !resps[i].OK {
			t.Fatalf("Do(note) call %d = %+v, %v, want OK and nil error", i, resps[i], errs[i])
		}
	}
	if resps[0].Context != resps[1].Context {
		t.Fatalf("second call pid = %s, want first call's %s", resps[1].Context, resps[0].Context)
	}
	// The lock is held by exactly that one process...
	err := daemon.Serve(context.Background(), daemon.Options{Home: home, Handler: echoHandler{}})
	if !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Fatalf("in-process Serve() = %v, want ErrAlreadyRunning", err)
	}
	// ...and only one ever got as far as listening. Give a losing process a
	// moment to have written anything it was going to write.
	time.Sleep(100 * time.Millisecond)
	if n := listeningLines(t, home); n != 1 {
		t.Fatalf("daemons listening = %d, want 1", n)
	}
	resp, err := Do(proto.Request{Op: proto.OpNote}, Options{Home: home})
	if err != nil || resp.Context != resps[0].Context {
		t.Fatalf("follow-up Do(note) = %+v, %v, want pid %s and nil error", resp, err, resps[0].Context)
	}
}

func TestStaleSocketRecoversWithStart(t *testing.T) {
	bin := helper(t)
	home := tempHome(t)
	sock := filepath.Join(home, socketFile)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("Lstat(stale socket) err = %v, want nil", err)
	}

	if _, err := Do(proto.Request{Op: proto.OpNote}, Options{Home: home}); err == nil {
		t.Fatal("Do(note) over stale socket err = nil, want error")
	}
	resp, err := Do(proto.Request{Op: proto.OpNote, Note: "x"}, Options{Home: home, Timeout: 5 * time.Second, Start: true, Self: bin})
	t.Cleanup(func() { stopDaemons(t, home, resp.Context) })
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Reason != "x" {
		t.Fatalf("Do(note x) = %+v, want OK with Reason %q", resp, "x")
	}
}
