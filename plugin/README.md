# agent-quorum for Antigravity

This plugin connects Antigravity sessions to the local agent-quorum daemon.
It registers sessions, shares peer context before prompts, guards risky
`run_command` commands, and claims files before Antigravity's native write and
edit tools run.

## Install from a checkout

Clone the repository and install this directory with the Antigravity CLI:

```sh
agy plugin install ./plugin
```

The first session start downloads the matching `agent-quorum` binary from the
GitHub release and verifies its checksum. Enable the plugin in Antigravity if
the CLI leaves it disabled.

Antigravity file claims cover `write_to_file`, `replace_file_content`, and
`multi_replace_file_content`. Shell edits and changes made outside Antigravity
are not intercepted.
