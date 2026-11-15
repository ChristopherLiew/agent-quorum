# Contributing to agent-quorum

Thanks for helping. This file covers the dev loop, the rules the code lives by, and how releases are versioned and cut. For what the tool does, read [README.md](README.md).

## Prerequisites

- Go 1.27
- `jq` (used by `make lint`, `make release` and the version stamp)
- `shellcheck` (optional locally, enforced in CI)
- the `claude` CLI, for `make validate-plugin` and for trying the plugin by hand
- `git`, `curl`, `tar`
- macOS or Linux

## Dev loop

```bash
make check      # vet + lint + test: exactly what CI runs
make dev        # build, then start Claude Code with the local plugin and local binary
```

Every make target:

| Target | What it does |
|---|---|
| `make build` | Builds `./bin/agent-quorum`, version stamped as `<plugin.json version>-dev`. |
| `make test` | `go test -race -count=1 ./...` |
| `make vet` | `go vet ./...` plus a gofmt check that fails on unformatted files. |
| `make lint` | `sh -n` and shellcheck on `plugin/scripts/*.sh` and `scripts/*.sh`, and `jq empty` on the marketplace, plugin and hooks JSON. |
| `make check` | `vet`, `lint`, `test`. |
| `make validate-plugin` | `claude plugin validate ./plugin --strict` and `claude plugin validate .`. Needs the `claude` CLI. |
| `make snapshot` | Cross-compiles the four release archives (darwin and linux, amd64 and arm64) and `checksums.txt` into `./dist`. |
| `make dev` | `make build`, then `AGENT_QUORUM_BIN=$PWD/bin/agent-quorum claude --plugin-dir ./plugin`. |
| `make release V=1.2.3` | Runs `make check`, then `scripts/bump-version.sh`: edits `plugin.json`, commits, and creates the annotated tag `v1.2.3`. Does not push. |
| `make clean` | Removes `bin` and `dist`. |

Run one test:

```bash
go test -race -run TestHookFailsOpen ./cmd/agent-quorum
```

Run a hook by hand, with no agent involved:

```bash
./bin/agent-quorum hook --agent claude < testdata/hooks/claude/pre_edit.json; echo $?
```

Capture real hook payloads from your Claude Code version with `AGENT_QUORUM_CAPTURE=/tmp/cap.jsonl make dev`, then `jq -c .payload /tmp/cap.jsonl`. Debug hooks in a live session with `claude --debug-file /tmp/cc.log` and search for lines starting `Hooks:`; apply `hooks.json` edits with `/reload-plugins`.

Use a scratch runtime directory when experimenting so you do not disturb your real daemon: `export AGENT_QUORUM_HOME=$(mktemp -d)`.

## Project layout

```text
cmd/agent-quorum/           argument dispatch (main.go), hook entry (hook.go), CLI commands (cli.go)
internal/proto/             Request, Response, Version: the neutral wire protocol
internal/core/              State, Session, Claim, decisions and the deny/context texts. Pure.
internal/guard/             Classify(command) Risk. Pure.
internal/adapter/claude/    Parse and Render for Claude Code hook JSON. Pure.
internal/daemon/            socket server, flock, snapshot, the real core.Env
internal/client/            dial, start-on-demand, deadlines, path normalisation
internal/gitx/              thin wrappers over the git CLI
plugin/                     the Claude Code plugin: hooks/hooks.json, scripts/*.sh, skills/status
scripts/                    release-build.sh, bump-version.sh
testdata/hooks/claude/      hook payload fixtures
.claude-plugin/             marketplace.json: the repo root is also a marketplace
```

### Invariants

Do not break these. Review will check each one.

