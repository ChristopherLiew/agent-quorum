# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

agent-quorum is local coordination for concurrent coding-agent sessions that share **one working tree** (no worktrees): it stops sessions from editing the same uncommitted file, blocks tree-wide git commands that would clobber another session's work, and lets sessions see each other.

**Current state:** the daemon, hook client, Claude Code adapter, git guard, presence and the `check --staged` pre-commit backstop are implemented and tested (DESIGN.md milestones M1 to M6 and part of M7). Not built yet: the Codex adapter (M8), `PostToolUse` detection of shell edits, the notes mailbox and MCP server, and the mod. No release has been tagged, so the marketplace install cannot download a binary yet; `make dev` is the way to run it.

`docs/DESIGN.md` is the design rationale. Read the relevant section before changing behaviour, but the code wins where they differ (known differences: the hook budget is 1.5 s rather than 250 ms, and the project is no longer standard-library-only). `docs/` is in `.gitignore`, so the design doc is local-only and absent from a fresh clone and from CI. `README.md` is the public description; `CONTRIBUTING.md` holds the dev guide, the semantic versioning rules and the release SOP, which must be followed before any tag.

## Commands

| Command | What it does |
|---|---|
| `make build` | Build `./bin/agent-quorum` (version from `plugin.json` + `-dev`) |
| `make check` | `vet` + `lint` + `test`. What CI runs. |
| `make test` | `go test -race -count=1 ./...` |
| `make vet` | `go vet` plus a gofmt check that fails on unformatted files |
| `make lint` | `sh -n` and shellcheck (optional locally, enforced in CI) on all scripts; `jq` on the plugin JSON |
| `make validate-plugin` | `claude plugin validate` on `./plugin` (`--strict`) and on the marketplace root |
| `make dev` | Build, then start Claude Code with the local plugin and `AGENT_QUORUM_BIN` pointing at the local binary |
| `make snapshot` | Cross-compile all four release archives into `./dist` |
| `make release V=1.2.3` | Check, bump `plugin.json`, commit, annotated tag `v1.2.3` (needs a clean tree; publish with `git push --follow-tags`) |

Single test: `go test -race -run TestHookFailsOpen ./cmd/agent-quorum`

Run a hook by hand: `./bin/agent-quorum hook --agent claude < testdata/hooks/claude/pre_edit.json; echo $?`

Capture real hook payloads: `AGENT_QUORUM_CAPTURE=/tmp/cap.jsonl make dev`, then `jq -c .payload /tmp/cap.jsonl`. The fixtures in `testdata/hooks/claude/` are doc-derived, not captured. The adapter's golden test is strict both ways: every fixture file needs an entry in the `golden` table in `internal/adapter/claude/claude_test.go` and vice versa.

The end-to-end tests in `cmd/agent-quorum/main_test.go` build the real binary and drive a real daemon and git repo. Tests that need a daemon home must use a short path under `/tmp` (macOS limits Unix socket paths to about 104 bytes, so `t.TempDir()` is too long), and in-process hook tests must set `AGENT_QUORUM_NO_START=1` so the test binary is never spawned as a daemon.

Debug hooks in a live session with `claude --debug-file /tmp/cc.log` (search for lines starting `Hooks:`); apply `hooks.json` edits with `/reload-plugins`.

## Architecture

One Go binary (Go 1.27; macOS and Linux), three roles:

- **`agent-quorum hook --agent <name>`**: short-lived process per hook call. Parses the agent's hook JSON from stdin (adapter), sends one request over a Unix socket, prints the agent-specific decision, exits. Stateless. Budget 1.5 s (0.8 s for `SessionEnd`), deliberately generous because the slow path is a contended claim, where the daemon runs `git status`, and timing out there would fail open exactly when a deny is due.
- **`agent-quorum serve`**: one daemon per user, started on demand by the first hook that cannot connect, single instance enforced by `flock`. Holds sessions and claims in memory behind one mutex; snapshots to `state.json` after every mutation.
- **`agent-quorum status | release | check | stop`**: human and git-hook CLI over the same socket and protocol.

Runtime files live in `~/.agent-quorum/` (override: `AGENT_QUORUM_HOME`): socket, `daemon.lock`, `state.json`, `daemon.log`, `bin/agent-quorum-<version>`. Nothing may derive paths from Claude's config dir, `CLAUDE_PLUGIN_ROOT` or `CLAUDE_PLUGIN_DATA`: sessions with different config dirs must reach the same daemon, and the plugin root changes on every update.

Package layout:

