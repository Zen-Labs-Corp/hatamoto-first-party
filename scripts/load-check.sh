#!/usr/bin/env bash
# load-check.sh - load every plugin's build/ through the core loader at the commit pinned in
# .hatamoto-core (package parse, the asset read install and serve run, and the template
# registration and compile that follows), so a file or template the host refuses fails here. Run scripts/build-plugin.sh first. One line per plugin; exit 1 on any FAIL.
# Usage: scripts/load-check.sh [plugins/<name> ...]   (default: every plugins/*/)
# Env:   HATAMOTO_CORE_SRC  a core checkout, normally at the pin (skips the fetch); the helper is
#        copied in for the run and removed after, and a HEAD other than the pin is said in the header
# Env:   LOAD_CHECK_KNOWN  known-refusal list (default scripts/load-check-known.txt): `<plugin> <substring of one refusal>`
#        per line (several lines per plugin; a line without a reason is an error), `#` comments. A plugin prints KNOWN FAIL
#        and is not counted only when EVERY refusal matches a listed substring; one that fails for any other reason still fails, and one that now passes fails too (remove it from the list).
# Env:   LOAD_CHECK_HELPER_SRC  a Go file to run instead of scripts/loadcheck/main.go (tests only)
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
cp "${LOAD_CHECK_HELPER_SRC:-$root/scripts/loadcheck/main.go}" "$src/backend/cmd/fp-loadcheck/main.go"
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
# every list line is `<plugin> <reason substring>`; a plugin name alone would excuse any refusal
if [ -f "$known" ]; then
  awk 'NF && $1 !~ /^#/ && NF < 2 {print "load-check: " FILENAME ":" NR ": \"" $1 "\" has no reason" > "/dev/stderr"; bad=1} END {exit bad}' "$known" || exit 1
fi
rc=0
out=$(GOWORK=off GOFLAGS= go run ./cmd/fp-loadcheck "${args[@]}" 2>&1) || rc=$?
bad=0 verdicts=0
prefix='core would refuse templates: '
while IFS= read -r line; do
  if [[ $line =~ ^\ \ ([^\ ]+)\ \.+\ (OK|FAIL)\ ?(.*)$ ]]; then
    name=${BASH_REMATCH[1]} verdict=${BASH_REMATCH[2]} reason=${BASH_REMATCH[3]}
    verdicts=$((verdicts + 1))
    wants=()
    [ ! -f "$known" ] || mapfile -t wants < <(awk -v n="$name" 'NF >= 2 && $1 == n {sub(/^[^ \t]+[ \t]+/, ""); print}' "$known")
    if [ ${#wants[@]} -gt 0 ]; then
      if [ "$verdict" = OK ]; then
        echo "${line%% OK*} OK (remove it from load-check-known.txt)"; bad=1; continue
      fi
      # known only if EVERY refusal of the plugin matches a listed substring
      all=1
      IFS=$'\x1f' read -r -d '' -a items < <(printf '%s' "${reason#"$prefix"}") || true
      for item in "${items[@]}"; do
        hit=0
        for w in "${wants[@]}"; do [[ $item == *"$w"* ]] && hit=1; done
        [ $hit = 1 ] || all=0
      done
      if [ $all = 1 ] && [ ${#items[@]} -gt 0 ]; then
        echo "${line%% FAIL*} KNOWN FAIL ${reason//$'\x1f'/; }"; continue
      fi
    fi
    [ "$verdict" = OK ] || bad=1
    line=${line//$'\x1f'/; }
  fi
  # `go run` exits 1 for any child failure and prints `exit status N`; the helper itself only exits 0 or 1
  if [[ $line =~ ^exit\ status\ ([0-9]+)$ ]]; then
    [ "${BASH_REMATCH[1]}" = 1 ] || { echo "load-check: helper exited ${BASH_REMATCH[1]}" >&2; bad=1; }
  else
    echo "$line"
  fi
done <<<"$out"
# every plugin needs a verdict line, and the helper may only exit 0 or 1; anything else
# (build error, crash, kill) fails, even after some FAIL lines were printed
if [ "$verdicts" != "${#args[@]}" ]; then
  echo "load-check: $verdicts verdict(s) for ${#args[@]} plugin(s): the helper stopped early" >&2; bad=1
fi
if [ "$rc" != 0 ] && [ "$rc" != 1 ]; then echo "load-check: helper exited $rc" >&2; bad=1; fi
exit "$bad"
