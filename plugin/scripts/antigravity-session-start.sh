#!/bin/sh
dir="${0%/*}"
"$dir/install.sh" </dev/null >/dev/null 2>&1 || true
exec "$dir/agent-quorum.sh" hook --agent antigravity
