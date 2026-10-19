#!/usr/bin/env bash
# Partition every Go package exactly once, including newly added packages.
set -euo pipefail
shard=${1:?usage: ci_coverage_packages.sh SHARD}
case $shard in
  api|handlers|repository|migrations|core) ;;
  *) echo "Unknown coverage shard: $shard" >&2; exit 2 ;;
esac
packages=$(go list -tags=e2e ./...)
while IFS= read -r package; do
  case $package in
    */internal/handler/api) group=api ;;
    */internal/handler|*/internal/agentserver) group=handlers ;;
    */internal/repository) group=repository ;;
    */internal/migrate) group=migrations ;;
    *) group=core ;;
  esac
  if [[ $group == "$shard" ]]; then printf '%s\n' "$package"; fi
done <<< "$packages"
