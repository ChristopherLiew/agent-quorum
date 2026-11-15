// Package guard classifies shell commands by the damage their git verbs can
// do to other sessions' uncommitted work. Pure. See docs/DESIGN.md M5.
//
// Deliberate decisions (tune against real use; every deny is logged):
//
//   - The command WORD is checked, not the text. `echo git stash`,
//     `grep -r "git reset --hard" .` and `gitk stash` are None; only a simple
//     command whose command word is `git` (or a path ending in `/git`) counts.
//   - Quoted text is data and is never scanned for commands or flags as text:
//     `git commit -m "add -A to docs"` and `git commit -m "x; git stash"` are
//     None. The one exception is what the shell itself executes inside double
//     quotes: `$( ... )` and backticks are scanned (`echo "$(git stash)"` is
//     RewritesTree). Inside single quotes nothing is scanned.
//   - Heredoc bodies (<<EOF, <<-EOF, <<'EOF') are data and are skipped, so
//     `git commit -m "$(cat <<'EOF' ... EOF)"` is judged on its flags only.
//     `bash <<EOF` + git lines is therefore missed. Here-strings (<<<) and
//     redirection targets are dropped; comments (# ...) are ignored.
//   - Leading VAR=value words, the wrappers sudo, env, command, exec, time,
//     nohup (with their options), and the shell words if, then, elif, else,
//     do, while, until, !, { and } are skipped before the command word.
//     Subshells `( ... )`, `$( ... )`, `<( ... )` and backticks are scanned.
//   - `bash -c "git stash"` (also sh, zsh, dash, ksh, fish with a -c cluster
//     such as -lc) and `eval ...` are re-parsed and classified by content.
//     `xargs ... git <verb>` is classified from the first `git` word on (the
//     pathspecs come from stdin, so `xargs git add` is None, but
//     `xargs git checkout --` is RewritesTree).
//   - Not seen, by choice (None): git reached through anything else
//     (`find -exec git ...`, `timeout 5 git ...`, scripts, make, aliases and
//     shell functions), a command word that is an expansion (`$GIT stash`,
//     `$(which git) stash`), git aliases (`git st`), and abbreviated long
//     options (`--dry`). The guard is a seat belt against accidents by a
//     cooperating agent, not a sandbox.
//   - Verb table: checkout (unless -b/-B/--orphan), switch (unless
//     -c/-C/--create/--force-create/--orphan), restore (unless it only
//     unstages: --staged/-S without --worktree/-W), reset with
//     --hard/--merge/--keep, stash (unless the subcommand is list or show;
//     bare `git stash` and `git stash -u` are risky), clean (unless -n or
//     --dry-run), rebase, merge, pull, cherry-pick, revert, am are
//     RewritesTree, including their --abort/--continue forms. `add` with
//     -A/--all/-u/--update or a pathspec that is exactly `.`, `./`, `*` or
//     `:/`, and `commit` with -a/--all (also in a cluster such as -am) are
//     SweepsIndex. Values of commit options (-m, -F, -C, -c, -t, --message,
//     --author, --fixup, ...) are not flags: `git commit -m -a` is None.
//     worktree, push, fetch, branch and everything else are None.
//   - Worst risk wins over the whole command: RewritesTree > SweepsIndex >
//     None (note this is not the numeric order of the constants).
//   - Classify never panics; nesting deeper than maxDepth is not followed.
package guard

import (
	"strings"

	"github.com/bytedance/gg/gslice"
)

// Risk is how much damage a shell command can do to other sessions' work.
type Risk int

// The Risk levels, from harmless to most destructive.
const (
	None         Risk = iota // the command is harmless to other sessions
	RewritesTree             // checkout, stash, reset --hard, ...: can destroy uncommitted work
	SweepsIndex              // add -A, commit -a, ...: can stage or commit another session's edits
)

// maxDepth bounds recursion into groups, substitutions and `bash -c` strings.
const maxDepth = 40

// Classify returns the worst risk among the git invocations in command.
func Classify(command string) Risk {
	return classify(command, 0)
}

func classify(s string, depth int) Risk {
	p := parser{s: s}
	p.list(false, depth)
	return p.worst
}

// worse returns the more severe of two risks.
func worse(a, b Risk) Risk {
	if a == RewritesTree || b == RewritesTree {
		return RewritesTree
	}
	if a == SweepsIndex || b == SweepsIndex {
		return SweepsIndex
	}
	return None
}

// ---- shell splitting ----

type heredoc struct {
	delim     string
	stripTabs bool
}

