package daemon

import (
	"errors"
	"syscall"
	"time"

	"github.com/ChristopherLiew/agent-quorum/internal/core"
	"github.com/ChristopherLiew/agent-quorum/internal/gitx"
)

// RealEnv is the production core.Env: wall clock, kill(pid, 0) and git.
type RealEnv struct{}

var _ core.Env = RealEnv{}

// Now returns the wall-clock time.
func (RealEnv) Now() time.Time { return time.Now() }

// Alive reports whether a process with this pid exists. EPERM means it exists
// but belongs to someone else.
func (RealEnv) Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Dirty reports whether path has uncommitted changes in repo, and whether
// git could tell.
func (RealEnv) Dirty(repo, path string) (dirty, known bool) { return gitx.Dirty(repo, path) }

// RepoRoot returns the git work tree root containing dir, or "" if there is none.
func (RealEnv) RepoRoot(dir string) string { return gitx.RepoRoot(dir) }
