// Package core holds sessions, claims and every coordination decision.
// It is pure: no I/O and no time.Now(); everything impure comes through Env.
package core

import (
	"time"

	"github.com/ChristopherLiew/agent-quorum/internal/guard"
)

// Env is everything the core needs from the outside world.
type Env interface {
	// Now returns the current time.
	Now() time.Time
	// Alive reports whether a process with this pid exists.
	Alive(pid int) bool
	// Dirty reports whether path has uncommitted changes (modified, staged or
	// untracked) in repo. known=false: not a git repo, or git failed.
	Dirty(repo, path string) (dirty, known bool)
	// RepoRoot returns the git toplevel containing dir, or "" if none.
	RepoRoot(dir string) string
}

// Config tunes the claim lifecycle. Zero values mean the defaults.
type Config struct {
	// Grace is how long a clean file stays claimed after the last touch.
	// The default is 120s.
	Grace time.Duration
	// IdleTTL is the liveness window when a session's pid is unknown.
	// The default is 30m.
	IdleTTL time.Duration
	// Classify rates a shell command. The default is guard.Classify; tests
	// inject their own.
	Classify func(command string) guard.Risk
}

// SessionID identifies a session as "<agent>:<id>".
type SessionID string

// Session is one registered agent session.
type Session struct {
	ID        SessionID `json:"id"`
	Agent     string    `json:"agent"`
	PID       int       `json:"pid"`
	CWD       string    `json:"cwd"`
	Repo      string    `json:"repo"`
	Note      string    `json:"note"`
	StartedAt time.Time `json:"started_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// Claim records which session holds a file and when it last touched it.
type Claim struct {
	Path       string    `json:"path"`
	Owner      SessionID `json:"owner"`
	Repo       string    `json:"repo"`
	AcquiredAt time.Time `json:"acquired_at"`
	TouchedAt  time.Time `json:"touched_at"`
}
