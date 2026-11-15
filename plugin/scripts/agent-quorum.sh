#!/bin/sh
# Launcher: find the agent-quorum binary and exec it with the same stdin and args.
# Runs on every hook call, so it stays cheap (one sed) and it FAILS OPEN:
# if no binary can be found it exits 0, which every agent reads as "no opinion".
#
# Resolution order:
#   1. $AGENT_QUORUM_BIN                      (development: point at ./bin/agent-quorum)
#   2. ~/.agent-quorum/bin/agent-quorum-<version>   (installed by install.sh; version = plugin.json)
#   3. agent-quorum on PATH                   (go install / manual install)
root="${0%/*}/.."

if [ -n "${AGENT_QUORUM_BIN:-}" ] && [ -x "$AGENT_QUORUM_BIN" ]; then
  exec "$AGENT_QUORUM_BIN" "$@"
fi

ver=$(sed -n '/"version"/{s/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p;q;}' \
  "$root/.claude-plugin/plugin.json" 2>/dev/null)
bin="${AGENT_QUORUM_HOME:-$HOME/.agent-quorum}/bin/agent-quorum-$ver"
if [ -n "$ver" ] && [ -x "$bin" ]; then
  exec "$bin" "$@"
fi

if command -v agent-quorum >/dev/null 2>&1; then
  exec agent-quorum "$@"
fi

exit 0
