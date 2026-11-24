#!/usr/bin/env bash
# hatamoto-bin.sh - print the path of a hatamoto binary built from the core
# commit pinned in .hatamoto-core (public repo, cached in ${XDG_CACHE_HOME:-~/.cache}/hatamoto-first-party/core-<sha>/,
# shared by every checkout).
# HATAMOTO_BIN, when set, wins: then it only prints that. Used by CI; handy on
# a laptop without a hatamoto on PATH: export HATAMOTO_BIN=$(scripts/hatamoto-bin.sh)
set -euo pipefail
if [ -n "${HATAMOTO_BIN:-}" ]; then echo "$HATAMOTO_BIN"; exit 0; fi
root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
sha=$(tr -d '[:space:]' < "$root/.hatamoto-core")
[[ $sha =~ ^[0-9a-f]{40}$ ]] || { echo "hatamoto-bin: .hatamoto-core must hold a 40 hex commit" >&2; exit 1; }
out=${XDG_CACHE_HOME:-$HOME/.cache}/hatamoto-first-party/core-$sha
if [ ! -x "$out/hatamoto" ]; then
  src=$(mktemp -d); trap 'rm -rf "$src"' EXIT
  git -C "$src" init -q
  git -C "$src" fetch -q --depth 1 https://github.com/Zen-Labs-Corp/hatamoto "$sha" >&2
  git -C "$src" checkout -q FETCH_HEAD
  mkdir -p "$out"
  ( cd "$src/backend" && GOWORK=off GOFLAGS= CGO_ENABLED=0 go build -trimpath -o "$out/hatamoto" ./cmd/hatamoto ) >&2
fi
echo "$out/hatamoto"