```
internal/proto/            Request, Response, Version (neutral wire protocol)
internal/core/             State, Session, Claim, decisions
internal/guard/            Classify(command) Risk
internal/adapter/claude/   Parse, Render
internal/daemon/           socket server, lock, snapshot, real Env
internal/client/           dial, start-on-demand, deadlines
internal/gitx/             thin wrappers over the git CLI
```

### Code style

- Google Go Style Guide, strictly (doc comments on every exported identifier, `got` before `want` in test messages, no shadowed builtins, lower-case wrapped errors).
- JSON goes through `github.com/bytedance/sonic`, never `encoding/json`. Generic slice and map helpers come from `github.com/bytedance/gg` where they replace a plain map/filter/collect loop.
- Keep dependencies to those two unless the user asks for more; release binaries are built with `CGO_ENABLED=0` for four targets.

### Invariants

- **Fail open, always.** Any error, panic, timeout, missing binary or missing daemon in the hook path means exit 0 with empty stdout. "A broken agent-quorum is invisible" is the property to protect above all; every new failure path in `hook` gets a test for it. Do not set `onFailure` in `hooks.json`.
- **Never emit `permissionDecision: "allow"`.** A grant is empty output (no opinion); `allow` would bypass the user's permission prompts. A deny is exit 0 with the `hookSpecificOutput` deny JSON on one line.
- **Deny reasons are written for the model**, as instructions: who holds the file, what not to do (including shell workarounds), what to do instead.
- **`core`, `guard` and `adapter/*` are pure**: no OS imports, no `time.Now()`. Everything impure is injected through `core.Env` (`Now`, `Alive`, `Dirty`, `RepoRoot`). `State.Handle(req)` is the only entry point into core. `daemon` depends on core only through its `Handler` and `Persister` interfaces; `cmd/agent-quorum` wires them together.
- **Paths are normalised in the client** (`client.NormalizePath`: clean, then resolve symlinks through the nearest existing ancestor). Core only cleans them.
- **The session's pid is the hook's parent process** (verified against Claude Code 2.1.325). Liveness depends on it.
- **The daemon never sees agent-specific JSON.** Supporting another agent is an adapter (two pure functions, golden-file tested) plus a hook config, with no core changes.
- **Claims** are implicit (taken by the first edit), keyed by exact absolute file path, owned by the session (`<agent>:<session_id>`); subagents share their parent's claims.
- **Release by evidence, validated lazily.** A claim is valid while its holder is alive AND (not a git repo OR the file is dirty OR the claim is younger than the 120 s grace period). Checked only at contention or status time: no reaper, no timers, no background goroutines.
- **Wire protocol**: one connection per request, one JSON line each way, `v` bumped only for incompatible changes.

### Plugin and release packaging

The repo root is both the source tree and a Claude Code marketplace (`.claude-plugin/marketplace.json`, name `agent-quorum-tools`) with one plugin at `./plugin`.

- `plugin/.claude-plugin/plugin.json` `version` is the single source of truth for the version (never also set it in the marketplace entry). The release workflow fails if the tag does not match it. Change it only through `make release`, which accepts `1.2.3` and pre-releases such as `1.2.3-rc.1`.
- Marketplace channels are the branches `stable` and `latest`, moved only by `scripts/publish-channels.sh` from `release.yml` after the binaries are published. Never commit to them by hand. `CONTRIBUTING.md` (CI/CD) has the rules.
- The plugin ships shell scripts only, in `scripts/` (a top-level `bin/` is rejected by claude.ai and Cowork). The binary is downloaded from GitHub Releases to `~/.agent-quorum/bin/agent-quorum-<version>` by `install.sh`, with checksum verification and an atomic move.
- Every hook goes through `plugin/scripts/agent-quorum.sh`, which resolves `$AGENT_QUORUM_BIN`, then the versioned installed binary, then `agent-quorum` on `PATH`, and exits 0 if none exists.
- Hooks use exec form (`args` present) and the launcher uses `exec`, so that `getppid()` in the binary is the agent process. Liveness detection depends on this; do not insert a shell between them.
- Hooks for one event run in parallel, so install-then-register on `SessionStart` is one script (`session-start.sh`), not two hooks.
- Scripts are POSIX `sh`, must pass shellcheck, and parse `plugin.json` with `sed` (no `jq` at runtime on user machines).
- Asset naming is a contract between `scripts/release-build.sh` and `plugin/scripts/install.sh`: `agent-quorum_<version>_<os>_<arch>.tar.gz` plus `checksums.txt`.
