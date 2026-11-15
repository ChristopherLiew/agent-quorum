# agent-quorum

Local coordination for coding-agent sessions that share one working tree. When
several Claude Code sessions (direct, or through an LLM proxy) or other agents
run on the same machine, agent-quorum stops them from editing the same uncommitted
file and from running tree-wide git commands over each other's work, and lets
each session see what the others are doing.

Status: scaffold. The build, test, release and plugin packaging work; the
coordination logic is still to be written. Start with [docs/DESIGN.md](docs/DESIGN.md).

## Develop

```bash
make check        # vet, lint, tests
make dev          # Claude Code with the local plugin and the local binary
```

## Install (after the first release)

```bash
claude plugin marketplace add ChristopherLiew/agent-quorum
claude plugin install agent-quorum@agent-quorum-tools
```
