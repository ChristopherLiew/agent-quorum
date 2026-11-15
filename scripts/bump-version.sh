#!/bin/sh
# Set the plugin version, commit, and create the annotated tag that triggers
# the release workflow. Usage: scripts/bump-version.sh 1.2.3 | 1.2.3-rc.1
set -eu
ver="${1:?usage: bump-version.sh <version without v>}"
manifest=plugin/.claude-plugin/plugin.json

"${0%/*}/semver.sh" check "$ver"
[ -z "$(git status --porcelain)" ] || { echo "working tree is dirty" >&2; exit 1; }

tmp=$(mktemp)
jq --arg v "$ver" '.version = $v' "$manifest" > "$tmp"
mv "$tmp" "$manifest"

git add "$manifest"
git commit -m "release v$ver"
git tag -a "v$ver" -m "agent-quorum v$ver"
echo "Tagged v$ver. Publish with: git push --follow-tags"
