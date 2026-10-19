#!/usr/bin/env bash
# Keep each package in one timing-balanced unit-test shard.
set -euo pipefail
shard=${1:?usage: ci_test_packages.sh SHARD}
case $shard in
  repository|database|handlers|core) ;;
  *) echo "Unknown unit test shard: $shard" >&2; exit 2 ;;
esac
packages=$(go list ./...)
while IFS= read -r package; do
  case $package in
    */internal/repository) group=repository ;;
    */internal/migrate|*/cmd/server|*/internal/testutil) group=database ;;
    */internal/handler|*/internal/handler/api) group=handlers ;;
    *) group=core ;;
  esac
  if [[ $group == "$shard" ]]; then printf '%s\n' "$package"; fi
done <<< "$packages"
