---
name: status
description: Shows which other coding-agent sessions are active on this machine and which files each one holds, and explains what to do about a held file. Use when an edit or a git command was denied by agent-quorum, before starting work that touches many files, before a tree-wide git command (stash, checkout, reset, rebase, add -A, commit -a), or when the user asks who else is working in the repo or why something was blocked.
---

# agent-quorum status

Run this and read the result:

```bash
"${CLAUDE_PLUGIN_ROOT:-${PLUGIN_ROOT}}/scripts/agent-quorum.sh" status
```

## Reading the output

Each session is one line, followed by the files it holds:

```text
claude:9f2c1a7e  pid 4242  idle 40s  /src/app  "refactor token refresh"
    /src/app/auth.go
```

- The first field is the session id. The quoted text is the start of that session's latest prompt, so it tells you what the session is working on.
- A file listed under a session has uncommitted work by that session. Only that session may edit it.
- `(holds no files)` means the session is open but has nothing claimed.
- `daemon not running` or `no live sessions` means nobody holds anything. Empty output means agent-quorum is not installed in this session; say so and carry on.
- One of the sessions listed is you. Your own files never block you.

## What to report

Tell the user, briefly: which other sessions are active in this repository, what each is working on, and which files they hold. If you were asked because something was denied, name the session that holds the file you needed.

## If a file you need is held by another session

1. Do not edit it, and do not change it through the shell (`sed -i`, redirection, `tee`, a formatter, a code generator, `git checkout -- file`). The claim protects someone else's uncommitted work.
2. Do the parts of the task that do not need that file.
3. If the task cannot proceed without it, stop and tell the user which session holds it and what that session is working on. The file frees up when that session commits or reverts it, or when it exits.

## Git while other sessions hold files

- Stage by explicit path: `git add path/to/file`. Never `git add -A`, `git add .` or `git commit -a`.
- Do not run commands that rewrite the working tree: `git stash`, `git checkout <ref>`, `git switch`, `git restore`, `git reset --hard`, `git clean`, `git rebase`, `git merge`, `git pull`. Ask the user instead.
- Commit only files you edited in this session.

## Releasing claims

Claims release themselves; you should rarely need this. Never release another session's claims on your own initiative. Only if the user explicitly asks to free a stuck file:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/agent-quorum.sh" release --session <id-from-status> --all
```
