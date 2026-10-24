#!/usr/bin/env bash
# Split the compiled API test inventory without dropping new tests or examples.
set -euo pipefail
shard=${1:?usage: ci_coverage_tests.sh 1|2}
case $shard in
  1|2) ;;
  *) echo "Unknown API coverage shard: $shard" >&2; exit 2 ;;
esac
inventory=$(bash scripts/go_test.sh -tags=e2e -covermode=atomic -coverpkg=./... \
  -list . ./internal/handler/api)
tests=$(printf '%s\n' "$inventory" | sed -n '/^\(Test\|Example\|Fuzz\)[^[:space:]]*$/p' | LC_ALL=C sort)
[[ -n $tests ]] || { echo 'No API coverage tests found.' >&2; exit 1; }
[[ -z $(printf '%s\n' "$tests" | uniq -d) ]] || {
  echo 'Duplicate API test names cannot be sharded safely.' >&2
  exit 1
}
selected=$(printf '%s\n' "$tests" | awk -v shard="$shard" '(NR - 1) % 2 == shard - 1')
[[ -n $selected ]] || { echo 'API coverage shard is empty.' >&2; exit 1; }
printf '%s\n' "$selected"
