#!/usr/bin/env bash
# Export and scan one AMD64 image before making it eligible for retention.
set -euo pipefail
private=$(mktemp -d)
trap 'rm -rf "$private"' EXIT
docker buildx create --use --driver docker-container \
  --driver-opt image=harbor.durp.info/dockerhub/moby/buildkit@sha256:cec9f139f45e93c5c69c60f8b07cfad9f43f4ef6b6a6cd917527fea5ff2e3dea
docker buildx inspect --bootstrap
docker buildx build --platform linux/amd64 --provenance=false --sbom=false \
  --build-arg BUILD_VERSION="${software_version:?}" \
  --build-arg BUILD_NUMBER="${build_number:?}" \
  --build-arg BUILD_COMMIT="${commit:?}" --build-arg BUILD_MODIFIED=false \
  --label "org.opencontainers.image.source=${CI_PROJECT_URL:?}" \
  --label "org.opencontainers.image.revision=$commit" \
  --output "type=oci,dest=$private/image.tar" .
mkdir -p "$private/layout"
tar -xf "$private/image.tar" -C "$private/layout"
make trivy-tools
bash scripts/container_scan.sh image "$private/layout" artifacts/image-evidence
jq -n --arg arch amd64 --arg commit "$commit" --arg version "$software_version" \
  --arg number "$build_number" --arg tree "$(git rev-parse HEAD^{tree})" \
  --arg head "${CI_MERGE_REQUEST_SOURCE_BRANCH_SHA:-${CI_COMMIT_SHA:?}}" \
  --arg base "${CI_MERGE_REQUEST_DIFF_BASE_SHA:-}" \
  --arg hash "$(sha256sum "$private/image.tar" | cut -d ' ' -f 1)" \
  --argjson pipeline "${CI_PIPELINE_ID:?}" --argjson job "${CI_JOB_ID:?}" \
  '{schema:2,arch:$arch,commit:$commit,version:$version,build_number:$number,
    tree:$tree,head:$head,base:$base,sha256:$hash,pipeline:$pipeline,image_job:$job}' \
  > "$private/source.json"
mkdir -p artifacts/built-image-amd64
mv "$private/image.tar" "$private/source.json" artifacts/built-image-amd64/
printf 'SCANNED_IMAGE_JOB_ID=%s\n' "$CI_JOB_ID" > artifacts/image.env
