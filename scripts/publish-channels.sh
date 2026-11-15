#!/bin/sh
# Move the marketplace channel branches to a released commit.
# Usage: scripts/publish-channels.sh <version> <commit> [remote]
#
# Users pick a channel when they add the marketplace:
#   claude plugin marketplace add ChristopherLiew/agent-quorum#stable
#   claude plugin marketplace add ChristopherLiew/agent-quorum#latest
#
#   stable  moves on every release that is not a pre-release
#   latest  moves on every pre-release, and on a stable release unless
#           latest is already on a newer MAJOR.MINOR.PATCH line (so a patch
#           to 0.1.x does not pull latest back from 0.2.0-rc.1)
#
# Set DRY_RUN=1 to print the pushes instead of running them.
set -eu
ver="${1:?usage: publish-channels.sh <version> <commit> [remote]}"
sha="${2:?usage: publish-channels.sh <version> <commit> [remote]}"
remote="${3:-origin}"
dir="${0%/*}"
manifest=plugin/.claude-plugin/plugin.json

"$dir/semver.sh" check "$ver"
pre=$("$dir/semver.sh" prerelease "$ver")
base=$("$dir/semver.sh" base "$ver")

push() {
  if [ -n "${DRY_RUN:-}" ]; then
    echo "would move $1 to $sha ($ver)"
  else
    git push --force "$remote" "$sha:refs/heads/$1"
    echo "moved $1 to $sha ($ver)"
  fi
}

# newer_line A B: true when MAJOR.MINOR.PATCH A is strictly greater than B.
newer_line() {
  [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1,1n -k2,2n -k3,3n | tail -n 1)" = "$1" ]
}

if [ "$pre" = true ]; then
  push latest
  exit 0
fi

push stable

current=""
if git fetch --quiet "$remote" latest 2>/dev/null; then
  current=$(git show "FETCH_HEAD:$manifest" 2>/dev/null | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
fi
if [ -n "$current" ] && newer_line "$("$dir/semver.sh" base "$current")" "$base"; then
  echo "latest stays on $current (newer line than $ver)"
else
  push latest
fi
