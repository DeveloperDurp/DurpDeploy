#!/usr/bin/env bash
# Publish one package to the GitLab registry and verify its downloaded bytes.
set -euo pipefail
archive=$(realpath "${1:?usage: publish_release_chart.sh ARCHIVE VERSION}")
version=${2:?}
private=$(mktemp -d "$(dirname "$archive")/.chart-publication.XXXXXXXX")
trap 'rm -rf "$private"' EXIT
repository=${CI_REGISTRY_IMAGE:?}/${CHART_REPO:?}
helm push "$archive" "oci://$repository"
helm pull "oci://$repository/durpdeploy" --version "$version" --destination "$private"
cmp "$archive" "$private/durpdeploy-$version.tgz"
