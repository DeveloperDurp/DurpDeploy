#!/usr/bin/env bash
# Exercise current-pipeline image verification with real Git and OCI archives.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git init -q --initial-branch=main "$work/repo"
cd "$work/repo"
git config user.email fixture@example.invalid
git config user.name Fixture
printf 'merged source\n' > source
git add source
git commit -qm 'main without an associated MR'
export CI_COMMIT_SHA CI_DEFAULT_BRANCH=main
export CI_PIPELINE_ID=99 SCANNED_IMAGE_JOB_ID=111
export BUILD_VERSION=v1.2.3+build.4 BUILD_NUMBER=742
CI_COMMIT_SHA=$(git rev-parse HEAD)
first_target=$CI_COMMIT_SHA
tree=$(git rev-parse HEAD^{tree})
layout="$work/layout"
mkdir -p "$layout/blobs/sha256" "$work/versioned/built-image-amd64"
jq -nc --arg commit "$CI_COMMIT_SHA" '
  {architecture:"amd64",os:"linux",config:{Labels:{
    "org.opencontainers.image.version":"v1.2.3+build.4",
    "org.opencontainers.image.revision":$commit,"info.durpdeploy.build-number":"742"}},
    rootfs:{type:"layers",diff_ids:[]}}
' > "$work/config"
config_hash=$(sha256sum "$work/config" | cut -d ' ' -f 1)
cp "$work/config" "$layout/blobs/sha256/$config_hash"
jq -nc --arg digest "sha256:$config_hash" --argjson size "$(wc -c < "$work/config")" '
  {schemaVersion:2,mediaType:"application/vnd.oci.image.manifest.v1+json",
    config:{mediaType:"application/vnd.oci.image.config.v1+json",digest:$digest,size:$size},layers:[]}
' > "$work/manifest"
manifest_hash=$(sha256sum "$work/manifest" | cut -d ' ' -f 1)
cp "$work/manifest" "$layout/blobs/sha256/$manifest_hash"
jq -nc --arg digest "sha256:$manifest_hash" --argjson size "$(wc -c < "$work/manifest")" '
  {schemaVersion:2,manifests:[{mediaType:"application/vnd.oci.image.manifest.v1+json",
    digest:$digest,size:$size,platform:{architecture:"amd64",os:"linux"}}]}
' > "$layout/index.json"
printf '%s\n' '{"imageLayoutVersion":"1.0.0"}' > "$layout/oci-layout"
tar -cf "$work/versioned/built-image-amd64/image.tar" -C "$layout" .
hash=$(sha256sum "$work/versioned/built-image-amd64/image.tar" | cut -d ' ' -f 1)
jq -n --arg tree "$tree" --arg commit "$CI_COMMIT_SHA" --arg hash "$hash" '
  {schema:2,arch:"amd64",tree:$tree,commit:$commit,version:"v1.2.3+build.4",
    build_number:"742",sha256:$hash,pipeline:99,image_job:111}
' > "$work/versioned-source.json"
reset_source() {
  cp "$work/versioned-source.json" "$work/versioned/built-image-amd64/source.json"
}
prepare_versioned() {
  bash "$root/scripts/prepare_versioned_images.sh" "$work/versioned" "$work/verified"
}
expect_rejection() {
  if prepare_versioned > "$work/rejection.log" 2>&1; then
    printf 'Unexpected archive acceptance: %s\n' "$1" >&2
    exit 1
  fi
  printf 'PASS: reject %s\n' "$1"
  reset_source
}
reset_source
prepare_versioned
cmp "$work/versioned/built-image-amd64/image.tar" "$work/verified/amd64/image.tar"
echo 'PASS: verify the current main pipeline image without MR metadata or GitLab API access'
for mutation in '.pipeline=98' '.image_job=110' '.tree="wrong"' '.commit=("0"*40)' \
  '.version="v9.9.9"' '.build_number="743"' '.sha256=("0"*64)' '.arch="arm64"' '.schema=1'; do
  jq "$mutation" "$work/versioned-source.json" > "$work/versioned/built-image-amd64/source.json"
  expect_rejection "mismatched image metadata: $mutation"
done
cp "$work/versioned/built-image-amd64/image.tar" "$work/image.tar"
printf 'corruption' >> "$work/versioned/built-image-amd64/image.tar"
expect_rejection 'archive digest mismatch'
mv "$work/image.tar" "$work/versioned/built-image-amd64/image.tar"
mv "$work/versioned/built-image-amd64/source.json" "$work/source.json"
expect_rejection 'missing source metadata'
mv "$work/source.json" "$work/versioned/built-image-amd64/source.json"

printf 'newer main\n' >> source
git commit -qam 'another main commit'
CI_COMMIT_SHA=$(git rev-parse HEAD)
expect_rejection 'image from an earlier main commit'
CI_COMMIT_SHA=$first_target
prepare_versioned
echo 'Release image boundary tests: PASS'

# Exercise the publication entry point against local registries with this run's artifacts.
ln -s "$root/scripts" scripts
ln -s "$work/versioned" artifacts
CI_COMMIT_SHA=$(git rev-parse HEAD)
bash "$root/scripts/release_publish_test.sh" "$work" "$first_target"
