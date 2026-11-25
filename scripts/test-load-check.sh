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

fixture outsidedef
printf '{{define "plugin/outsidedef/ok_name"}}ok{{end}}{{define "head"}}x{{end}}\n' >"$tmp/plugins/outsidedef/build/partials/ok_name.html"
check "partial defining a name outside plugin/<name>/" 1 'outsidedef \.+ FAIL core would refuse templates: .*defines "head"' "$tmp/plugins/outsidedef"

fixture brokentpl
printf '{{define "plugin/brokentpl/ok_name"}}{{if}}{{end}}\n' >"$tmp/plugins/brokentpl/build/partials/ok_name.html"
check "partial that does not parse" 1 'brokentpl \.+ FAIL core would refuse templates: .*missing value for if' "$tmp/plugins/brokentpl"

fixture foreigncall
printf '{{define "plugin/foreigncall/ok_name"}}{{template "plugin/good/ok_name"}}{{end}}\n' >"$tmp/plugins/foreigncall/build/partials/ok_name.html"
check "partial calling another plugin" 1 'foreigncall \.+ FAIL core would refuse templates: .*another plugin' "$tmp/plugins/foreigncall" "$tmp/plugins/good"

fixture scriptmark
jq '.permissions=["site.scripts"]' "$tmp/plugins/scriptmark/build/plugin.json" >"$tmp/m.json" && mv "$tmp/m.json" "$tmp/plugins/scriptmark/build/plugin.json"
printf '{{define "section-fx_block"}}<button onclick="x()">go</button>{{end}}\n' >"$tmp/plugins/scriptmark/build/sections/fx_block.html"
check "inline handler in a section of a plugin that asks for site.scripts (no grant on a fresh install)" 1 'scriptmark \.+ FAIL core would refuse templates: .*site\.scripts' "$tmp/plugins/scriptmark"

# a wasm with one export, func_x (type () -> (), one empty function)
wasm_x='\0asm\1\0\0\0\1\4\1\140\0\0\3\2\1\0\7\12\1\6func_x\0\0\12\4\1\2\0\13'
funcfixture() { # <name> <declared func>: a section that calls plugin_<name>_<func>
  fixture "$1"
  jq --arg f "$2" '.funcs=[$f]' "$tmp/plugins/$1/build/plugin.json" >"$tmp/m.json" && mv "$tmp/m.json" "$tmp/plugins/$1/build/plugin.json"
  printf "$wasm_x" >"$tmp/plugins/$1/build/plugin.wasm"
  printf '{{define "section-fx_block"}}{{plugin_%s_%s}}{{end}}\n' "$1" "$2" >"$tmp/plugins/$1/build/sections/fx_block.html"
}
funcfixture hasexport x
check "template func with a wasm export loads" 0 'hasexport \.+ OK' "$tmp/plugins/hasexport"
funcfixture noexport y
check "template func without a wasm export (host skips it, the call then fails to parse)" 1 'noexport \.+ FAIL core would refuse templates' "$tmp/plugins/noexport"

fixture deepranges
printf '{{define "section-fx_block"}}{{range .Content.a}}{{template "plugin/deepranges/p" .}}{{end}}{{end}}\n' >"$tmp/plugins/deepranges/build/sections/fx_block.html"
printf '{{define "plugin/deepranges/p"}}{{range .b}}{{range $.Content.c}}x{{end}}{{end}}{{end}}\n' >"$tmp/plugins/deepranges/build/partials/ok_name.html"
check "3 nested ranges across a called template" 1 'deepranges \.+ FAIL core would refuse templates: .*nests ranges deeper' "$tmp/plugins/deepranges"

mkdir -p "$tmp/plugins/nobuild"
check "no build/" 1 'FAIL no build/' "$tmp/plugins/nobuild"

check "one FAIL does not stop the others" 1 '^  good \.+ OK' "$tmp/plugins/good" "$tmp/plugins/bad-name"

# known-refusal list (a temp list, never the real file)
known=$tmp/known.txt
printf '# c\nscriptmark site.scripts\n' >"$known"
LOAD_CHECK_KNOWN=$known check "known: listed + matching reason is KNOWN FAIL, exit 0" 0 'scriptmark \.+ KNOWN FAIL core would refuse' "$tmp/plugins/scriptmark"
printf 'scriptmark some other reason\n' >"$known"
LOAD_CHECK_KNOWN=$known check "known: listed + different reason still fails" 1 'scriptmark \.+ FAIL core would refuse' "$tmp/plugins/scriptmark"
printf 'good whatever\n' >"$known"
LOAD_CHECK_KNOWN=$known check "known: listed + now passing fails" 1 'good \.+ OK \(remove it from load-check-known\.txt\)' "$tmp/plugins/good"

printf 'scriptmark site.scripts\n' >"$known"
printf '{{define "section-fx_block"}}<button onclick="x()">go</button>{{end}}{{define "plugin/scriptmark/ok_name"}}{{if}}{{end}}\n' >"$tmp/plugins/scriptmark/build/partials/ok_name.html"
LOAD_CHECK_KNOWN=$known check "known: the listed reason plus one more refusal still fails" 1 'scriptmark \.+ FAIL core would refuse' "$tmp/plugins/scriptmark"
printf 'scriptmark\n' >"$known"
LOAD_CHECK_KNOWN=$known check "known: an entry without a reason is a config error" 1 'has no reason' "$tmp/plugins/scriptmark"
rm "$tmp/plugins/scriptmark/build/partials/ok_name.html"
printf '{{define "plugin/scriptmark/ok_name"}}ok{{end}}\n' >"$tmp/plugins/scriptmark/build/partials/ok_name.html"

# the helper stopping early: a fake helper (LOAD_CHECK_HELPER_SRC) that prints one verdict then dies
printf 'package main\nimport ("fmt";"os")\nfunc main() { fmt.Println("  good ... FAIL x"); os.Exit(%s) }\n' 1 >"$tmp/h1.go"
printf 'package main\nimport ("fmt";"os")\nfunc main() { fmt.Println("  good ... FAIL x"); os.Exit(%s) }\n' 137 >"$tmp/h2.go"
LOAD_CHECK_HELPER_SRC=$tmp/h1.go check "helper prints a verdict for one of two plugins" 1 'stopped early' "$tmp/plugins/good" "$tmp/plugins/bad-name"
LOAD_CHECK_HELPER_SRC=$tmp/h2.go check "helper exits with a code other than 0/1" 1 'helper exited 137' "$tmp/plugins/good"

# core panics on a same-file define that calls a cross-file partial (the old hn shape);
# this case flips when core fixes the panic
fixture panics
printf '{{define "plugin/panics/aa"}}{{template "plugin/panics/ok_name"}}{{end}}{{define "section-fx_block"}}{{template "plugin/panics/aa"}}{{end}}\n' >"$tmp/plugins/panics/build/sections/fx_block.html"
check "core panic on the templates is a FAIL" 1 'panics \.+ FAIL core panics on the templates' "$tmp/plugins/panics"

((fails == 0)) || { echo "test-load-check: $fails case(s) failed" >&2; exit 1; }
echo "test-load-check: all cases passed"
