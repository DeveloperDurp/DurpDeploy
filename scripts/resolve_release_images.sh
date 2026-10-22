#!/usr/bin/env bash
# Resolve verified GitLab MR sources for merge, squash, and fast-forward merges.
set -euo pipefail
destination=${1:?usage: resolve_release_images.sh DESTINATION}
[[ ${CI_PROJECT_ID:?} =~ ^[0-9]+$ ]]
[[ ${CI_COMMIT_SHA:?} =~ ^[0-9a-f]{40}$ ]]
[[ ${CI_API_V4_URL:?} == https://* ]]
: "${CI_JOB_TOKEN:?GitLab must provide a CI job token}"
target=$(git rev-parse "$CI_COMMIT_SHA^{commit}")
tree=$(git rev-parse "$target^{tree}")
git merge-base --is-ancestor "$target" "origin/${CI_DEFAULT_BRANCH:?}"
mkdir -p "$destination"
destination=$(cd "$destination" && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

api() {
  local endpoint=$1 max_bytes=${2:-2147483648}
  # GitLab serves these same-origin artifact-file endpoints without redirects.
  curl --fail --silent --show-error --max-time 180 \
    --proto '=https' --proto-redir '=https' \
    --max-filesize "$max_bytes" \
    --header "JOB-TOKEN: $CI_JOB_TOKEN" \
    "$CI_API_V4_URL/projects/$CI_PROJECT_ID/$endpoint"
}
api_list() {
  local endpoint=$1 page=1 count separator='?'
  [[ $endpoint != *\?* ]] || separator='&'
  : > "$scratch/pages.json"
  while ((page <= 10000)); do
    api "$endpoint${separator}per_page=100&page=$page" 10485760 > "$scratch/page.json"
    count=$(jq -er 'if type == "array" then length else error("Expected an API array") end' "$scratch/page.json")
    cat "$scratch/page.json" >> "$scratch/pages.json"
    if ((count < 100)); then jq -s 'add' "$scratch/pages.json"; return; fi
    ((page+=1))
  done
  echo 'GitLab pagination limit exceeded.' >&2
  return 1
}

api_list "repository/commits/$target/merge_requests?state=merged" |
  jq -e --arg target "$target" --arg branch "$CI_DEFAULT_BRANCH" --argjson project "$CI_PROJECT_ID" '
    [.[] | select(.state == "merged" and .target_branch == $branch
      and .target_project_id == $project
      and (.merge_commit_sha == $target or .squash_commit_sha == $target
        or (.merge_commit_sha == null and .squash_commit_sha == null and .sha == $target)))]
    | if length == 1 then .[0] else error("No unique merged MR") end
  ' > "$scratch/mr.json"
iid=$(jq -er '.iid | select(type == "number" and . > 0)' "$scratch/mr.json")
api "merge_requests/$iid" 1048576 > "$scratch/mr.json"
head=$(jq -er '.diff_refs.head_sha | select(test("^[0-9a-f]{40}$"))' "$scratch/mr.json")
base=$(jq -er '.diff_refs.base_sha | select(test("^[0-9a-f]{40}$"))' "$scratch/mr.json")
jq -e --argjson project "$CI_PROJECT_ID" '
  .head_pipeline | select(.project_id == $project
    and .source == "merge_request_event" and .status == "success")
' "$scratch/mr.json" > "$scratch/pipeline.json"
pipeline=$(jq -er '.id | select(type == "number" and . > 0)' "$scratch/pipeline.json")
tested=$(jq -er '.sha | select(test("^[0-9a-f]{40}$"))' "$scratch/pipeline.json")
if ! git cat-file -e "$tested^{commit}" 2>/dev/null; then git fetch --no-tags origin "$tested"; fi
if [[ $(git rev-parse "$tested^{tree}") != "$tree" ]]; then
  echo 'Publication source tree does not match the verified MR pipeline. Rebase and rerun MR checks before merging.' >&2
  exit 1
fi
api_list "pipelines/$pipeline/jobs?include_retried=true" > "$scratch/jobs.json"
latest_job() {
  local name=$1
  jq -er --arg name "$name" --argjson pipeline "$pipeline" '
    [.[] | select(.name == $name)] | max_by(.id)
    | select(.status == "success" and .allow_failure == false and .pipeline.id == $pipeline)
    | .id | select(type == "number" and . > 0)
  ' "$scratch/jobs.json"
}
for required in image-verification e2e security-scan container-security \
  'test: [repository]' 'test: [database]' 'test: [handlers]' 'test: [core]'; do
  latest_job "$required" >/dev/null
done
image_job=$(latest_job images)
retained=$(latest_job retain-image)
jq -e --argjson job "$retained" '
  .[] | select(.id == $job and .erased_at == null
    and .artifacts_file.size > 0 and .artifacts_file.size < 2147483648)
' "$scratch/jobs.json" >/dev/null
mkdir -p "$destination/amd64"
api "jobs/$retained/artifacts/artifacts/built-image-amd64/source.json" 65536 > "$destination/amd64/source.json"
jq -e --arg tree "$tree" --arg head "$head" --arg base "$base" --arg tested "$tested" \
  --argjson pipeline "$pipeline" --argjson job "$image_job" '
  .schema == 2 and .arch == "amd64" and .tree == $tree and .head == $head and .base == $base
  and .commit == $tested and .pipeline == $pipeline and .image_job == $job
  and (.sha256 | test("^[0-9a-f]{64}$"))
' "$destination/amd64/source.json" >/dev/null
api "jobs/$retained/artifacts/artifacts/built-image-amd64/image.tar" > "$destination/amd64/image.tar"
hash=$(jq -er '.sha256' "$destination/amd64/source.json")
printf '%s  %s\n' "$hash" "$destination/amd64/image.tar" | sha256sum -c -
skopeo inspect --config "oci-archive:$destination/amd64/image.tar" |
  jq -e '.os == "linux" and .architecture == "amd64"' >/dev/null
skopeo inspect --raw "oci-archive:$destination/amd64/image.tar" > "$destination/amd64/manifest.json"
jq -e '.schemaVersion == 2 and .mediaType == "application/vnd.oci.image.manifest.v1+json"' \
  "$destination/amd64/manifest.json" >/dev/null
printf 'Verified MR %s, pipeline %s, source tree %s.\n' "$iid" "$pipeline" "$tree"
