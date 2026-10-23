#!/usr/bin/env bash
# Build one pipeline-specific tool image in the existing GitLab registry.
set -euo pipefail
[[ ${GITLAB_CI:-} == true ]]
ready=false
for _ in {1..60}; do
  if docker info >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
[[ $ready == true ]] || { echo 'CI Docker daemon did not become ready.' >&2; exit 1; }
printf '%s' "${CI_REGISTRY_PASSWORD:?}" |
  docker login "${CI_REGISTRY:?}" -u "${CI_REGISTRY_USER:?}" --password-stdin
private=$(mktemp -d)
trap 'docker logout "$CI_REGISTRY" >/dev/null; rm -rf -- "$private"' EXIT
docker buildx create --use --driver docker-container \
  --driver-opt image=harbor.durp.info/dockerhub/moby/buildkit@sha256:cec9f139f45e93c5c69c60f8b07cfad9f43f4ef6b6a6cd917527fea5ff2e3dea
docker buildx inspect --bootstrap

cache_scope=
if [[ ${CI_PIPELINE_SOURCE:-} == merge_request_event && \
      ${CI_MERGE_REQUEST_SOURCE_PROJECT_ID:-} == "${CI_PROJECT_ID:?}" && \
      ${CI_COMMIT_REF_PROTECTED:-false} != true ]]; then
  [[ ${CI_MERGE_REQUEST_IID:-} =~ ^[1-9][0-9]*$ ]]
  cache_scope="${CI_REGISTRY_IMAGE:?}/ci-cache/mr-$CI_MERGE_REQUEST_IID"
fi

build_image() {
  local name=$1 file=$2 repository=$3 started=$SECONDS digest
  shift 3
  local -a cache=()
  if [[ -n $cache_scope ]]; then
    cache=(--cache-from "type=registry,ref=$cache_scope:$name"
      --cache-to "type=registry,ref=$cache_scope:$name,mode=min")
  fi
  docker buildx build --pull --platform linux/amd64 --push \
    --provenance=false --sbom=false "${cache[@]}" \
    --metadata-file "$private/$name.json" \
    -f "$file" -t "$repository:pipeline-${CI_PIPELINE_ID:?}" "$@" .
  digest=$(jq -er '."containerimage.digest"' "$private/$name.json")
  [[ $digest =~ ^sha256:[0-9a-f]{64}$ ]]
  printf '%s@%s\n' "$repository" "$digest" > "$private/$name.digest"
  printf 'CI_TIMING phase=%s seconds=%d\n' "$name" "$((SECONDS - started))"
}

browser_started=$SECONDS
build_image browser Dockerfile.mobile-browser "${CI_REGISTRY_IMAGE:?}/ci-browser"
browser_seconds=$((SECONDS - browser_started))
read -r browser_digest < "$private/browser.digest"
runtime_started=$SECONDS
build_image runtime Dockerfile.ci "$CI_REGISTRY_IMAGE/ci" --build-arg "BROWSER_IMAGE=$browser_digest"
runtime_seconds=$((SECONDS - runtime_started))
read -r digest < "$private/runtime.digest"
mkdir -p artifacts
printf 'CI_RUNTIME_IMAGE=%s\n' "$digest" > artifacts/runtime.env
printf '{"browser_seconds":%d,"runtime_seconds":%d,"cache_scope":"%s"}\n' \
  "$browser_seconds" "$runtime_seconds" "${cache_scope:+merge_request}" > artifacts/runtime-timing.json
