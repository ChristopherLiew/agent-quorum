#!/bin/sh
# Open a release pull request: create the branch release/v<version> from an
# up-to-date main, set the plugin version, commit, push and open the PR.
# Merging that PR is what publishes the release (see release.yml); main is
# protected, so nothing here pushes to it or creates a tag.
# Usage: scripts/bump-version.sh 1.2.3 | 1.2.3-rc.1
set -eu
ver="${1:?usage: bump-version.sh <version without v>}"
manifest=plugin/.claude-plugin/plugin.json
branch="release/v$ver"

"${0%/*}/semver.sh" check "$ver"
[ -z "$(git status --porcelain)" ] || { echo "working tree is dirty" >&2; exit 1; }
[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || { echo "run this from main" >&2; exit 1; }

git fetch --quiet origin main --tags
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || { echo "local main is not at origin/main; pull first" >&2; exit 1; }
if git rev-parse -q --verify "refs/tags/v$ver" >/dev/null; then
  echo "v$ver is already released; published versions are never reused" >&2
  exit 1
fi
[ "$(jq -r .version "$manifest")" != "$ver" ] || { echo "plugin.json is already at $ver" >&2; exit 1; }

git checkout -q -b "$branch"
tmp=$(mktemp)
jq --arg v "$ver" '.version = $v' "$manifest" > "$tmp"
mv "$tmp" "$manifest"
git add "$manifest"
git commit -q -m "release v$ver"

if [ -n "${NO_PUSH:-}" ]; then
  echo "Created $branch locally. Push it and open a pull request against main."
  exit 0
fi
git push -q -u origin "$branch"
if command -v gh >/dev/null 2>&1; then
  gh pr create --base main --head "$branch" --title "release v$ver" \
    --body "Release v$ver. Merging this pull request publishes it: the release workflow tags the merge commit, builds the binaries, creates the GitHub release and moves the marketplace channel."
else
  echo "Pushed $branch. Open a pull request against main; merging it publishes v$ver."
fi