1. **Fail open, always.** Any error, panic, timeout, missing binary or missing daemon in the hook path means exit 0 with empty stdout. A broken agent-quorum must be invisible. Do not set `onFailure` in `hooks.json`.
2. **Never emit `permissionDecision: "allow"`.** A grant is empty output. `allow` would bypass the user's permission prompts. A deny is exit 0 with the `hookSpecificOutput` deny JSON on one line.
3. **Deny reasons are written for the model**, as instructions: who holds the file, what not to do (including shell workarounds), what to do instead.
4. **`core`, `guard` and `adapter/*` are pure.** No OS imports and no `time.Now()`. Everything impure is injected through `core.Env` (`Now`, `Alive`, `Dirty`, `RepoRoot`). `State.Handle(req)` is the only entry point into core.
5. **Neutral core, adapters at the edge.** The daemon never sees agent-specific JSON. Supporting another agent is an adapter (two pure functions, golden-file tested) plus a hook config, with no core changes.
6. **Release by evidence, validated lazily.** No reaper, timers or background goroutines. Claim validity is checked only at contention or status time.
7. **Exec all the way down.** Hooks use exec form (`args` present) and the launcher uses `exec`, so `getppid()` in the binary is the agent process. Liveness detection depends on that; never insert a shell in between.
8. **Nothing derives paths from Claude's config dir, `CLAUDE_PLUGIN_ROOT` or `CLAUDE_PLUGIN_DATA`.** Sessions with different config dirs must reach the same daemon.
9. **Plugin scripts depend on nothing but POSIX tools.** No `jq` at runtime on a user's machine; `plugin.json` is read with `sed`.

## Code style

