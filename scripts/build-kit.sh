#!/usr/bin/env bash
# build-kit.sh - check a kit (scripts/check-kit.sh) and pack it into
# kits/<name>/build/<name>-<version>.kit.hmp, signed with a throwaway key no site
# trusts (core always signs a pack). For local tests; releases are signed with
# the first-party key by the release tooling. Re-checks the packed file.
#
# Usage: scripts/build-kit.sh kits/<name>      Env: HATAMOTO_BIN
set -euo pipefail
export LC_ALL=C.UTF-8

die() { echo "build-kit: $*" >&2; exit 1; }

[ $# -eq 1 ] || die "usage: scripts/build-kit.sh kits/<name>"
cd "$(git rev-parse --show-toplevel)"
dir=${1%/}
name=${dir#kits/}
export HATAMOTO_BIN=${HATAMOTO_BIN:-$(scripts/hatamoto-bin.sh)}

scripts/check-kit.sh "$dir"
version=$(jq -r .version "$dir/kit.json")
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
"$HATAMOTO_BIN" keys generate --dir "$tmp" --name throwaway >/dev/null
rm -rf "$dir/build"
mkdir -p "$dir/build"
out=$dir/build/$name-$version.kit.hmp
"$HATAMOTO_BIN" kit pack "$dir" --key "$tmp/throwaway.key" --out "$out" --expect-name "$name"
"$HATAMOTO_BIN" kit check "$out" --expect-name "$name"
echo "build-kit: $out (throwaway key)"