type parser struct {
	s        string
	i        int
	worst    Risk
	heredocs []heredoc // announced on the current line, bodies start after the next newline
}

const (
	redirNone     = iota
	redirDrop     // next word is a redirection target
	redirHere     // next word is a heredoc delimiter
	redirHereTabs // same, for <<-
)

// placeholder stands in a word for the output of a command substitution.
const placeholder = "$()"

// list parses simple commands from p.i until end of input or, when sub is
// set, the closing parenthesis of the group it was called for.
func (p *parser) list(sub bool, depth int) {
	var (
		words  []string
		word   []byte
		inWord bool
		redir  = redirNone
	)
	endWord := func() {
		if !inWord {
			return
		}
		w := string(word)
		word, inWord = word[:0], false
		switch redir {
		case redirNone:
			words = append(words, w)
		case redirHere, redirHereTabs:
			p.heredocs = append(p.heredocs, heredoc{w, redir == redirHereTabs})
		}
		redir = redirNone
	}
	endCmd := func() {
		endWord()
		redir = redirNone
		if len(words) > 0 {
			p.worst = worse(p.worst, classifyWords(words, depth))
			words = words[:0]
		}
	}
	peek := func() byte {
		if p.i < len(p.s) {
			return p.s[p.i]
		}
		return 0
	}

	for p.i < len(p.s) {
		c := p.s[p.i]
		switch c {
		case ' ', '\t', '\r':
			endWord()
			p.i++
		case '\n':
			endCmd()
			p.i++
			p.skipHeredocs()
		case ';', '|':
			endCmd()
			p.i++
		case '&':
			p.i++
			if peek() == '>' { // &>file: a redirection, not a separator
				endWord()
				continue
			}
			endCmd()
		case '(':
			endCmd()
			p.i++
			if depth < maxDepth {
				p.list(true, depth+1)
			}
		case ')':
			endCmd()
			p.i++
			if sub {
				return
			}
		case '<', '>':
			if inWord && allDigits(word) { // fd number, as in 2>file
				word, inWord = word[:0], false
			} else {
				endWord()
			}
			p.i++
			kind := redirDrop
			switch {
			case c == '<' && peek() == '<':
				p.i++
				if peek() == '<' { // here-string: its word is dropped
					p.i++
				} else {
					kind = redirHere
					if peek() == '-' {
						p.i++
						kind = redirHereTabs
					}
				}
			case c == '>' && peek() == '>':
				p.i++
			case peek() == '(': // process substitution: the group is scanned
				kind = redirNone
			}
			if kind == redirDrop && (peek() == '&' || peek() == '|') { // >&2, <&3, >|
				p.i++
			}
			redir = kind
		case '#':
			if inWord {
				word = append(word, c)
				p.i++
				continue
			}
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		case '\\':
			p.i++
			if p.i >= len(p.s) {
				continue
			}
			if p.s[p.i] != '\n' { // backslash-newline is a line continuation
				word = append(word, p.s[p.i])
				inWord = true
			}
			p.i++
		case '\'':
			inWord = true
			rest := p.s[p.i+1:]
			j := strings.IndexByte(rest, '\'')
			if j < 0 {
				word = append(word, rest...)
				p.i = len(p.s)
			} else {
				word = append(word, rest[:j]...)
				p.i += j + 2
			}
		case '"':
			inWord = true
			p.i++
			for p.i < len(p.s) && p.s[p.i] != '"' {
				d := p.s[p.i]
				switch {
				case d == '\\' && p.i+1 < len(p.s):
					n := p.s[p.i+1]
					switch n {
					case '\n':
					case '$', '`', '"', '\\':
						word = append(word, n)
					default:
						word = append(word, d, n)
					}
					p.i += 2
				case d == '$' && p.i+1 < len(p.s) && p.s[p.i+1] == '(' && depth < maxDepth:
					p.i += 2
					p.list(true, depth+1)
					word = append(word, placeholder...)
				case d == '`':
					p.backtick(depth)
					word = append(word, placeholder...)
				default:
					word = append(word, d)
					p.i++
				}
			}
			if p.i < len(p.s) {
				p.i++ // closing quote
			}
		case '$':
			inWord = true
			p.i++
			switch n := peek(); {
			case n == '(' && depth < maxDepth:
				p.i++
				p.list(true, depth+1)
				word = append(word, placeholder...)
			case n == '{':
				rest := p.s[p.i:]
				j := strings.IndexByte(rest, '}')
				if j < 0 {
					j = len(rest) - 1
				}
				word = append(word, '$')
				word = append(word, rest[:j+1]...)
				p.i += j + 1
			case n == '\'' || n == '"': // $'...' and $"...": drop the dollar
			default:
				word = append(word, '$')
			}
		case '`':
			inWord = true
			p.backtick(depth)
			word = append(word, placeholder...)
		default:
			word = append(word, c)
			inWord = true
			p.i++
		}
	}
	endCmd()
}

