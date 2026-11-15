#!/bin/sh
# Validate the Codex marketplace entry and installable plugin in an isolated home.
set -eu

command -v codex >/dev/null 2>&1 || { echo "validate-codex-plugin: codex CLI not found" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "validate-codex-plugin: jq not found" >&2; exit 1; }

repo=$(CDPATH='' cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
codex_home="$tmp/codex-home"
mkdir -p "$codex_home"

CODEX_HOME="$codex_home" codex plugin marketplace add "$repo" >/dev/null
available=$(CODEX_HOME="$codex_home" codex plugin list --available --json)
printf '%s\n' "$available" | jq -e '.available | any(.pluginId == "agent-quorum@agent-quorum-tools")' >/dev/null

CODEX_HOME="$codex_home" codex plugin add agent-quorum@agent-quorum-tools >/dev/null
installed=$(CODEX_HOME="$codex_home" codex plugin list --json)
printf '%s\n' "$installed" | jq -e '.installed | any(.pluginId == "agent-quorum@agent-quorum-tools" and .installed)' >/dev/null

plugin_root="$codex_home/plugins/cache/agent-quorum-tools/agent-quorum/$(jq -r .version "$repo/plugin/.codex-plugin/plugin.json")"
jq -e '.hooks == "./hooks/codex.json"' "$plugin_root/.codex-plugin/plugin.json" >/dev/null
jq -e '.hooks.PreToolUse | any(.matcher == "^(Bash|apply_patch)$")' "$plugin_root/hooks/codex.json" >/dev/null
test -x "$plugin_root/scripts/codex-session-start.sh"

echo "Codex marketplace discovery and plugin installation validated"
