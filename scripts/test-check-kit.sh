#!/usr/bin/env bash
# test-check-kit.sh - prove scripts/check-kit.sh refuses broken kits: copies
# kits/hn and what it reads from plugins/hn into a throwaway repo, applies one
# mutation at a time and expects exit 1 with the named step failing.
#
# Usage: scripts/test-check-kit.sh      Env: HATAMOTO_BIN (else the pinned core is built once)
set -euo pipefail
export LC_ALL=C.UTF-8
root=$(git rev-parse --show-toplevel)
cd "$root"
export HATAMOTO_BIN=${HATAMOTO_BIN:-$(scripts/hatamoto-bin.sh)}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0
r=$tmp/r

fixture() { # a fresh throwaway repo at $r
  rm -rf "$r"
  mkdir -p "$r/scripts" "$r/plugins/hn" "$r/kits"
  cp scripts/check-kit.sh scripts/hatamoto-bin.sh "$r/scripts/"
  cp .hatamoto-core "$r/"
  cp -r plugins/hn/plugin.json plugins/hn/schemas plugins/hn/sections "$r/plugins/hn/"
  cp -r kits/hn "$r/kits/hn"
  rm -rf "$r/kits/hn/build"
  git -C "$r" init -q
}
mutate() { jq "$1" "$r/kits/hn/kit.json" >"$tmp/m" && mv "$tmp/m" "$r/kits/hn/kit.json"; }
expect_fail() { # <label> <regex the output must match>
  local out
  if out=$(cd "$r" && scripts/check-kit.sh kits/hn 2>&1); then
    echo "FAIL $1: the broken kit passed"; fails=$((fails + 1))
  elif ! grep -qE "$2" <<<"$out"; then
    echo "FAIL $1: no '$2' in:"; echo "$out"; fails=$((fails + 1))
  else
    echo "ok   $1"
  fi
}

fixture
(cd "$r" && scripts/check-kit.sh kits/hn >/dev/null) || { echo "FAIL baseline: the unmodified kit does not pass"; exit 1; }
echo "ok   baseline"

fixture; mutate '.name="other"'; expect_fail "name is not the folder name" 'is not the folder name'
fixture; mutate '.extra=1'; expect_fail "unknown key" 'FAIL  kit check'
fixture; mutate '.requires += ["comment.sigin"]'; expect_fail "unknown requires name" 'FAIL  kit check'
fixture; mutate '.schemas[0].schema.properties.kind.enum=["story"]'; expect_fail "schema drift" 'FAIL  schema parity'
fixture; mutate 'del(.schemas[0].schema.properties.kind)'; expect_fail "field set drift" 'FAIL  schema parity'
fixture; mutate '(.pages[] | select(.slug == "news").blocks[0].type) = "hn-feed"'; expect_fail "hyphenated block type" 'FAIL  sections'
fixture; mutate 'del(.pages[] | select(.slug == "reply"))'; expect_fail "missing page" 'FAIL  page parity'
fixture; mutate '.pages += [{"slug":"extra","name":"Extra","schema":"page","blocks":[{"type":"hn_feed","props":{}}]}]'; expect_fail "extra plugin page" 'FAIL  page parity'
fixture; mutate '.schemas[0].settings.comments.policy.max_depth = 33'; expect_fail "comment range" 'FAIL  comments'
fixture; mutate '.schemas[0].settings.comments.policy.max_depth = 1.5'; expect_fail "fractional comment number" 'FAIL  comments'
fixture; mutate '.schemas[0].settings.comments.order = ""'; (cd "$r" && scripts/check-kit.sh kits/hn >/dev/null) && echo "ok   empty comment order passes" || { echo "FAIL empty comment order was refused"; fails=$((fails + 1)); }
fixture; mutate '.menus[0].items[0].url = "/nowhere"'; expect_fail "menu target" 'FAIL  menu targets'
fixture; printf 'see /ho%s/someone/x\n' me >>"$r/kits/hn/README.md"; expect_fail "private path" 'FAIL  neutrality'
fixture; mkdir "$r/kits/hn/sections"; touch "$r/kits/hn/sections/x.html"; expect_fail "extra file" 'FAIL  files'
fixture; mutate '.plugins[0].grant = ["site.scripts"]'; expect_fail "care permission in a grant list" 'FAIL  grants'
fixture; sed -i 's/site[.]scripts/the script permission/g' "$r/kits/hn/README.md"; expect_fail "care permission not in the README" 'FAIL  grants'

((fails == 0)) || { echo "test-check-kit: $fails case(s) failed" >&2; exit 1; }
echo "test-check-kit: all cases passed"
