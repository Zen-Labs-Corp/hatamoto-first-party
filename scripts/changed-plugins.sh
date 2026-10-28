#!/usr/bin/env bash
# changed-plugins.sh [BASE [HEAD]] - print the plugin folders (plugins/<name>)
# touched between BASE and HEAD, one per line, as a JSON array with --json.
# A change outside plugins/ (scripts/, template/, .github/, ...) counts as a
# change to every plugin. No BASE: every plugin.
#
# Usage: scripts/changed-plugins.sh [--json] [BASE [HEAD]]
set -euo pipefail
json=0
[ "${1:-}" = --json ] && { json=1; shift; }
cd "$(git rev-parse --show-toplevel)"
all() { for f in plugins/*/plugin.json; do [ -f "$f" ] && dirname "$f"; done; }

if [ $# -eq 0 ]; then
  list=$(all)
else
  base=$1 head=${2:-HEAD}
  files=$(git diff --name-only "$base...$head")
  if grep -qv '^plugins/' <<<"$files"; then
    list=$(all)
  else
    list=$(sed -n 's#^\(plugins/[^/]*\)/.*#\1#p' <<<"$files" | sort -u | while read -r d; do
      [ -f "$d/plugin.json" ] && echo "$d"; done)
  fi
fi

if [ "$json" = 1 ]; then
  printf '%s\n' $list | jq -R 'select(length > 0)' | jq -cs .
else
  [ -z "$list" ] || printf '%s\n' $list
fi
