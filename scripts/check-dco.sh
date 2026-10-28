#!/usr/bin/env bash
# check-dco.sh BASE HEAD - every non-merge commit in BASE..HEAD needs a
# "Signed-off-by: Name <email>" trailer whose email matches the commit's
# author email (case-insensitive). Run by .github/workflows/dco.yml; works
# locally too: scripts/check-dco.sh origin/main HEAD.
set -euo pipefail
base=${1:?usage: check-dco.sh BASE HEAD}; head=${2:?usage: check-dco.sh BASE HEAD}
git rev-parse --verify --quiet "$base^{commit}" >/dev/null || { echo "dco: unknown base $base" >&2; exit 2; }
git rev-parse --verify --quiet "$head^{commit}" >/dev/null || { echo "dco: unknown head $head" >&2; exit 2; }
fail=0 n=0
while read -r sha; do
  [[ -n "$sha" ]] || continue
  n=$((n + 1))
  author=$(git log -1 --format='%ae' "$sha" | tr '[:upper:]' '[:lower:]')
  subject=$(git log -1 --format='%s' "$sha")
  if git log -1 --format='%(trailers:key=Signed-off-by,valueonly)' "$sha" \
      | tr '[:upper:]' '[:lower:]' | grep -qF "<$author>"; then
    echo "ok   ${sha:0:12} $subject"
  else
    echo "FAIL ${sha:0:12} $subject: no Signed-off-by for the author <$author> (git commit -s; fix with git rebase --signoff)"
    fail=1
  fi
done < <(git rev-list --no-merges "$base..$head")
echo "dco: $n commit(s) checked"
exit $fail
