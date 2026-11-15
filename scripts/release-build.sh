#!/bin/sh
# Cross-compile release archives into ./dist and write checksums.txt.
# Usage: scripts/release-build.sh 1.2.3
set -eu
ver="${1:?usage: release-build.sh <version without v>}"

rm -rf dist
mkdir -p dist
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  os="${target%/*}"
  arch="${target#*/}"
  stage="dist/agent-quorum_${ver}_${os}_${arch}"
  mkdir -p "$stage"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$stage/agent-quorum" ./cmd/agent-quorum
  tar -czf "$stage.tar.gz" -C "$stage" agent-quorum
  rm -rf "$stage"
done

cd dist
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum ./*.tar.gz | sed 's# \./# #' > checksums.txt
else
  shasum -a 256 ./*.tar.gz | sed 's# \./# #' > checksums.txt
fi
cat checksums.txt
