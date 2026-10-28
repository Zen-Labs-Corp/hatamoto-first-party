#!/usr/bin/env bash
# new-plugin.sh <name> "<one-line description>" - create plugins/<name> from
# template/. The name is the plugin's route prefix (/p/<name>/) and its
# registry name: ^[a-z][a-z0-9-]{1,39}$, not reserved (hello, webhooks), and
# not already a folder here. Builds the new plugin once to prove it works.
set -euo pipefail
export LC_ALL=C.UTF-8
die() { echo "new-plugin: $*" >&2; exit 1; }
[ $# -eq 2 ] || die "usage: $0 <name> \"<one-line description>\""
name=$1 desc=$2
[[ $name =~ ^[a-z][a-z0-9-]{1,39}$ ]] || die "name must match ^[a-z][a-z0-9-]{1,39}$"
case "$name" in hello|webhooks) die "name \"$name\" is reserved" ;; esac
[ -n "$desc" ] && [ ${#desc} -le 200 ] || die "description must be 1-200 characters"
[[ $desc != *$'\n'* && $desc != *'"'* && $desc != *'\'* && $desc != *'|'* && $desc != *'&'* ]] \
  || die "description must be one line without \" \\ | &"
root=$(git rev-parse --show-toplevel)
cd "$root"
dst=plugins/$name
[ ! -e "$dst" ] || die "$dst already exists"
mkdir -p "$dst"
cp -r template/. "$dst/"
find "$dst" -type f -print0 | xargs -0 sed -i -e "s|__NAME__|$name|g" -e "s|__DESCRIPTION__|$desc|g"
! grep -rq '__NAME__\|__DESCRIPTION__' "$dst" || die "placeholder left in $dst"
scripts/build-plugin.sh "$dst"
echo "new-plugin: created $dst"
