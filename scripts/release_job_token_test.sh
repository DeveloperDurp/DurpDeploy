#!/usr/bin/env bash
# Check publication API access with a real job token before merging CI changes.
set -euo pipefail
[[ ${GITLAB_CI:-} == true ]]
[[ ${CI_PROJECT_ID:?} =~ ^[0-9]+$ ]]
[[ ${CI_MERGE_REQUEST_IID:?} =~ ^[0-9]+$ ]]
[[ ${CI_PIPELINE_ID:?} =~ ^[0-9]+$ ]]
[[ ${CI_COMMIT_SHA:?} =~ ^[0-9a-f]{40}$ ]]
[[ ${CI_API_V4_URL:?} == https://* ]]
: "${CI_JOB_TOKEN:?}"
private=$(mktemp -d)
trap 'rm -rf "$private"' EXIT
api() {
  curl --fail --silent --show-error --max-time 180 --max-filesize 10485760 \
    --proto '=https' --header "JOB-TOKEN: $CI_JOB_TOKEN" \
    "$CI_API_V4_URL/projects/$CI_PROJECT_ID/$1"
}
api "repository/commits/$CI_COMMIT_SHA/merge_requests?per_page=100" |
  jq -e 'type == "array"' >/dev/null
api "merge_requests/$CI_MERGE_REQUEST_IID" > "$private/mr.json"
jq -e --argjson pipeline "$CI_PIPELINE_ID" --argjson project "$CI_PROJECT_ID" '
  .head_pipeline.id == $pipeline and .head_pipeline.project_id == $project
  and .head_pipeline.source == "merge_request_event"
' "$private/mr.json" >/dev/null
api "pipelines/$CI_PIPELINE_ID/jobs?per_page=100&include_retried=true" > "$private/jobs.json"
version_job=$(jq -er '[.[] | select(.name == "version")] | max_by(.id)
  | select(.status == "success") | .id | select(type == "number" and . > 0)' "$private/jobs.json")
api "jobs/$version_job/artifacts/artifacts/build.env" > "$private/build.env"
test -s "$private/build.env"
echo 'Publication job-token API and artifact access: PASS'
