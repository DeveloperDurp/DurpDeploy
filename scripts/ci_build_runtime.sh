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
trusted_main=false
if [[ ${CI_COMMIT_REF_PROTECTED:-false} == true && \
      -n ${CI_COMMIT_BRANCH:-} && ${CI_COMMIT_BRANCH:-} == "${CI_DEFAULT_BRANCH:-}" && \
      ${CI_PIPELINE_SOURCE:-} =~ ^(push|web|schedule)$ ]]; then
  trusted_main=true
fi
if [[ ${CI_PIPELINE_SOURCE:-} == merge_request_event && \
      ${CI_MERGE_REQUEST_SOURCE_PROJECT_ID:-} == "${CI_PROJECT_ID:?}" && \
      ${CI_COMMIT_REF_PROTECTED:-false} != true ]]; then
  [[ ${CI_MERGE_REQUEST_IID:-} =~ ^[1-9][0-9]*$ ]]
  cache_scope="${CI_REGISTRY_IMAGE:?}/ci-cache/mr-$CI_MERGE_REQUEST_IID"
fi

# A weekly build input refreshes package installations even on busy branches.
cache_epoch=$(($(date +%s) / 604800))
refresh=false
if [[ ${CI_PIPELINE_SOURCE:-} == schedule || ${CI_RUNTIME_REFRESH:-false} == true ]]; then
  refresh=true
fi
trusted_cache=false
fetch_cache() {
  local endpoint=$1 destination=$2
  printf 'CI_RUNTIME_CACHE lookup=%s\n' "$endpoint"
  curl --fail --silent --show-error --max-time 30 --max-filesize 262144 \
    --proto '=https' --header "JOB-TOKEN: ${CI_JOB_TOKEN:?}" \
    "${CI_API_V4_URL:?}/projects/$CI_PROJECT_ID/$endpoint" -o "$destination"
}
if [[ ( $trusted_main == true || -n $cache_scope ) && $refresh == false ]]; then
  branch=$(jq -rn --arg branch "${CI_DEFAULT_BRANCH:?}" '$branch | @uri')
  # Resolve a branch pipeline and its job through GitLab, not artifact claims.
  # A tag named like the default branch must not supply trusted metadata.
  if fetch_cache "pipelines?ref=$branch&scope=branches&status=success&source=push&per_page=1" "$private/pipelines.json" && \
      pipeline=$(jq -er --arg ref "$CI_DEFAULT_BRANCH" --argjson project "$CI_PROJECT_ID" '
        .[0] | select(.ref == $ref and .project_id == $project and .status == "success" and .source == "push") |
        .id | select(type == "number" and . > 0 and . == floor)
      ' "$private/pipelines.json") && \
      fetch_cache "pipelines/$pipeline/jobs?per_page=100" "$private/jobs.json" && \
      cache_job=$(jq -er --arg ref "$CI_DEFAULT_BRANCH" --argjson pipeline "$pipeline" '
        [.[] | select(.name == "ci-runtime" and .status == "success" and
          .ref == $ref and .tag == false and .pipeline.id == $pipeline)] |
        max_by(.id).id | select(type == "number" and . > 0 and . == floor)
      ' "$private/jobs.json") && \
      fetch_cache "jobs/$cache_job/artifacts/artifacts/runtime-cache.json" "$private/trusted-cache.json" && \
      jq -e --arg project "$CI_PROJECT_ID" --arg branch "$CI_DEFAULT_BRANCH" \
        --arg registry "$CI_REGISTRY_IMAGE" --arg pipeline "$pipeline" --argjson epoch "$cache_epoch" '
        .project_id == $project and .ref == $branch and .protected == true and
        .cache_epoch == $epoch and .pipeline_id == $pipeline and
        (.browser | startswith($registry + "/ci-browser@sha256:") and
          (ltrimstr($registry + "/ci-browser@sha256:") | test("^[0-9a-f]{64}$"))) and
        (.runtime | startswith($registry + "/ci@sha256:") and
          (ltrimstr($registry + "/ci@sha256:") | test("^[0-9a-f]{64}$")))
      ' "$private/trusted-cache.json" >/dev/null; then
    trusted_cache=true
  else
    echo 'CI_RUNTIME_CACHE trusted cache unavailable; build without it.'
  fi
fi
printf 'CI_RUNTIME_CACHE trusted=%s refresh=%s epoch=%s\n' "$trusted_cache" "$refresh" "$cache_epoch"

build_image() {
  local name=$1 file=$2 repository=$3 started=$SECONDS digest
  shift 3
  local -a cache=()
  if [[ $trusted_main == true || -n $cache_scope ]]; then
    cache+=(--cache-to type=inline)
  fi
  if [[ -n $cache_scope ]]; then
    cache+=(--cache-from "type=registry,ref=$cache_scope:$name"
      --cache-to "type=registry,ref=$cache_scope:$name,mode=min,ignore-error=true")
  fi
  if [[ $trusted_cache == true ]]; then
    cache+=(--cache-from "type=registry,ref=$(jq -er --arg name "$name" '.[$name]' "$private/trusted-cache.json")")
  fi
  if [[ $refresh == true ]]; then
    cache=(--no-cache "${cache[@]}")
  fi
  docker buildx build --pull --platform linux/amd64 --push \
    --provenance=false --sbom=false "${cache[@]}" \
    --build-arg "CI_RUNTIME_CACHE_EPOCH=$cache_epoch" \
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
if [[ $trusted_main == true ]]; then
  jq -n --arg project "$CI_PROJECT_ID" --arg ref "$CI_DEFAULT_BRANCH" \
    --arg pipeline "$CI_PIPELINE_ID" --arg browser "$browser_digest" --arg runtime "$digest" \
    --argjson epoch "$cache_epoch" \
    '{project_id:$project,ref:$ref,protected:true,pipeline_id:$pipeline,cache_epoch:$epoch,browser:$browser,runtime:$runtime}' \
    > artifacts/runtime-cache.json
fi
jq -n --argjson browser "$browser_seconds" --argjson runtime "$runtime_seconds" \
  --arg scope "${cache_scope:+merge_request}" --argjson trusted "$trusted_cache" \
  --argjson refresh "$refresh" --argjson epoch "$cache_epoch" \
  '{browser_seconds:$browser,runtime_seconds:$runtime,cache_scope:$scope,trusted_cache:$trusted,refresh:$refresh,cache_epoch:$epoch}' \
  > artifacts/runtime-timing.json
