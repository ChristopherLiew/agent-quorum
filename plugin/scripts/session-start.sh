#!/bin/sh
# SessionStart entry point: make sure the binary for this plugin version is
# installed, then hand the hook payload (still unread on stdin) to agent-quorum.
# Hooks for one event run in parallel, so install and register must be ONE hook.
dir="${0%/*}"
"$dir/install.sh" </dev/null >/dev/null 2>&1 || true
exec "$dir/agent-quorum.sh" hook --agent claude
