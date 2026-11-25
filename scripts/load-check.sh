#!/usr/bin/env bash
# load-check.sh - load every plugin's build/ through the core loader at the commit pinned in
# .hatamoto-core (package parse, the asset read install and serve run, and the template
# registration and compile that follows), so a file or template the host refuses fails here. Run scripts/build-plugin.sh first. One line per plugin; exit 1 on any FAIL.
# Usage: scripts/load-check.sh [plugins/<name> ...]   (default: every plugins/*/)
# Env:   HATAMOTO_CORE_SRC  a core checkout, normally at the pin (skips the fetch); the helper is
#        copied in for the run and removed after, and a HEAD other than the pin is said in the header
# Env:   LOAD_CHECK_KNOWN  known-refusal list (default scripts/load-check-known.txt): `<plugin> <substring of the FAIL reason>`
#        per line, `#` comments. A match prints KNOWN FAIL and is not counted; a listed plugin that fails for another
#        reason still fails, and one that now passes fails too (remove it from the list).
# The core source is fetched once into ${XDG_CACHE_HOME:-~/.cache}/hatamoto-first-party/core-src-<sha>/.
set -euo pipefail
die() { echo "load-check: $*" >&2; exit 1; }
root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
sha=$(tr -d '[:space:]' < "$root/.hatamoto-core")
[[ $sha =~ ^[0-9a-f]{40}$ ]] || die ".hatamoto-core must hold a 40 hex commit"
repo=https://github.com/Zen-Labs-Corp/hatamoto
src=${HATAMOTO_CORE_SRC:-${XDG_CACHE_HOME:-$HOME/.cache}/hatamoto-first-party/core-src-$sha}
if [ ! -f "$src/backend/go.mod" ]; then
  [ -z "${HATAMOTO_CORE_SRC:-}" ] || die "HATAMOTO_CORE_SRC has no backend/go.mod"
  mkdir -p "$(dirname "$src")"
  tmp=$(mktemp -d "$(dirname "$src")/.core-src.XXXXXX")
  trap 'rm -rf "$tmp"' EXIT
  git -C "$tmp" init -q
  git -C "$tmp" fetch -q --depth 1 "$repo" "$sha" >&2
  git -C "$tmp" checkout -q FETCH_HEAD
  tag=$(git ls-remote --tags "$repo" 2>/dev/null | awk -v s="$sha" '$1==s && $2 ~ /refs\/tags\/v[0-9][^\/]*\^\{\}$/ {sub("refs/tags/","",$2); sub("\\^\\{\\}","",$2); print $2; exit}' || true)
  echo "${tag:-${sha:0:12}}" > "$tmp/.load-check-core"
  rm -rf "$src"
  mv "$tmp" "$src" 2>/dev/null || [ -f "$src/backend/go.mod" ] || die "cannot place the core source in $src"
fi
src=$(cd "$src" && pwd)
core=$(cat "$src/.load-check-core" 2>/dev/null || echo "${sha:0:12}")
helper=$src/backend/cmd/fp-loadcheck
trap 'rm -rf "$helper" ${tmp:+"$tmp"}' EXIT
mkdir -p "$helper"
cp "$root/scripts/loadcheck/main.go" "$src/backend/cmd/fp-loadcheck/main.go"
dirs=("$@")
[ ${#dirs[@]} -gt 0 ] || dirs=("$root"/plugins/*/)
args=()
for d in "${dirs[@]}"; do
  d=${d%/}; [[ $d = /* ]] || d=$PWD/$d
  args+=("$(basename "$d")=$d/build")
done
head=$(git -C "$src" rev-parse HEAD 2>/dev/null || true)
if [ -n "${HATAMOTO_CORE_SRC:-}" ] && [ "$head" != "$sha" ]; then
  echo "load-check: WARNING core checkout HEAD ${head:0:12} is not the .hatamoto-core pin ${sha:0:12}" >&2
  echo "load-check: core ${head:0:12} (NOT the pin)"
else
  echo "load-check: core $core (.hatamoto-core pin)"
fi
cd "$src/backend"
known=${LOAD_CHECK_KNOWN:-$root/scripts/load-check-known.txt}
rc=0
out=$(GOWORK=off GOFLAGS= go run ./cmd/fp-loadcheck "${args[@]}" 2>&1) || rc=$?
bad=0
while IFS= read -r line; do
  if [[ $line =~ ^\ \ ([^\ ]+)\ \.+\ (OK|FAIL)\ ?(.*)$ ]]; then
    name=${BASH_REMATCH[1]} verdict=${BASH_REMATCH[2]} reason=${BASH_REMATCH[3]}
    want=$(awk -v n="$name" '$1 == n {sub(/^[^ ]+[ \t]+/, ""); print; exit}' "$known" 2>/dev/null || true)
    if [ -n "$want" ]; then
      if [ "$verdict" = OK ]; then
        echo "${line%% OK*} OK (remove it from load-check-known.txt)"; bad=1; continue
      elif [[ $reason == *"$want"* ]]; then
        echo "${line%% FAIL*} KNOWN FAIL $reason"; continue
      fi
    fi
    [ "$verdict" = OK ] || bad=1
  fi
  [[ $line =~ ^exit\ status\ [0-9]+$ ]] || echo "$line"
done <<<"$out"
# a helper failure that printed no FAIL line (build error, panic) must still fail
if [ "$rc" != 0 ] && ! grep -q ' FAIL ' <<<"$out"; then bad=1; fi
exit "$bad"
