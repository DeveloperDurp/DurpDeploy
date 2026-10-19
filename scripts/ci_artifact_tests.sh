#!/usr/bin/env bash
# Partition the compiled artifact-gate test inventory without dropping new tests.
set -euo pipefail
shard=${1:?usage: ci_artifact_tests.sh 1|2}
case $shard in
  1|2) ;;
  *) echo "Unknown artifact test shard: $shard" >&2; exit 2 ;;
esac
inventory=$(bash scripts/go_test.sh -tags=e2e -race \
  -list '^TestArtifactGate.*E2E$' ./internal/handler/api)
tests=$(printf '%s\n' "$inventory" | sed -n '/^TestArtifactGate.*E2E$/p' | LC_ALL=C sort)
[[ -n $tests ]] || { echo 'No artifact gate tests found.' >&2; exit 1; }
selected=$(printf '%s\n' "$tests" | awk -v shard="$shard" '(NR - 1) % 2 == shard - 1')
[[ -n $selected ]] || { echo 'Artifact test shard is empty.' >&2; exit 1; }
printf '%s\n' "$selected"
