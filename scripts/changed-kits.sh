#!/usr/bin/env bash
# changed-kits.sh [BASE [HEAD]] - print the kit folders (kits/<name>) a change
# touches, one per line, as a JSON array with --json: kits changed directly,
# kits whose kit.json names a changed plugins/<name>, and every kit for a change
# outside plugins/ and kits/ (scripts/, .github/, .hatamoto-core, ...).
# No BASE: every kit.
#
# Usage: scripts/changed-kits.sh [--json] [BASE [HEAD]]
set -euo pipefail
json=0
[ "${1:-}" = --json ] && { json=1; shift; }
cd "$(git rev-parse --show-toplevel)"
all() { for f in kits/*/kit.json; do if [ -f "$f" ]; then dirname "$f"; fi; done; }

if [ $# -eq 0 ]; then
  list=$(all)
else
  base=$1 head=${2:-HEAD}
  files=$(git diff --name-only "$base...$head")
  if [ -z "$files" ]; then
    list=
  elif grep -qvE '^(plugins|kits)/' <<<"$files"; then
    list=$(all)
  else
    list=$({
      sed -n 's#^\(kits/[^/]*\)/.*#\1#p' <<<"$files"
      for p in $(sed -n 's#^plugins/\([^/]*\)/.*#\1#p' <<<"$files" | sort -u); do
        for k in kits/*/kit.json; do
          if [ -f "$k" ] && jq -e --arg p "$p" 'any(.plugins[]?; .name == $p)' "$k" >/dev/null; then dirname "$k"; fi
        done
      done
    } | sort -u | while read -r d; do if [ -f "$d/kit.json" ]; then echo "$d"; fi; done)
  fi
fi

if [ "$json" = 1 ]; then
  printf '%s\n' $list | jq -R 'select(length > 0)' | jq -cs .
else
  [ -z "$list" ] || printf '%s\n' $list
fi
