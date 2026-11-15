// Command helper is a stand-in for `agent-quorum serve`, used by the client
// tests for start-on-demand. Its handler answers with its own pid.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/ChristopherLiew/agent-quorum/internal/daemon"
	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

type echo struct{}

// Handle echoes the note as the reason and reports the helper pid as context.
func (echo) Handle(req proto.Request) proto.Response {
	return proto.Response{OK: true, Reason: req.Note, Context: strconv.Itoa(os.Getpid())}
}

func main() {
	if len(os.Args) != 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: helper serve")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	err := daemon.Serve(ctx, daemon.Options{
		Home:    os.Getenv("AGENT_QUORUM_HOME"),
		Handler: echo{},
		Logf:    func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) },
	})
	if err != nil && !errors.Is(err, daemon.ErrAlreadyRunning) {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
}
