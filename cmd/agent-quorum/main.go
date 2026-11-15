// Command agent-quorum coordinates concurrent coding-agent sessions on one machine.
//
// One binary, three roles (docs/DESIGN.md section 4):
//
//	agent-quorum hook --agent <name>   short-lived filter run by an agent's hooks
//	agent-quorum serve                 the per-user daemon, started on demand
//	agent-quorum status|release|check|stop|install-git-hook   human and git-hook CLI
package main

import (
	"fmt"
	"io"
	"os"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

const usage = `usage: agent-quorum <command>

  status [--json]                      live sessions and the files each one holds
  release [--session ID] --all|PATH..  drop claims of a session (default $AGENT_QUORUM_SESSION)
  check --staged | PATH...             exit 1 if a path is held by another session (pre-commit)
  install-git-hook                     write .git/hooks/pre-commit running "check --staged"
  stop                                 stop the daemon (claims survive in state.json)
  serve                                run the daemon in the foreground
  hook --agent claude                  hook entry point (reads hook JSON on stdin)
  version
`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "hook":
		return runHook(args[1:], stdin, stdout)
	case "serve":
		return runServe(stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "release":
		return runRelease(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "install-git-hook":
		return runInstallGitHook(stdout, stderr)
	case "stop":
		return runStop(stdout, stderr)
	default:
		fmt.Fprintf(stderr, "agent-quorum: unknown command %q\n%s", args[0], usage)
		return 2
	}
}
