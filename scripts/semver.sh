#!/bin/sh
# Semantic Versioning 2.0.0 helpers shared by the release scripts and CI.
#   semver.sh check <version>        exit 1 unless <version> is valid (no leading v)
#   semver.sh prerelease <version>   print true or false
#   semver.sh base <version>         print MAJOR.MINOR.PATCH
set -eu
cmd="${1:?usage: semver.sh check|prerelease|base <version>}"
ver="${2:?usage: semver.sh check|prerelease|base <version>}"

num='(0|[1-9][0-9]*)'
ident='[0-9A-Za-z-]+'
re="^$num\\.$num\\.$num(-$ident(\\.$ident)*)?(\\+$ident(\\.$ident)*)?\$"

valid() { printf '%s\n' "$ver" | grep -Eq "$re"; }

case "$cmd" in
  check)
    valid || { echo "not a semantic version: $ver (want 1.2.3 or 1.2.3-rc.1)" >&2; exit 1; }
    ;;
  prerelease)
    valid || { echo "not a semantic version: $ver" >&2; exit 1; }
    case "${ver%%+*}" in *-*) echo true ;; *) echo false ;; esac
    ;;
  base)
    valid || { echo "not a semantic version: $ver" >&2; exit 1; }
    v="${ver%%+*}"
    echo "${v%%-*}"
    ;;
  *)
    echo "usage: semver.sh check|prerelease|base <version>" >&2
    exit 2
    ;;
esac
