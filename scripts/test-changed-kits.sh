#!/usr/bin/env bash
# test-changed-kits.sh - prove scripts/changed-kits.sh exits 0 and lists the
# right kits for every shape of diff, in a throwaway git repo.
#
# Usage: scripts/test-changed-kits.sh
set -euo pipefail
export LC_ALL=C.UTF-8
root=$(git rev-parse --show-toplevel)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
r=$tmp/r
fails=0

mkdir -p "$r/scripts" "$r/plugins/a" "$r/plugins/b" "$r/plugins/c" "$r/kits/x" "$r/kits/y"
cp "$root/scripts/changed-kits.sh" "$r/scripts/"
echo '{"plugins":[{"name":"b"}]}' >"$r/kits/x/kit.json"
echo '{"plugins":[{"name":"a"}]}' >"$r/kits/y/kit.json"
for p in a b c; do echo 1 >"$r/plugins/$p/f"; done
git -C "$r" init -q
git -C "$r" add -A
git -C "$r" -c user.name=t -c user.email=t@example.com commit -q -m base
git -C "$r" tag base

# case <label> <expected json> <change command run in the repo>
case_() {
  local label=$1 want=$2 got rc=0
  shift 2
  git -C "$r" reset -q --hard base
  (cd "$r" && "$@")
  git -C "$r" add -A
  git -C "$r" -c user.name=t -c user.email=t@example.com commit -q --allow-empty -m change
  got=$(cd "$r" && scripts/changed-kits.sh --json HEAD~1 HEAD 2>&1) || rc=$?
  if [ "$rc" != 0 ] || [ "$got" != "$want" ]; then
    echo "FAIL $label: exit $rc, got '$got', want '$want'"; fails=$((fails + 1))
  else
    echo "ok   $label"
  fi
}

case_ "unnamed plugin only" '[]' sh -c 'echo 2 >plugins/c/f'
case_ "plugin named by the first kit only" '["kits/x"]' sh -c 'echo 2 >plugins/b/f'
case_ "plugin named by the last kit only" '["kits/y"]' sh -c 'echo 2 >plugins/a/f'
case_ "kit deletion" '["kits/x"]' sh -c 'rm -r kits/y; echo 2 >kits/x/README.md'
case_ "last kit deleted" '[]' sh -c 'rm -r kits/y'
case_ "kits only" '["kits/x"]' sh -c 'echo 2 >kits/x/README.md'
case_ "scripts only" '["kits/x","kits/y"]' sh -c 'echo "# c" >>scripts/changed-kits.sh'
case_ "empty diff" '[]' true

((fails == 0)) || { echo "test-changed-kits: $fails case(s) failed" >&2; exit 1; }
echo "test-changed-kits: all cases passed"
