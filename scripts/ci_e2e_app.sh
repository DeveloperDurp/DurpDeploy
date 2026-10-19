#!/usr/bin/env bash
# Reuse this pipeline's scanned application image and its static server binary.
set -euo pipefail
[[ ${GITLAB_CI:-} == true ]]
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
scratch=$(mktemp -d)
container=
cleanup() {
  if [[ -n $container ]]; then docker rm -v "$container" >/dev/null; fi
  rm -rf -- "$scratch"
}
trap cleanup EXIT
BUILD_VERSION=${software_version:?} BUILD_NUMBER=${build_number:?} \
  bash scripts/prepare_versioned_images.sh artifacts "$scratch/verified"
skopeo copy "oci-archive:$scratch/verified/amd64/image.tar" \
  "docker-archive:$scratch/app.tar:${DURPDEPLOY_E2E_IMAGE:?}"
docker load --input "$scratch/app.tar"
container=$(docker create "$DURPDEPLOY_E2E_IMAGE")
mkdir -p "$(dirname "${DURPDEPLOY_E2E_BINARY:?}")"
docker cp "$container:/usr/local/bin/durpdeploy" "$DURPDEPLOY_E2E_BINARY"
chmod 0755 "$DURPDEPLOY_E2E_BINARY"
"$DURPDEPLOY_E2E_BINARY" version
