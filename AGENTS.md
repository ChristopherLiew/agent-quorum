# Repository guidance

This file is shared project guidance for coding agents that read `AGENTS.md` (including Codex and Antigravity). Claude Code also has the more detailed `CLAUDE.md`; read it when working on release, plugin packaging, or Claude hook behavior.

## Project

agent-quorum coordinates coding-agent sessions that share one working tree. A local daemon tracks sessions and file claims. Agent-specific hooks are adapters around a neutral wire protocol; keep agent JSON and output formats out of the daemon and core.

Claude Code, Codex, Antigravity, and OpenCode hooks are implemented. Codex support lives in `internal/adapter/codex/`, its compatibility manifest is `plugin/.codex-plugin/plugin.json`, and its hook config is `plugin/hooks/codex.json`. Codex hooks currently register sessions, record prompts, guard Bash git commands, and claim paths in `apply_patch`. Antigravity support lives in `internal/adapter/antigravity/`, its manifest is `plugin/.antigravity-plugin/plugin.json`, and its hooks are in `plugin/hooks.json`; it registers lifecycle events, records prompts, guards `run_command`, and claims paths for Antigravity's file write and edit tools. OpenCode support lives in `internal/adapter/opencode/` plus a thin TypeScript shim, `plugin/opencode/index.ts`, which OpenCode loads directly; the shim maps OpenCode objects onto a bridge schema owned by the Go adapter and makes no decisions itself.

## Working in this repository

- Read the relevant adapter, core, daemon, or plugin files before changing behavior. Keep changes aligned with existing package boundaries.
- Keep `internal/core`, `internal/guard`, and `internal/adapter/*` pure: no OS access or wall-clock reads. Inject impure behavior through `core.Env`.
- Preserve the fail-open hook contract: errors, timeouts, missing binaries, and panics produce exit status 0 with no output.
- Never emit a permission grant. Empty output means no opinion; a deny must be an agent-valid hook response.
- Normalize file paths in the hook client before sending requests. Core cleans paths but does not resolve symlinks.
- Preserve the shared daemon protocol and session ownership model when adding an agent. Prefer a pure adapter (`Parse` and `Render`) plus agent hook configuration over core changes.
- Shell scripts use POSIX `sh`, fail open if the binary is unavailable, and should be checked with `sh -n` and shellcheck when available.
- Do not edit generated release artifacts or change the plugin version directly. Follow `CLAUDE.md` and `CONTRIBUTING.md` for release steps.

## Build and checks

- `make build` builds the local binary.
- `make lint` checks shell syntax and plugin JSON.
- `make vet` runs `go vet` and checks gofmt.
- `make test` runs the Go test suite with the race detector.
- `make check` runs vet, lint, and tests.

When changing an adapter, keep its input and output behavior covered by focused tests and fixtures. When a check cannot run in the current environment, report the command and the observed limitation rather than treating it as passed.
