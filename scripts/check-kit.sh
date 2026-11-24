#!/usr/bin/env bash
# check-kit.sh - check one kit (kits/<name>): core's own checker (the core
# pinned in .hatamoto-core), then what core cannot see offline: parity with the
# first-party plugins the kit names, page and menu targets, comment settings,
# permission grants, neutrality, and a pack round trip. One ok/FAIL line per
# step, all steps run; exit 1 on any FAIL.
#
# Usage: scripts/check-kit.sh kits/<name>
# Env: HATAMOTO_BIN        hatamoto binary (default: scripts/hatamoto-bin.sh builds the pinned core)
#      BUILTIN_SECTIONS    block types that need no plugin (default "content")
#      CARE_PERMISSIONS    permissions the review screen leaves to the owner's own tick
#                          (default below; ui.allow:* is always one)
set -euo pipefail
export LC_ALL=C.UTF-8

die() { echo "check-kit: $*" >&2; exit 1; }

[ $# -eq 1 ] || die "usage: scripts/check-kit.sh kits/<name>"
cd "$(git rev-parse --show-toplevel)"
dir=${1%/}
name=${dir#kits/}
[[ $dir == kits/* && $name =~ ^[a-z][a-z0-9-]{1,39}$ && -f $dir/kit.json ]] || die "$1: not kits/<name> with a kit.json"
k=$dir/kit.json
jq -e 'type == "object"' "$k" >/dev/null 2>&1 || die "$k: not a JSON object"
[ "$(jq -r '.name // ""' "$k")" = "$name" ] || die "$k: name $(jq -r '.name // ""' "$k") is not the folder name $name"
version=$(jq -r '.version // ""' "$k")
BIN=${HATAMOTO_BIN:-$(scripts/hatamoto-bin.sh)}
builtin=${BUILTIN_SECTIONS:-content}
care=${CARE_PERMISSIONS:-site.scripts builder.island http.fetch.any entities.filter client.key entities.write.own comments.moderate}

fails=0
ok()  { printf 'ok    %-14s %s\n' "$1" "$2"; }
bad() { printf 'FAIL  %-14s %s\n' "$1" "${2//$'\n'/$'\n'                      }"; fails=$((fails + 1)); }
# report <step> <problems> <ok text>: one line per step
report() { if [ -z "$2" ]; then ok "$1" "$3"; else bad "$1" "${2%$'\n'}"; fi; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# the kit's plugins that live in this repo
plugs=()
while read -r p; do
  [ -n "$p" ] && [ -f "plugins/$p/plugin.json" ] && plugs+=("$p")
done < <(jq -r '.plugins[]?.name' "$k")

# section types of a plugin, one per line
sections_of() { for f in "plugins/$1"/sections/*.html; do [ -f "$f" ] && basename "$f" .html; done; }
# a JSON array of every section type of the kit's repo plugins
all_sections=$(for p in "${plugs[@]}"; do sections_of "$p"; done | jq -R . | jq -cs .)

# 1. files: only what core allows in a kit directory
extra=$(git ls-files --cached --others --exclude-standard -- "$dir" | sed "s#^$dir/##" \
  | { grep -vxE 'kit\.json|README\.md|LICENSE|CHANGELOG\.md' || true; } | sed 's/^/unexpected file: /')
report files "$extra" "only kit.json and docs in $dir"

# 2. kit check: core's own checker
# a requires name core does not know can never be applied; "needs a newer Hatamoto" stays a note
if "$BIN" kit check "$dir" --expect-name "$name" >"$tmp/check" 2>&1 && ! grep -q 'which this Hatamoto does not know' "$tmp/check"; then
  ok "kit check" "$(head -1 "$tmp/check")"
  grep '^note:' "$tmp/check" | sed 's/^/                      /' || true
else
  bad "kit check" "$(cat "$tmp/check")"
fi

# 3. schema parity: the kit's schema is the plugin's schema file, same fields
probs=""
for p in "${plugs[@]}"; do
  s=$(jq -r '.entity_schema // empty' "plugins/$p/plugin.json")
  [ -n "$s" ] || continue
  f=plugins/$p/schemas/$s.json
  if [ ! -f "$f" ]; then probs+="$p: $f is missing"$'\n'; continue; fi
  if ! jq -e --arg s "$s" 'any(.schemas[]?; .slug == $s)' "$k" >/dev/null; then
    probs+="$p: kit has no schema $s"$'\n'; continue
  fi
  if ! diff -q <(jq -S --arg s "$s" '.schemas[] | select(.slug == $s) | .schema' "$k") <(jq -S . "$f") >/dev/null; then
    probs+="$p: kit schema $s differs from $f"$'\n'
  fi
  want=$(jq -c '(.entity_fields // []) | sort' "plugins/$p/plugin.json")
  have=$(jq -c --arg s "$s" '[.schemas[] | select(.slug == $s) | .schema.properties // {} | keys[]] | sort' "$k")
  [ "$want" = "$have" ] || probs+="$p: kit schema $s fields $have, plugin entity_fields $want"$'\n'
done
report "schema parity" "$probs" "${plugs[*]:-no plugin}: kit schema equals the plugin schema file"

# 4. sections: every block type is a built-in or a section of a kit plugin
probs=$(jq -r --argjson known "$all_sections" --arg builtin "$builtin" '
  ($builtin | split(" ")) as $b
  | .pages[]? | .slug as $s | .blocks[]?.type
  | select(. as $t | ($b + $known) | index($t) | not)
  | "page \($s): block type \(.) is no section of a kit plugin or built-in"' "$k")
report sections "$probs" "every block type is a plugin section or built-in"

# 5. page parity: one kit page per plugin page path, and no stray plugin page
probs=""
for p in "${plugs[@]}"; do
  psec=$(sections_of "$p" | jq -R . | jq -cs .)
  out=$(jq -r --slurpfile m "plugins/$p/plugin.json" --argjson psec "$psec" --arg p "$p" '
    ($m[0].page_paths // []) as $paths
    | (.pages // []) as $pages
    | def has_sec: any(.blocks[]?; .type as $t | $psec | index($t) != null);
      ($paths[] | select(. != "/") | . as $path | ltrimstr("/") as $slug
       | select(any($pages[]; .slug == $slug and has_sec) | not)
       | "\($p): page path \($path) has no kit page \($slug) with a \($p) section"),
      ($pages[] | select(has_sec) | "/\(.slug)" as $path
       | select($paths | index($path) | not)
       | "\($p): kit page \(.slug) carries a \($p) section but \($path) is not in its page_paths")' "$k")
  [ -z "$out" ] || probs+="$out"$'\n'
done
report "page parity" "${probs%$'\n'}" "every page path (except /) has a kit page, no stray plugin page"

# 6. comments: ranges core checks only at apply
probs=$(jq -r '
  .schemas[]? | select(.settings.comments != null) | .slug as $s | .settings.comments as $c
  | (if ($c.order // "") != "" and (["newest","oldest","rank"] | index($c.order) | not)
       then "schema \($s): comments order \($c.order) is not newest, oldest or rank" else empty end),
    ($c.policy // {}) as $p
  | (if $p.max_depth != null and (($p.max_depth | type) != "number" or ($p.max_depth | floor) != $p.max_depth or $p.max_depth < 1 or $p.max_depth > 32)
       then "schema \($s): policy max_depth \($p.max_depth) is not 1-32" else empty end),
    (if $p.edit_window_minutes != null and (($p.edit_window_minutes | type) != "number" or ($p.edit_window_minutes | floor) != $p.edit_window_minutes or $p.edit_window_minutes < 1 or $p.edit_window_minutes > 1440)
       then "schema \($s): policy edit_window_minutes \($p.edit_window_minutes) is not 1-1440" else empty end),
    (if $p.slow_mode_seconds != null and (($p.slow_mode_seconds | type) != "number" or ($p.slow_mode_seconds | floor) != $p.slow_mode_seconds or $p.slow_mode_seconds < 0 or $p.slow_mode_seconds > 86400)
       then "schema \($s): policy slow_mode_seconds \($p.slow_mode_seconds) is not 0-86400" else empty end)' "$k")
report comments "$probs" "comment settings in range"

# 7. menu targets: a kit page, a kit plugin page path or a path prefix of one
paths='[]' prefixes='[]'
for p in "${plugs[@]}"; do
  paths=$(jq -c --slurpfile m "plugins/$p/plugin.json" '. + ($m[0].page_paths // [])' <<<"$paths")
  prefixes=$(jq -c --slurpfile m "plugins/$p/plugin.json" '. + ($m[0].page_path_prefixes // [])' <<<"$prefixes")
done
probs=$(jq -r --argjson paths "$paths" --argjson prefixes "$prefixes" '
  [.pages[]? | "/" + .slug] as $pages
  | .menus[]? | .name as $m | .items[]? | .url as $u
  | select(($pages | index($u)) == null and ($paths | index($u)) == null
           and (any($prefixes[]; . as $x | $u | startswith($x)) | not))
  | "menu \($m): \($u) is no kit page, plugin page path or plugin prefix"' "$k")
report "menu targets" "$probs" "every menu url is a kit page or a plugin page path"

# 8. grants: care permissions are the owner's own ticks, never in a grant list
probs=$(jq -r --arg care "$care" '
  ($care | split(" ")) as $c
  | .plugins[]? | select((.grant | type) == "array") | .name as $n
  | .grant[] | select(. as $g | ($c | index($g)) != null or ($g | startswith("ui.allow:")))
  | "plugin \($n): grant lists \(.), which only the owner can tick"' "$k")
notes=""
for p in "${plugs[@]}"; do
  for perm in $(jq -r '.permissions[]? | select(. != "")' "plugins/$p/plugin.json"); do
    if [[ " $care " == *" $perm "* || $perm == ui.allow:* ]]; then
      grep -qF "$perm" "$dir/README.md" 2>/dev/null || probs+="plugin $p declares $perm but $dir/README.md does not name it as the owner's own tick"$'\n'
      notes+="$p $perm "
    fi
  done
done
report grants "${probs%$'\n'}" "no care permission in a grant list; owner ticks: ${notes:-none}"
[ -z "$notes" ] || echo "                      note: the review screen's reason text for these comes from core, not from the plugin manifest"

# 9. neutrality: the generic public bans (the private list stays in the gate)
mail=$(printf '%s|' gmail googlemail yahoo hotmail outlook icloud 'proton(mail)?')
ban="/ho[m]e/[a-z]|/Users/[A-Za-z]|Goland[P]rojects|[.]pi/|@(${mail%|})[.]"
mapfile -t files < <(git ls-files --cached --others --exclude-standard -- "$dir")
hits=""
if [ ${#files[@]} -gt 0 ]; then
  hits=$({ grep -HnIiE "$ban" "${files[@]}" || true; } | cut -d: -f1,2 | sed 's/^/ban match at /')
fi
report neutrality "$hits" "no private path or address in $dir"

# 10. pack: signed with a throwaway key no site trusts (core always signs a pack)
if "$BIN" keys generate --dir "$tmp" --name throwaway >/dev/null 2>"$tmp/pack" \
  && "$BIN" kit pack "$dir" --key "$tmp/throwaway.key" --out "$tmp/$name-$version.kit.hmp" --expect-name "$name" >/dev/null 2>>"$tmp/pack" \
  && "$BIN" kit check "$tmp/$name-$version.kit.hmp" --expect-name "$name" >/dev/null 2>>"$tmp/pack"; then
  ok pack "$name-$version.kit.hmp (throwaway key, not trusted by any site) packs and re-checks"
else
  bad pack "$(cat "$tmp/pack")"
fi

((fails == 0)) || die "kit $name $version: $fails problem(s)"
echo "kit $name $version: all checks passed"