// backtick consumes a `...` substitution starting at p.i and classifies it.
func (p *parser) backtick(depth int) {
	var inner []byte
	j := p.i + 1
	for j < len(p.s) && p.s[j] != '`' {
		if p.s[j] == '\\' && j+1 < len(p.s) {
			if n := p.s[j+1]; n == '`' || n == '\\' || n == '$' {
				inner = append(inner, n)
			} else {
				inner = append(inner, '\\', n)
			}
			j += 2
			continue
		}
		inner = append(inner, p.s[j])
		j++
	}
	if j < len(p.s) {
		j++ // closing backtick
	}
	p.i = j
	if depth < maxDepth {
		p.worst = worse(p.worst, classify(string(inner), depth+1))
	}
}

// skipHeredocs is called just after a newline and skips the bodies of the
// heredocs announced on the line before it.
func (p *parser) skipHeredocs() {
	for _, h := range p.heredocs {
		for p.i < len(p.s) {
			line := p.s[p.i:]
			if j := strings.IndexByte(line, '\n'); j >= 0 {
				line = line[:j]
				p.i += j + 1
			} else {
				p.i = len(p.s)
			}
			line = strings.TrimSuffix(line, "\r")
			if h.stripTabs {
				line = strings.TrimLeft(line, "\t")
			}
			if line == h.delim {
				break
			}
		}
	}
	p.heredocs = p.heredocs[:0]
}

func allDigits(b []byte) bool {
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(b) > 0
}

// ---- simple commands ----

var shellKeywords = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "do": true,
	"while": true, "until": true, "!": true, "{": true, "}": true,
}

// wrappers maps a wrapper command to its short options that take a value.
var wrappers = map[string]string{
	"sudo":    "ughpCDRTUrt",
	"env":     "uCS",
	"command": "",
	"exec":    "a",
	"time":    "fo",
	"nohup":   "",
}

// wrapperLongValue lists wrapper long options that take a separate value.
var wrapperLongValue = map[string]bool{
	"--user": true, "--group": true, "--host": true, "--prompt": true,
	"--chdir": true, "--unset": true, "--other-user": true, "--role": true,
	"--type": true, "--close-from": true, "--command-timeout": true,
}

var shells = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
}

func base(w string) string {
	return w[strings.LastIndexByte(w, '/')+1:]
}

func isGit(w string) bool {
	return w == "git" || strings.HasSuffix(w, "/git")
}

// isAssignment reports whether w looks like NAME=value or NAME+=value.
func isAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	name := strings.TrimSuffix(w[:eq], "+")
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		letter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// classifyWords classifies one simple command given as unquoted words.
func classifyWords(w []string, depth int) Risk {
	i := 0
	for i < len(w) {
		x := w[i]
		if isAssignment(x) || shellKeywords[x] {
			i++
			continue
		}
		valueOpts, ok := wrappers[base(x)]
		if !ok {
			break
		}
		i++
		for i < len(w) && strings.HasPrefix(w[i], "-") {
			opt := w[i]
			i++
			if opt == "--" {
				break
			}
			if wrapperLongValue[opt] || (len(opt) == 2 && strings.IndexByte(valueOpts, opt[1]) >= 0) {
				i++
			}
		}
	}
	if i >= len(w) {
		return None
	}
	cmd, args := w[i], w[i+1:]
	switch b := base(cmd); {
	case isGit(cmd):
		return classifyGit(args)
	case depth >= maxDepth:
		return None
	case shells[b]:
		for j := 0; j < len(args); j++ {
			a := args[j]
			if a == "-o" || a == "+o" || a == "--rcfile" || a == "--init-file" {
				j++
				continue
			}
			if len(a) < 2 || (a[0] != '-' && a[0] != '+') {
				return None // script name: its content is unknown
			}
			if a[0] == '-' && a[1] != '-' && strings.IndexByte(a, 'c') > 0 {
				if j+1 < len(args) {
					return classify(args[j+1], depth+1)
				}
				return None
			}
		}
	case b == "eval":
		return classify(strings.Join(args, " "), depth+1)
	case b == "xargs":
		for j, a := range args {
			if isGit(a) {
				return classifyGit(args[j+1:])
			}
		}
	}
	return None
}

