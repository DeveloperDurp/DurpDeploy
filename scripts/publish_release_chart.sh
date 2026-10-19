#!/usr/bin/env bash
# Publish one package to both registries and verify its downloaded bytes.
set -euo pipefail
archive=$(realpath "${1:?usage: publish_release_chart.sh ARCHIVE VERSION}")
version=${2:?}
private=$(mktemp -d "$(dirname "$archive")/.chart-publication.XXXXXXXX")
trap 'rm -rf "$private"' EXIT
for repository in "${CI_REGISTRY_IMAGE:?}/${CHART_REPO:?}" \
  "ghcr.io/${REGISTRY_OWNER:?}/$CHART_REPO"; do
  helm push "$archive" "oci://$repository"
  download=$(mktemp -d "$private/chart.XXXXXXXX")
  helm pull "oci://$repository/durpdeploy" --version "$version" --destination "$download"
  cmp "$archive" "$download/durpdeploy-$version.tgz"
done
