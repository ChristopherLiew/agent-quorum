package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v err = %v, output %s, want nil", args, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	write(t, filepath.Join(dir, "b.txt"), "one\n")
	write(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestRepoRoot(t *testing.T) {
	repo := newRepo(t)
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"root":        repo,
		"subdir":      sub,
		"missing dir": filepath.Join(repo, "not", "yet", "there"),
		"via symlink": link,
	} {
		if got := RepoRoot(dir); got != repo {
			t.Errorf("%s: RepoRoot(%q) = %q, want %q", name, dir, got, repo)
		}
	}
	if got := RepoRoot(t.TempDir()); got != "" {
		t.Errorf("RepoRoot(non-repo) = %q, want empty", got)
	}
	if got := RepoRoot(""); got != "" {
		t.Errorf("RepoRoot(\"\") = %q, want empty", got)
	}
}

func TestDirty(t *testing.T) {
	repo := newRepo(t)
	a := filepath.Join(repo, "a.txt")
	b := filepath.Join(repo, "b.txt")
	untracked := filepath.Join(repo, "new.txt")
	ignored := filepath.Join(repo, "x.log")

	check := func(name, path string, wantDirty, wantKnown bool) {
		t.Helper()
		dirty, known := Dirty(repo, path)
		if dirty != wantDirty || known != wantKnown {
			t.Errorf("%s: Dirty(%q) = (%v, %v), want (%v, %v)", name, path, dirty, known, wantDirty, wantKnown)
		}
	}

	check("clean", a, false, true)

	write(t, a, "two\n")
	check("modified", a, true, true)
	check("other file still clean", b, false, true)

	write(t, b, "two\n")
	git(t, repo, "add", "b.txt")
	check("staged", b, true, true)

	write(t, untracked, "x\n")
	check("untracked", untracked, true, true)

	write(t, ignored, "x\n")
	check("ignored", ignored, false, true)

	check("not created yet", filepath.Join(repo, "nope.txt"), false, true)

	git(t, repo, "commit", "-q", "-am", "second")
	check("clean after commit", a, false, true)

	nonRepo := t.TempDir()
	if dirty, known := Dirty(nonRepo, filepath.Join(nonRepo, "f")); dirty || known {
		t.Errorf("Dirty(non-repo) = (%v, %v), want (false, false)", dirty, known)
	}
	if dirty, known := Dirty("", a); dirty || known {
		t.Errorf("Dirty(\"\", a) = (%v, %v), want (false, false)", dirty, known)
	}
	if dirty, known := Dirty(filepath.Join(nonRepo, "missing"), a); dirty || known {
		t.Errorf("Dirty(missing dir, a) = (%v, %v), want (false, false)", dirty, known)
	}
}