// ---- git ----

// gitGlobalValue lists git global options that take a separate value.
var gitGlobalValue = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--super-prefix": true, "--config-env": true,
	"--attr-source": true,
}

// classifyGit classifies the words following the `git` command word.
func classifyGit(a []string) Risk {
	i := 0
	for i < len(a) && strings.HasPrefix(a[i], "-") {
		if gitGlobalValue[a[i]] {
			i++
		}
		i++
	}
	if i >= len(a) {
		return None
	}
	verb, args := a[i], a[i+1:]
	fl := flags(args)
	switch verb {
	case "checkout":
		if hasFlag(fl, "-b", "-B", "--orphan") {
			return None
		}
		return RewritesTree
	case "switch":
		if hasFlag(fl, "-c", "-C", "--create", "--force-create", "--orphan") {
			return None
		}
		return RewritesTree
	case "restore":
		staged := hasFlag(fl, "--staged") || hasShort(fl, 'S')
		worktree := hasFlag(fl, "--worktree") || hasShort(fl, 'W')
		if staged && !worktree {
			return None
		}
		return RewritesTree
	case "reset":
		if hasFlag(fl, "--hard", "--merge", "--keep") {
			return RewritesTree
		}
		return None
	case "stash":
		if len(args) > 0 && (args[0] == "list" || args[0] == "show") {
			return None
		}
		return RewritesTree
	case "clean":
		if hasFlag(fl, "--dry-run") || hasShort(fl, 'n') {
			return None
		}
		return RewritesTree
	case "rebase", "merge", "pull", "cherry-pick", "revert", "am":
		return RewritesTree
	case "add":
		return classifyAdd(args)
	case "commit":
		return classifyCommit(args)
	}
	return None
}

// flags returns the option words of args that come before a `--`.
func flags(args []string) []string {
	var out []string
	for _, a := range args {
		if a == "--" {
			break
		}
		if len(a) > 1 && a[0] == '-' {
			out = append(out, a)
		}
	}
	return out
}

// hasFlag reports whether any flag equals one of names (or is name=value).
func hasFlag(fl []string, names ...string) bool {
	return gslice.Any(fl, func(f string) bool {
		return gslice.Any(names, func(n string) bool {
			return f == n || (strings.HasPrefix(n, "--") && strings.HasPrefix(f, n+"="))
		})
	})
}

// hasShort reports whether any short-flag cluster (-x, -xyz) contains c.
func hasShort(fl []string, c byte) bool {
	return gslice.Any(fl, func(f string) bool {
		return !strings.HasPrefix(f, "--") && strings.IndexByte(f, c) > 0
	})
}

func sweepingPathspec(a string) bool {
	return a == "." || a == "./" || a == "*" || a == ":/"
}

func classifyAdd(args []string) Risk {
	pathsOnly := false
	for _, a := range args {
		switch {
		case pathsOnly || len(a) < 2 || a[0] != '-':
			if sweepingPathspec(a) {
				return SweepsIndex
			}
		case a == "--":
			pathsOnly = true
		case strings.HasPrefix(a, "--"):
			if a == "--all" || a == "--update" {
				return SweepsIndex
			}
		default:
			if strings.ContainsAny(a, "Au") {
				return SweepsIndex
			}
		}
	}
	return None
}

// commitLongValue lists commit long options that take a separate value.
var commitLongValue = map[string]bool{
	"--message": true, "--file": true, "--reuse-message": true,
	"--reedit-message": true, "--author": true, "--date": true,
	"--fixup": true, "--squash": true, "--template": true, "--cleanup": true,
	"--trailer": true, "--pathspec-from-file": true,
}

func classifyCommit(args []string) Risk {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return None // the rest are paths
		case len(a) < 2 || a[0] != '-':
			// a path
		case strings.HasPrefix(a, "--"):
			if a == "--all" {
				return SweepsIndex
			}
			if commitLongValue[a] {
				i++
			}
		default:
		cluster:
			for j := 1; j < len(a); j++ {
				switch a[j] {
				case 'a':
					return SweepsIndex
				case 'm', 'F', 'C', 'c', 't': // value follows, attached or as the next word
					if j == len(a)-1 {
						i++
					}
					break cluster
				case 'S', 'u': // optional attached value
					break cluster
				}
			}
		}
	}
	return None
}
