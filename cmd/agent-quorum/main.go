// Command agent-quorum coordinates concurrent coding-agent sessions on one machine.
//
// This file is scaffold only: it wires `version` and a fail-open `hook` stub so
// that CI, the release pipeline and the plugin work before any logic exists.
// Replace run() as you implement the milestones in docs/DESIGN.md.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: agent-quorum <version|hook|serve|status>")
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, version)
		return 0
	case "hook":
		// Fail-open stub: consume the payload, decide nothing, exit 0.
		// An exit 0 with empty stdout means "no opinion" to every supported agent.
		payload, _ := io.ReadAll(stdin)
		capture(payload)
		return 0
	default:
		fmt.Fprintf(stderr, "agent-quorum %s: not implemented yet\n", args[0])
		return 1
	}
}

// capture appends raw hook payloads to $AGENT_QUORUM_CAPTURE, one JSON object per
// line. Use it in M0 to record real fixtures from your own agent versions.
func capture(payload []byte) {
	path := os.Getenv("AGENT_QUORUM_CAPTURE")
	if path == "" || len(payload) == 0 {
		return
	}
	rec := struct {
		TS      string          `json:"ts"`
		PPID    int             `json:"ppid"`
		Args    []string        `json:"args"`
		Payload json.RawMessage `json:"payload"`
	}{time.Now().Format(time.RFC3339), os.Getppid(), os.Args[1:], payload}
	line, err := json.Marshal(rec) // fails if the payload is not valid JSON
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}
