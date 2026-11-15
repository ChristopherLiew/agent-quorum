// Package gitx holds thin wrappers over the git CLI. Every call is bounded by
// a short timeout, never takes optional locks and never reads stdin.
package gitx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const timeout = 2 * time.Second

// run executes git with args and returns its stdout.
func run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdin = nil // /dev/null
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd.Output()
}

// RepoRoot returns the git toplevel containing dir, symlink-resolved, or ""
// if there is none or git fails. A dir that does not exist yet is replaced by
// its nearest existing ancestor.
func RepoRoot(dir string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	for {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	out, err := run("-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	root := strings.TrimRight(string(out), "\r\n")
	if root == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root
}

// Dirty reports whether path has uncommitted changes (modified, staged or
// untracked) in repo. known is false when repo is empty or git fails.
func Dirty(repo, path string) (dirty, known bool) {
	if repo == "" {
		return false, false
	}
	out, err := run("-C", repo, "status", "--porcelain", "--", path)
	if err != nil {
		return false, false
	}
	return len(bytes.TrimSpace(out)) > 0, true
}
