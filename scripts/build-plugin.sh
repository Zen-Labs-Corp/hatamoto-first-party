#!/usr/bin/env bash
# build-plugin.sh <plugin-dir> - vet, test and build one plugin exactly as CI
# and the Hatamoto registry build do. Writes <plugin-dir>/build/ (plugin.wasm,
# plugin.json, and migrations/ ui/ assets/ when present).
#
# Checks: the folder name equals plugin.json "name" (it is the route prefix
# /p/<name>/), no go.work anywhere, no replace of hatamoto.dev/sdk, no
# local-path replace, gofmt clean, go vet, go test, *_test.js run with node
# when node is installed (CI always has it).
#
# Usage: scripts/build-plugin.sh plugins/<name> [--no-test]
# Env as CI: GOWORK=off GOFLAGS=-mod=readonly (set here when unset).
set -euo pipefail
export LC_ALL=C.UTF-8 GOWORK=off GOFLAGS=${GOFLAGS:--mod=readonly}

die() { echo "build-plugin: $*" >&2; exit 1; }
[ $# -ge 1 ] && [ $# -le 2 ] || die "usage: $0 plugins/<name> [--no-test]"
dir=${1%/}
test_it=1
[ "${2:-}" = --no-test ] && test_it=0

root=$(git rev-parse --show-toplevel)
[ -f "$dir/plugin.json" ] && [ ! -L "$dir/plugin.json" ] || die "$dir: no regular plugin.json"
[ -f "$dir/go.mod" ] || die "$dir: no go.mod"
name=$(basename "$dir")
[[ $name =~ ^[a-z][a-z0-9-]{1,39}$ ]] || die "$dir: folder name must match ^[a-z][a-z0-9-]{1,39}$"
pjname=$(jq -r '.name // empty' "$dir/plugin.json")
[ "$pjname" = "$name" ] || die "$dir: plugin.json name '$pjname' does not match the folder name '$name'"
version=$(jq -r '.version // empty' "$dir/plugin.json")
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]] || die "$dir: plugin.json version '$version' is not semver"

for d in "$root" "$dir"; do
  for w in go.work go.work.sum; do
    [ ! -e "$d/$w" ] && [ ! -L "$d/$w" ] || die "$w is not allowed in $d (the registry build refuses it)"
  done
done
gomod=$(cd "$dir" && go mod edit -json)
jq -e '[.Replace[]? | select(.Old.Path == "hatamoto.dev/sdk")] | length == 0' <<<"$gomod" >/dev/null \
  || die "$dir: go.mod replaces hatamoto.dev/sdk"
jq -e '[.Replace[]? | select(.New.Path | startswith(".") or startswith("/"))] | length == 0' <<<"$gomod" >/dev/null \
  || die "$dir: go.mod has a local-path replace"

cd "$dir"
bad=$(find . -name '*.go' -not -path './vendor/*' -not -path './build/*' -print0 | xargs -0 -r gofmt -l)
[ -z "$bad" ] || { echo "$bad" >&2; die "$dir: gofmt needed"; }
go vet ./...
if [ "$test_it" = 1 ]; then
  go test -count=1 ./...
  if command -v node >/dev/null; then
    while IFS= read -r -d '' t; do node "$t"; done < <(find . -name '*_test.js' -not -path './vendor/*' -not -path './build/*' -not -path './node_modules/*' -print0)
  fi
fi

rm -rf build && mkdir -p build
CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
cp plugin.json build/plugin.json
for d in migrations ui assets; do
  if [ -d "$d" ]; then cp -r "$d" "build/$d"; fi
done
[ -s build/plugin.wasm ] || die "$dir: empty build/plugin.wasm"
echo "build-plugin: OK $name $version ($(wc -c <build/plugin.wasm) bytes) -> $dir/build/"
