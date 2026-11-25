#!/usr/bin/env bash
# test-load-check.sh - prove scripts/load-check.sh refuses a plugin file the core loader refuses:
# builds synthetic plugin build dirs in a temp dir (no wasm compile) and expects OK / FAIL.
#
# Usage: scripts/test-load-check.sh     Env: HATAMOTO_CORE_SRC (else load-check fetches the pinned core once)
set -euo pipefail
export LC_ALL=C.UTF-8
root=$(git rev-parse --show-toplevel)
cd "$root"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0

fixture() { # <name>: $tmp/plugins/<name>/build with one partial and one section
  local b=$tmp/plugins/$1/build
  rm -rf "$tmp/plugins/$1"
  mkdir -p "$b/partials" "$b/sections"
  jq -n --arg n "$1" '{name:$n, version:"0.1.0", sdk:"^0.27", description:"Load check fixture.", permissions:[]}' >"$b/plugin.json"
  printf '\0asm\1\0\0\0' >"$b/plugin.wasm" # the package parse only needs the file present
  printf '{{define "plugin/%s/ok_name"}}ok{{end}}\n' "$1" >"$b/partials/ok_name.html"
  printf '{{define "section-fx_block"}}ok{{end}}\n' >"$b/sections/fx_block.html"
}
check() { # <label> <expected exit 0|1> <regex the output must match> <plugin>...
  local label=$1 want=$2 re=$3 out rc=0
  shift 3
  out=$(scripts/load-check.sh "$@" 2>&1) || rc=$?
  if [ "$rc" != "$want" ]; then
    echo "FAIL $label: exit $rc, want $want:"; echo "$out"; fails=$((fails + 1))
  elif ! grep -qE "$re" <<<"$out"; then
    echo "FAIL $label: no '$re' in:"; echo "$out"; fails=$((fails + 1))
  else
    echo "ok   $label"
  fi
}

fixture good
check "baseline: a well-named plugin loads" 0 '^  good \.+ OK \(partials 1, sections 1\)$' "$tmp/plugins/good"
check "baseline: the core is named in the header" 0 '^load-check: core ' "$tmp/plugins/good"

fixture bad-name
mv "$tmp/plugins/bad-name/build/partials/ok_name.html" "$tmp/plugins/bad-name/build/partials/comment-actions.html"
check "hyphenated partial" 1 'bad-name \.+ FAIL partials/comment-actions\.html: name must match \^\[A-Za-z\]\[A-Za-z0-9_\]\*\$' "$tmp/plugins/bad-name"

fixture bad-section
mv "$tmp/plugins/bad-section/build/sections/fx_block.html" "$tmp/plugins/bad-section/build/sections/fx-block.html"
check "hyphenated section" 1 'FAIL sections/fx-block\.html: name must match' "$tmp/plugins/bad-section"

mkdir -p "$tmp/plugins/nobuild"
check "no build/" 1 'FAIL no build/' "$tmp/plugins/nobuild"

check "one FAIL does not stop the others" 1 '^  good \.+ OK' "$tmp/plugins/good" "$tmp/plugins/bad-name"

((fails == 0)) || { echo "test-load-check: $fails case(s) failed" >&2; exit 1; }
echo "test-load-check: all cases passed"