- **Go:** follow the [Google Go Style Guide](https://google.github.io/styleguide/go/) strictly, including its decisions and best-practices documents. `gofmt` is enforced by `make vet`.
- **JSON:** use `github.com/bytedance/sonic`. Do not reach for `encoding/json` in new code.
- **Generic helpers:** use `github.com/bytedance/gg` rather than hand-rolled helpers for maps, slices and options.
- **Shell:** POSIX `sh`, no bashisms, passing `shellcheck`. Plugin scripts must fail open and stay cheap, since the launcher runs on every hook call.
- **Comments and docs:** plain prose, no emojis.

## Testing expectations

- Table-driven tests with a fake `core.Env`. Core logic is tested without a daemon, a clock or git.
- Golden fixtures: each adapter is tested against payloads in `testdata/hooks/<agent>/`. When Claude Code changes a payload, update the fixtures from a real capture, not from memory.
- **Every new failure path in the hook gets a fail-open test** proving the result is exit 0 with empty stdout. `TestHookFailsOpen` in `cmd/agent-quorum` is the model.
- Run everything with `-race` (`make test` does). Concurrency tests, such as many goroutines claiming one path with exactly one grant, belong in the package that owns the lock.
- A new guard rule needs a table row for the case it blocks and one for the near-miss it must allow.
- Changes to deny or context text need a test on the text, since the model is the reader.

## Commits and pull requests

- Conventional, short subjects in the imperative: `fix: allow git stash list`, `feat: add status --json`, `docs: explain grace period`. One logical change per commit.
- Branch from `main`, open a pull request against `main`, and keep it focused. Describe the behaviour change and how you tested it. Mention a change to any public surface (see [Versioning](#versioning)) explicitly.
- `make check` must pass. CI also runs `make validate-plugin`.
- Do not edit the `version` in `plugin/.claude-plugin/plugin.json` by hand. It changes only through `make release`.

## Versioning

agent-quorum follows [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html). Versions have the form `MAJOR.MINOR.PATCH` with an optional pre-release suffix.

### What is the public API

Semantic Versioning needs a declared public API. For this project it is everything a user, an agent or another tool can observe or depend on:

1. **Hook behaviour that agents observe.** Which events and tools are hooked, when an edit or command is denied or granted, that a grant is silent, and the shape of the deny JSON. The wording of deny and context texts is not part of the API; their meaning is.
2. **The CLI.** Commands, flags, arguments, exit codes, and the output of `status --json`. Human-readable output of `status` is not a stable interface; use `--json`.
3. **The wire protocol.** The request and response fields and ops, and the protocol version `v` (`proto.Version`).
4. **The `state.json` format**, because a new binary must be able to reload a snapshot written by an older one.
5. **Environment variables** (`AGENT_QUORUM_*`) and their meanings, and the layout of `~/.agent-quorum`.
6. **The plugin manifest and layout**: plugin name, marketplace name (`agent-quorum-tools`), hook registrations in `hooks.json`, the skill name, and the release asset naming contract (`agent-quorum_<version>_<os>_<arch>.tar.gz` plus `checksums.txt`).

Go packages under `internal/` are not public API.

### MAJOR, MINOR, PATCH

**MAJOR** for an incompatible change to the public API:

- A command, flag, environment variable or `status --json` field is removed or changes meaning (for example, removing `release --all`).
- The `state.json` format changes so that an older snapshot can no longer be loaded.
- The wire protocol `v` is bumped for an incompatible change.
- A hook that was granting now denies by default, or the guard starts blocking a command class that was previously allowed in the same situations, in a way that breaks existing workflows (for example, denying `git push`).
- Dropping a supported platform or agent, or renaming the plugin or marketplace.

**MINOR** for backward-compatible additions and notable behaviour:

- A new CLI command or flag (the way `install-git-hook` was added), a new `status --json` field, a new environment variable.
- A new hook event or matcher, or a new adapter such as Codex.
- A new guard verb that blocks only a clearly destructive command, when documented in the changelog.
- A new optional wire-protocol field or op that older clients may ignore.
- Deprecating something (removal waits for the next MAJOR).

**PATCH** for fixes that keep the documented behaviour:

- A guard misclassification fixed in the direction of the documented rules (for example, `git stash list` wrongly denied).
- A fail-open hole closed, a race fixed, a deny message reworded, a crash or timeout fixed.
- Install-script and launcher fixes, documentation and test-only changes, dependency bumps with no behaviour change.

When in doubt, ask who would be surprised. If a user's existing script, hook config or habit would break, it is MAJOR (or MINOR before 1.0, see below).

### Before 1.0 (0.y.z)

While the version is `0.y.z`, anything may change at any time under the Semantic Versioning spec. This project adds two rules of its own:

- A change that would be MAJOR after 1.0 bumps `y` (for example, 0.3.0 to 0.4.0) and is called out under "Breaking" in the changelog.
- A change that would be MINOR or PATCH after 1.0 bumps `z`, except that notable new features may bump `y`.

Version 1.0.0 will be declared once the Claude Code hooks, the CLI and the wire protocol have been stable across a few releases and real use.

### Pre-release tags

A pre-release such as `v0.2.0-rc.1` precedes `v0.2.0` in precedence and means "candidate, may still change". Use `-rc.N` for release candidates.

- `make release V=0.2.0-rc.1` works the same as for a stable version. `scripts/semver.sh` validates the version strictly (it rejects `1.2`, `v1.2.3`, `1.2.3.4` and leading zeros).
- The release workflow publishes a tag with a pre-release suffix as a GitHub **pre-release**, and moves only the `latest` channel. Stable users are not affected.
- Release assets carry the full version: `agent-quorum_0.2.0-rc.1_linux_amd64.tar.gz`.
- Cut release candidates from a release branch, not from `main`. A user who added the marketplace without a channel reads `main`, and would otherwise be offered the candidate.

### Where the version lives

- `plugin/.claude-plugin/plugin.json` `version` is the single source of truth. `make release` is the only thing that edits it.
- The git tag is `v<version>`, annotated, created by `scripts/bump-version.sh`. The release workflow fails if tag and manifest disagree.
- Binaries are stamped at build time with `-ldflags "-X main.version=<version>"`. `scripts/release-build.sh` stamps the exact version; `make build` stamps `<version>-dev`. An unstamped `go build` reports `dev`.
- Claude Code users stay on their cached copy of a plugin until the `version` string changes, so a release is a version bump or it is nothing.
- The **wire protocol version `v` is independent of the product version.** It changes only for an incompatible protocol change, which is also a MAJOR change of the product. Many product releases share `v: 1`.
- **Mixed versions.** Two sessions running different plugin versions share one daemon: whichever hook started it determines the daemon's version. A new hook binary talking to an old daemon works as long as `v` matches, and new behaviour in the daemon appears only after `agent-quorum stop` and a restart. Keep MINOR and PATCH releases compatible with a running older daemon and with a `state.json` written by it.

## CI/CD

Two workflows in `.github/workflows/`.

**`ci.yml`** runs on every pull request and on pushes to `main`:

| Job | What it checks |
|---|---|
| `test` (Ubuntu and macOS) | `make vet`, `make test` (race detector), `make build` |
| `lint` | `make lint` (shellcheck, `sh -n`, JSON syntax), and that the launcher fails open when no binary exists |
| `plugin` | `make validate-plugin`: `claude plugin validate ./plugin --strict` and the marketplace manifest |

**`release.yml`** runs when a `v*` tag is pushed. Releases are serialised and never cancelled halfway.

```mermaid
flowchart TD
  T["tag vX.Y.Z or vX.Y.Z-rc.N pushed"] --> V["tag equals plugin.json version, and is valid SemVer"]
  V --> C["make check and make validate-plugin"]
  C --> B["build 4 archives and checksums.txt"]
  B --> G["GitHub release (marked pre-release for -rc tags)"]
  G --> P["claude plugin tag: agent-quorum--vX.Y.Z"]
  P --> Q{"pre-release?"}
  Q -- "yes" --> L["move branch latest"]
  Q -- "no" --> S["move branch stable, and latest unless it is on a newer line"]
```

### Marketplace channels

Claude Code has no release-channel concept: one marketplace ref serves one version of a plugin, and a user gets an update only when the plugin's `version` changes. Channels are therefore two branches of this repository that the release workflow moves, and a user picks one when adding the marketplace:

```bash
claude plugin marketplace add ChristopherLiew/agent-quorum#stable   # stable releases only
claude plugin marketplace add ChristopherLiew/agent-quorum#latest   # release candidates too
```

| Branch | Moves when | Who should use it |
|---|---|---|
| `stable` | a release without a pre-release suffix is published | everyone by default |
| `latest` | any pre-release is published; also on a stable release, unless `latest` is already on a newer `MAJOR.MINOR.PATCH` line | people testing release candidates |
| `main` | on every merge | contributors; not a channel |

Rules that follow from this:

- Never commit to `stable` or `latest` by hand. `scripts/publish-channels.sh` force-moves them to a released commit, and only after the binaries are published, so a channel never advertises a version whose download would fail.
- `version` is set only in `plugin.json`, never also in the marketplace entry. Claude Code uses the manifest value and `claude plugin validate` reports a mismatch.
- A channel moving without a version change does nothing for users, who stay on their cached copy. Every release is a version bump.
- Both channels share the marketplace name `agent-quorum-tools`, so a user is on one channel at a time. To switch, remove the marketplace and add it again with the other suffix.
- Third-party marketplaces do not auto-update by default. Users enable it under `/plugin`, or run `claude plugin update agent-quorum@agent-quorum-tools`.
- The `agent-quorum--v<version>` tag created by `claude plugin tag` is the convention that lets other plugins depend on a version range of this one. It checks that the manifest and marketplace entry agree and that the tree is clean.

To rehearse the channel moves without pushing: `DRY_RUN=1 scripts/publish-channels.sh 0.2.0-rc.1 "$(git rev-parse HEAD)"`.

## Release SOP

Run this before every tag. Do not skip steps because the change is small.

```mermaid
flowchart LR
  R["make release V=x.y.z"] --> B["plugin.json version set, commit and tag vx.y.z"]
  B --> P["git push --follow-tags"]
  P --> CI["release.yml: tag equals plugin.json, make check, build 4 archives"]
  CI --> GH["GitHub release: archives and checksums.txt"]
  P --> MK["marketplace on main now lists x.y.z"]
  MK --> UP["user updates the plugin"]
  UP --> SS["next SessionStart: install.sh downloads and verifies x.y.z"]
  GH --> SS
```

1. **Clean tree, on `main`, CI green.** `git status` is clean, you are on an up-to-date `main`, and the latest CI run on that commit passed on both Ubuntu and macOS.
2. **Decide the bump** (`x.y.z`) from the rules above, by reading the changes since the last tag (`git log <last-tag>..HEAD`). Look for anything touching the public API.
3. **`make check`.** vet, lint and race tests must pass locally. Install `shellcheck` so lint is not skipped.
4. **`make validate-plugin`.** Strict validation of the plugin and of the marketplace root.
5. **`make snapshot`, then inspect `dist/`.** There must be exactly four archives (`darwin` and `linux`, `amd64` and `arm64`) and `checksums.txt`. Verify the checksums (`shasum -a 256 -c checksums.txt` run inside `dist/`), unpack the archive for your platform and run `agent-quorum version`.
6. **End-to-end by hand with two real sessions** on the current Claude Code version, using a snapshot or `make dev` build and a scratch repository and `AGENT_QUORUM_HOME`:
   - Edit conflict: session A edits a file, session B's edit to it is denied with the holder named.
   - Kill and recover: kill A (`kill`, not `/exit`), then confirm B can edit the file, and `agent-quorum status` no longer lists A.
   - Commit and release: A edits, commits, waits past the 120 second grace period, and B's edit is then granted.
   - Git stash refusal: while A holds a dirty file, B's `git stash` and `git add -A` are denied, and `git add <path>` is allowed.
   - Also confirm a normal, single-session run is unaffected and that the daemon starts and stops cleanly (`agent-quorum stop`).
7. **Real fixtures still match.** Capture payloads with `AGENT_QUORUM_CAPTURE` on the current Claude Code version and diff them against `testdata/hooks/claude/`. Update fixtures and adapter if the payloads drifted.
8. **Docs.** README and the changelog (or the draft release notes, if no CHANGELOG file exists yet) describe the release, including anything under "Breaking" for a pre-1.0 `y` bump.
9. **Cut it:** `make release V=x.y.z`. This runs `make check`, edits `plugin.json`, commits `release vx.y.z`, and creates the annotated tag.
10. **Publish:** `git push --follow-tags`. Then watch the `release` workflow in GitHub Actions to completion (see [CI/CD](#cicd) for what it does).
11. **Verify the release.** The release page has the four `agent-quorum_<version>_<os>_<arch>.tar.gz` assets and `checksums.txt`, a pre-release is marked as one, the `agent-quorum--v<version>` tag exists, and the channel branches point where you expect (`git ls-remote origin stable latest`). On a clean machine or with an empty `HOME`, install through the marketplace, start a session, and confirm `~/.agent-quorum/bin/agent-quorum-<version>` appears (the download is checksum-verified by `install.sh`, and a mismatch leaves no binary) and `status` works.
12. **Post-release.** Run `agent-quorum stop` on your own machine so the next hook starts a daemon of the new version (old daemons keep running otherwise). Announce the release with a link to the notes and any breaking changes.

### Rollback

Users cache plugin versions by their `version` string, and a tag and its checksums are a promise. Therefore:

- **Never delete and re-tag a published version, and never replace its assets.** Anyone who already installed it would keep the old bytes under the same version, and nobody could tell.
- **Ship a new PATCH that fixes or reverts the problem.** Revert the offending commit on `main`, run the SOP again, and release `x.y.(z+1)`. Users receive it when they update the plugin.
- If the bad release is dangerous, edit its GitHub release notes with a warning. If the binary itself is the problem, deleting that release's assets makes installs fail, which fails open, so agents keep working without coordination until the fix ships.
- A bad daemon keeps running after a bad release is replaced. Tell users to run `agent-quorum stop`.

### The marketplace window

Users on the `stable` or `latest` channel are not exposed to a half-published release, because the workflow moves those branches last. The window remains only for users who added the marketplace without a channel suffix: they read `main`, which carries the version bump as soon as it is pushed, a few minutes before the binaries exist. For them `install.sh` fails, the launcher fails open, and the next `SessionStart` retries the download. Point users at `#stable` to avoid it.

## Known gaps in the release tooling

- There is no CHANGELOG file yet; release notes are generated by GitHub from the commits.
- The plugin icon (`plugin/.claude-plugin/icon.png`) can only be set once for a public directory listing: the first time the plugin is saved or submitted. Replace the default before submitting if you want a different one.
