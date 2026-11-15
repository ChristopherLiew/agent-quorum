#!/bin/sh
# Download the agent-quorum binary that matches this plugin's version from GitHub
# Releases into ~/.agent-quorum/bin/agent-quorum-<version>. Idempotent and safe to fail:
# callers ignore the exit code and the launcher fails open without a binary.
set -eu

REPO="ChristopherLiew/agent-quorum"

root="${0%/*}/.."
ver=$(sed -n '/"version"/{s/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p;q;}' \
  "$root/.claude-plugin/plugin.json")
[ -n "$ver" ] || { echo "install: no version in plugin.json" >&2; exit 1; }

home="${AGENT_QUORUM_HOME:-$HOME/.agent-quorum}"
bin="$home/bin/agent-quorum-$ver"

[ -x "$bin" ] && exit 0                 # already installed
[ -n "${AGENT_QUORUM_BIN:-}" ] && exit 0      # development override in use

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in darwin | linux) ;; *) echo "install: unsupported OS $os" >&2; exit 1 ;; esac
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "install: unsupported arch $arch" >&2; exit 1 ;;
esac

asset="agent-quorum_${ver}_${os}_${arch}.tar.gz"
base="${AGENT_QUORUM_RELEASE_BASE:-https://github.com/$REPO/releases/download/v$ver}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL --max-time 40 -o "$tmp/$asset" "$base/$asset"
curl -fsSL --max-time 20 -o "$tmp/checksums.txt" "$base/checksums.txt"

# Integrity check against the release's own checksum file.
want=$(awk -v f="$asset" '$2 == f { print $1 }' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$tmp/$asset" | awk '{ print $1 }')
fi
[ -n "$want" ] && [ "$want" = "$got" ] || { echo "install: checksum mismatch for $asset" >&2; exit 1; }

tar -xzf "$tmp/$asset" -C "$tmp"
mkdir -p "$home/bin"
chmod 0755 "$tmp/agent-quorum"
mv "$tmp/agent-quorum" "$bin.$$"   # same filesystem as the target, so the next mv is atomic
mv "$bin.$$" "$bin"
