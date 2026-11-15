package guard

import (
	"math/rand"
	"strings"
	"testing"
)

func (r Risk) String() string {
	switch r {
	case None:
		return "None"
	case RewritesTree:
		return "RewritesTree"
	case SweepsIndex:
		return "SweepsIndex"
	}
	return "Risk(?)"
}

func TestClassify(t *testing.T) {
	const (
		N = None
		R = RewritesTree
		S = SweepsIndex
	)
	tests := []struct {
		cmd  string
		want Risk
	}{
		// Required by the design.
		{cmd: `cd x && git stash`, want: R},
		{cmd: `FOO=1 git -C /r reset --hard`, want: R},
		{cmd: `git commit -m "add -A to docs"`, want: N},
		{cmd: `echo git stash`, want: N},
		{cmd: `git checkout -b feature`, want: N},
		{cmd: `git switch -c feature`, want: N},
		{cmd: `git reset HEAD~1`, want: N},
		{cmd: `git reset --soft HEAD~1`, want: N},
		{cmd: `git stash list`, want: N},
		{cmd: `git add file.go other.go`, want: N},
		{cmd: `git add .`, want: S},
		{cmd: `git add -A && git commit -m x`, want: S},
		{cmd: `git status; git diff | head`, want: N},
		{cmd: `git log --oneline -5`, want: N},
		{cmd: `git restore --staged a.go`, want: N},
		{cmd: `git restore a.go`, want: R},
		{cmd: `git commit -am "msg"`, want: S},
		{cmd: `/usr/bin/git pull --rebase`, want: R},
		{cmd: `git --no-pager diff`, want: N},
		{cmd: `(cd sub && git clean -fd)`, want: R},
		{cmd: `x=$(git stash)`, want: R},
		{cmd: `git push`, want: N},
		{cmd: `git fetch`, want: N},
		{cmd: `git clean -n`, want: N},

		// Empty and separator-only input.
		{cmd: ``, want: N},
		{cmd: `   `, want: N},
		{cmd: `;;;`, want: N},
		{cmd: `&& || | &`, want: N},
		{cmd: `git`, want: N},
		{cmd: `git -C`, want: N},
		{cmd: `git -C /r`, want: N},

		// Command word, not text.
		{cmd: `echo "git stash"`, want: N},
		{cmd: `echo git stash && git status`, want: N},
		{cmd: `grep -r "git reset --hard" .`, want: N},
		{cmd: `gitk stash`, want: N},
		{cmd: `mygit stash`, want: N},
		{cmd: `git-stash`, want: N},
		{cmd: `GIT=1 echo git stash`, want: N},
		{cmd: `./git stash`, want: R},
		{cmd: `"git" stash`, want: R},
		{cmd: `git "stash"`, want: R},
		{cmd: `\git stash`, want: R},
		{cmd: `$(which git) stash`, want: N},
		{cmd: `git log --grep="git stash"`, want: N},

		// Quotes hide separators and flags.
		{cmd: `git commit -m "x; git stash"`, want: N},
		{cmd: `git commit -m 'x && git reset --hard'`, want: N},
		{cmd: `git commit -m "it's" -a`, want: S},
		{cmd: `git commit -m "say \"hi\"; git stash"`, want: N},
		{cmd: `echo '$(git stash)'`, want: N},

		// Substitutions and groups.
		{cmd: `echo "$(git stash)"`, want: R},
		{cmd: `echo "$(git add -A)"`, want: S},
		{cmd: "echo `git stash`", want: R},
		{cmd: "x=`git status`", want: N},
		{cmd: `x=$(git status)`, want: N},
		{cmd: `git commit -m "$(git log -1 --format=%s)"`, want: N},
		{cmd: `cat <(git stash show -p)`, want: N},
		{cmd: `diff <(git checkout main) x`, want: R},
		{cmd: `{ git add -A; }`, want: S},
		{cmd: `if git diff --quiet; then git stash; fi`, want: R},
		{cmd: `! git stash`, want: R},
		{cmd: `echo $((1+2)) && git status`, want: N},

		// Wrappers and assignments.
		{cmd: `GIT_DIR=/r/.git git pull`, want: R},
		{cmd: `A=1 B=2 git add -u`, want: S},
		{cmd: `sudo git stash`, want: R},
		{cmd: `sudo -u bob git stash`, want: R},
		{cmd: `env FOO=1 git stash`, want: R},
		{cmd: `env -i git add .`, want: S},
		{cmd: `command git stash`, want: R},
		{cmd: `exec git pull`, want: R},
		{cmd: `time git merge main`, want: R},
		{cmd: `nohup git rebase main &`, want: R},
		{cmd: `sudo env FOO=1 git status`, want: N},

		// Shells, eval, xargs.
		{cmd: `bash -c "git stash"`, want: R},
		{cmd: `sh -c 'git add -A'`, want: S},
		{cmd: `bash -lc 'cd x && git reset --hard'`, want: R},
		{cmd: `bash -c "echo git stash"`, want: N},
		{cmd: `bash script.sh -c "git stash"`, want: N},
		{cmd: `eval "git stash"`, want: R},
		{cmd: `xargs git add`, want: N},
		{cmd: `git ls-files -m | xargs git checkout --`, want: R},
		{cmd: `find . -name x -exec git stash \;`, want: N}, // documented miss

		// Global options.
		{cmd: `git -c core.editor=true -C /r --no-pager stash`, want: R},
		{cmd: `git --git-dir=/r/.git --work-tree /r checkout main`, want: R},
		{cmd: `git --git-dir /r/.git status`, want: N},
		{cmd: `git -C stash status`, want: N},
		{cmd: `git -c alias.x=stash log`, want: N},
		{cmd: `git -p --no-optional-locks --bare --exec-path=/x --namespace=n merge x`, want: R},
		{cmd: `git --namespace n pull`, want: R},
		{cmd: `git --paginate log`, want: N},

		// checkout / switch / restore / reset.
		{cmd: `git checkout main`, want: R},
		{cmd: `git checkout -- a.go`, want: R},
		{cmd: `git checkout -B f origin/f`, want: N},
		{cmd: `git checkout --orphan x`, want: N},
		{cmd: `git checkout -q -b x`, want: N},
		{cmd: `git switch main`, want: R},
		{cmd: `git switch -`, want: R},
		{cmd: `git switch --create f`, want: N},
		{cmd: `git switch -C f`, want: N},
		{cmd: `git switch --orphan f`, want: N},
		{cmd: `git restore -S a.go`, want: N},
		{cmd: `git restore --staged --worktree a.go`, want: R},
		{cmd: `git restore -SW a.go`, want: R},
		{cmd: `git restore --source=HEAD~1 a.go`, want: R},
		{cmd: `git reset`, want: N},
		{cmd: `git reset --mixed HEAD~1`, want: N},
		{cmd: `git reset -- a.go`, want: N},
		{cmd: `git reset --hard`, want: R},
		{cmd: `git reset --keep ORIG_HEAD`, want: R},
		{cmd: `git reset --merge`, want: R},

		// stash / clean / history rewriters.
		{cmd: `git stash`, want: R},
		{cmd: `git stash pop`, want: R},
		{cmd: `git stash push -m "wip"`, want: R},
		{cmd: `git stash -u`, want: R},
		{cmd: `git stash drop`, want: R},
		{cmd: `git stash apply`, want: R},
		{cmd: `git stash clear`, want: R},
		{cmd: `git stash show -p`, want: N},
		{cmd: `git clean -fd`, want: R},
		{cmd: `git clean -nfd`, want: N},
		{cmd: `git clean --dry-run -fd`, want: N},
		{cmd: `git rebase -i HEAD~3`, want: R},
		{cmd: `git merge --no-ff x`, want: R},
		{cmd: `git cherry-pick abc123`, want: R},
		{cmd: `git revert HEAD`, want: R},
		{cmd: `git am < patch.mbox`, want: R},
		{cmd: `git worktree add ../x`, want: N},
		{cmd: `git push --force`, want: N},
		{cmd: `git branch -D x`, want: N},

		// add.
		{cmd: `git add -A`, want: S},
		{cmd: `git add --all`, want: S},
		{cmd: `git add -Av`, want: S},
		{cmd: `git add -u`, want: S},
		{cmd: `git add --update`, want: S},
		{cmd: `git add -p`, want: N},
		{cmd: `git add -- .`, want: S},
		{cmd: `git add ./`, want: S},
		{cmd: `git add '*'`, want: S},
		{cmd: `git add *`, want: S},
		{cmd: `git add :/`, want: S},
		{cmd: `git add ./src`, want: N},
		{cmd: `git add -- -A`, want: N},
		{cmd: `git add -N file`, want: N},
		{cmd: `git add . >/dev/null 2>&1`, want: S},

		// commit.
		{cmd: `git commit`, want: N},
		{cmd: `git commit -a`, want: S},
		{cmd: `git commit --all -m x`, want: S},
		{cmd: `git commit -avm "x"`, want: S},
		{cmd: `git commit -m -a`, want: N},
		{cmd: `git commit -ma`, want: N},
		{cmd: `git commit --message "use -a"`, want: N},
		{cmd: `git commit --message=-a`, want: N},
		{cmd: `git commit --message -a`, want: N},
		{cmd: `git commit --author "a b" -a`, want: S},
		{cmd: `git commit --author -a -m x`, want: N},
		{cmd: `git commit --amend`, want: N},
		{cmd: `git commit --amend --no-edit -a`, want: S},
		{cmd: `git commit -F msg.txt`, want: N},
		{cmd: `git commit -F -a`, want: N},
		{cmd: `git commit --fixup abc123`, want: N},
		{cmd: `git commit -C HEAD`, want: N},
		{cmd: `git commit -S -m x`, want: N},
		{cmd: `git commit -uall -m x`, want: N},
		{cmd: `git commit -- a.go`, want: N},
		{cmd: `git commit -m x a.go`, want: N},

		// Redirections and comments.
		{cmd: `git status 2>&1 | head`, want: N},
		{cmd: `git stash 2>/dev/null`, want: R},
		{cmd: `git stash &>/dev/null`, want: R},
		{cmd: `git status # git stash`, want: N},
		{cmd: `# git stash`, want: N},
		{cmd: `cat <<< "git stash"`, want: N},

		// Worst risk wins, in either order.
		{cmd: `git stash; git add -A`, want: R},
		{cmd: `git add -A; git stash`, want: R},
		{cmd: `git status || git add . || git status`, want: S},

		// Multi-line and heredocs.
		{cmd: "git status\ngit stash\n", want: R},
		{cmd: "git add -A &&\ngit commit -m x", want: S},
		{cmd: "git \\\n  stash", want: R},
		{cmd: "git commit \\\n  -m 'x' \\\n  a.go", want: N},
		{cmd: "cat <<EOF > notes.txt\ngit stash\nEOF\ngit status", want: N},
		{cmd: "cat <<EOF\ngit stash\nEOF\ngit checkout main", want: R},
		{cmd: "cat <<-'END'\n\tgit add -A\n\tEND\ngit add a.go", want: N},
		{cmd: "git commit -m \"$(cat <<'EOF'\nfix: don't run git add -A\n\ngit stash is bad; git reset --hard too (really)\nEOF\n)\"", want: N},
		{cmd: "git commit -a -m \"$(cat <<'EOF'\nmsg\nEOF\n)\"", want: S},
		{cmd: "cat <<EOF\nnever closed\ngit stash", want: N},
	}
	if len(tests) < 60 {
		t.Fatalf("len(tests) = %d, want >= 60", len(tests))
	}
	for _, tc := range tests {
		if got := Classify(tc.cmd); got != tc.want {
			t.Errorf("Classify(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

var oddInputs = []string{
	"", " ", "\n", "\x00", "'", `"`, "`", `\`, "$", "$(", "${", "$((", "(", ")", "{", "}",
	"git '", `git "`, "git `", `git \`, "git $(", "git ${", "git stash '", `git commit -m "`,
	"git commit -m", "git commit -", "git add --", "git -c", "git --git-dir", "git stash <<",
	"git stash <<EOF", "git <<-", "git <", "git >", "git >>", "git &>", "git 2>&", "<<<",
	"sudo", "env", "env -u", "sudo -u", "bash -c", "eval", "xargs", "xargs git", "FOO=",
	"=", "=x git stash", "\"$(", "\"`", "`\\", "$'", "$\"", "#", "a#", "git stash\\",
	"git\tstash\r\n", "gït stäsh", "git \xff\xfe stash", "((((((((", "))))))))",
	strings.Repeat("(", 200000), strings.Repeat("$(", 200000), strings.Repeat("`", 100001),
	strings.Repeat("bash -c '", 500), strings.Repeat(`eval "`, 500),
	strings.Repeat("\"$(", 100000), strings.Repeat("git stash;", 10000),
}

func TestClassifyNeverPanics(t *testing.T) {
	check := func(s string) {
		t.Helper()
		if r := Classify(s); r != None && r != RewritesTree && r != SweepsIndex {
			t.Errorf("Classify(%q) = %d, want None, RewritesTree or SweepsIndex", s, int(r))
		}
	}
	for _, s := range oddInputs {
		check(s)
	}
	// Deterministic random soup of shell metacharacters and git words.
	pieces := []string{
		"git", "stash", "add", "commit", "-a", "-m", "-A", ".", "--", "reset", "--hard",
		" ", " ", "\n", "\t", "'", `"`, "`", `\`, "$", "(", ")", "{", "}", ";", "&", "|",
		"<", ">", "<<", "EOF", "#", "=", "-", "bash", "-c", "eval", "sudo", "env", "x", "1",
	}
	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 20000; n++ {
		var b strings.Builder
		for k := rng.Intn(24); k > 0; k-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		check(b.String())
	}
}

func FuzzClassify(f *testing.F) {
	for _, s := range oddInputs {
		if len(s) < 1000 {
			f.Add(s)
		}
	}
	f.Add(`cd x && git stash`)
	f.Add("git commit -m \"$(cat <<'EOF'\nx\nEOF\n)\"")
	f.Fuzz(func(_ *testing.T, s string) {
		Classify(s)
	})
}
