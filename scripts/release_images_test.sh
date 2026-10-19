#!/usr/bin/env bash
# Exercise GitLab provenance with real Git and OCI archives.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export FIXTURE_DIR="$work/api"
export REAL_CURL
REAL_CURL=$(command -v curl)
mkdir -p "$FIXTURE_DIR" "$work/bin"
git init -q --bare --initial-branch=main "$work/origin"
git clone -q "$work/origin" "$work/repo"
cd "$work/repo"
git config user.email fixture@example.invalid
git config user.name Fixture
printf 'base\n' > source
git add source
git commit -qm base
base=$(git rev-parse HEAD)
git switch -qc feature
printf 'change\n' > source
git commit -qam change
head=$(git rev-parse HEAD)
tree=$(git rev-parse HEAD^{tree})
git switch -q main
git merge --squash feature >/dev/null
git commit -qm squash
export CI_COMMIT_SHA CI_PROJECT_ID=3 CI_DEFAULT_BRANCH=main
export CI_API_V4_URL=https://gitlab.example/api/v4 GITLAB_READ_TOKEN=fixture
CI_COMMIT_SHA=$(git rev-parse HEAD)
first_target=$CI_COMMIT_SHA
git push -q origin main

cat > "$work/bin/curl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
path=${*: -1}
case "$path" in
  https://gitlab.example/api/v4/projects/3/*) ;;
  *) exec "$REAL_CURL" "$@" ;;
esac
[[ ! -e "$FIXTURE_DIR/fail" ]] || exit 22
case "$path" in
  */commits/*/merge_requests\?*)
    if [[ $path == *'page=2' ]]; then cat "$FIXTURE_DIR/mrs-next.json"; else cat "$FIXTURE_DIR/mrs.json"; fi ;;
  */merge_requests/1) cat "$FIXTURE_DIR/mr.json" ;;
  */merge_requests/1/pipelines\?*) cat "$FIXTURE_DIR/pipelines.json" ;;
  */pipelines/42/jobs\?*) cat "$FIXTURE_DIR/jobs.json" ;;
  */jobs/*/artifacts/artifacts/built-image-amd64/source.json) cat "$FIXTURE_DIR/source.json" ;;
  */jobs/*/artifacts/artifacts/built-image-amd64/image.tar) cat "$FIXTURE_DIR/image.tar" ;;
  *) printf 'Unexpected GitLab request: %s\n' "$path" >&2; exit 1 ;;
esac
SH
chmod +x "$work/bin/curl"
export PATH="$work/bin:$PATH"
jq -n --arg target "$CI_COMMIT_SHA" --arg head "$head" --arg base "$base" '
  {iid:1,state:"merged",squash_commit_sha:$target,merge_commit_sha:null,
    target_project_id:3,target_branch:"main",diff_refs:{head_sha:$head,base_sha:$base}}
' > "$FIXTURE_DIR/mr.json"
jq -s '.' "$FIXTURE_DIR/mr.json" > "$FIXTURE_DIR/mrs.json"
printf '[]\n' > "$FIXTURE_DIR/mrs-next.json"
jq -n --arg head "$head" '
  [{id:42,project_id:3,source:"merge_request_event",status:"success",sha:$head}]
' > "$FIXTURE_DIR/pipelines.json"
jq -n '["images","retain-image","image-verification","test","e2e","security-scan","container-security"]
  | to_entries | map({id:(.key+11),name:.value,status:"success",allow_failure:false,
    pipeline:{id:42},erased_at:null,artifacts_file:{size:10000}})' > "$FIXTURE_DIR/jobs.json"
layout="$work/layout"
mkdir -p "$layout/blobs/sha256"
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
tar -cf "$FIXTURE_DIR/image.tar" -C "$layout" .
hash=$(sha256sum "$FIXTURE_DIR/image.tar" | cut -d ' ' -f 1)
jq -n --arg tree "$tree" --arg head "$head" --arg base "$base" --arg hash "$hash" '
  {schema:2,arch:"amd64",tree:$tree,commit:$head,head:$head,base:$base,
    sha256:$hash,pipeline:42,image_job:11}
' > "$FIXTURE_DIR/source.json"
cp -R "$FIXTURE_DIR" "$work/good-api"
reset_api() { rm -f "$FIXTURE_DIR/fail"; cp "$work/good-api/"* "$FIXTURE_DIR/"; }
expect_rejection() {
  if bash "$root/scripts/resolve_release_images.sh" "$work/rejected" > "$work/rejection.log" 2>&1; then
    printf 'Unexpected acceptance: %s\n' "$1" >&2
    exit 1
  fi
  printf 'PASS: reject %s\n' "$1"
  reset_api
}
bash "$root/scripts/resolve_release_images.sh" "$work/verified"
cmp "$FIXTURE_DIR/image.tar" "$work/verified/amd64/image.tar"
echo 'PASS: promote matching AMD64 squash-merge artifacts'

# Unrelated test retries do not invalidate the original scanned image job.
jq '. += [(.[] | select(.name == "e2e") | .id=115)]' "$work/good-api/jobs.json" > "$FIXTURE_DIR/jobs.json"
bash "$root/scripts/resolve_release_images.sh" "$work/partial-verified"
reset_api
echo 'PASS: reuse scanned images after an unrelated successful retry'

# Paginated commit associations must include the matching MR on later pages.
jq -n '[range(100) | {state:"closed"}]' > "$FIXTURE_DIR/mrs.json"
cp "$work/good-api/mrs.json" "$FIXTURE_DIR/mrs-next.json"
bash "$root/scripts/resolve_release_images.sh" "$work/paginated"
reset_api
echo 'PASS: resolve paginated MR associations'

for mutation in '.[0].status="failed"' '.[0].project_id=99' '.[0].source="push"' '.[0].sha=("0"*40)'; do
  jq "$mutation" "$work/good-api/pipelines.json" > "$FIXTURE_DIR/pipelines.json"
  expect_rejection "unverified pipeline: $mutation"
done
for name in images retain-image image-verification test e2e security-scan container-security; do
  jq --arg name "$name" '. += [(.[] | select(.name == $name) | .id+=100 | .status="failed")]' \
    "$work/good-api/jobs.json" > "$FIXTURE_DIR/jobs.json"
  expect_rejection "failed latest $name job"
done
jq '. += [(.[] | select(.name == "images") | .id=111)]' "$work/good-api/jobs.json" > "$FIXTURE_DIR/jobs.json"
expect_rejection 'artifact from before the latest image retry'
for mutation in '.[] |= if .name == "retain-image" then .erased_at="expired" else . end' \
  '. |= map(select(.name != "retain-image"))' \
  '.[] |= if .name == "test" then .allow_failure=true else . end'; do
  jq "$mutation" "$work/good-api/jobs.json" > "$FIXTURE_DIR/jobs.json"
  expect_rejection "$mutation"
done
for mutation in '.tree=("0"*40)' '.head=("0"*40)' '.base=("0"*40)' '.pipeline=99' \
  '.image_job=111' '.arch="arm64"' '.schema=1' '.sha256=("0"*64)'; do
  jq "$mutation" "$work/good-api/source.json" > "$FIXTURE_DIR/source.json"
  expect_rejection "mismatched image provenance: $mutation"
done
printf 'corruption' >> "$FIXTURE_DIR/image.tar"
expect_rejection 'archive digest mismatch'
touch "$FIXTURE_DIR/fail"
expect_rejection 'API authentication or transport failure'
jq '.[0].target_branch="other"' "$work/good-api/mrs.json" > "$FIXTURE_DIR/mrs.json"
expect_rejection 'MR merged outside the default branch'

mkdir -p "$work/versioned/built-image-amd64"
cp "$FIXTURE_DIR/image.tar" "$work/versioned/built-image-amd64/"
jq --arg commit "$CI_COMMIT_SHA" '.commit=$commit | .version="v1.2.3+build.4"
  | .build_number="742" | .pipeline=99 | .image_job=111' \
  "$FIXTURE_DIR/source.json" > "$work/versioned-source.json"
cp "$work/versioned-source.json" "$work/versioned/built-image-amd64/source.json"
prepare_versioned() {
  CI_PIPELINE_ID=99 SCANNED_IMAGE_JOB_ID=111 BUILD_VERSION=v1.2.3+build.4 BUILD_NUMBER=742 \
    bash "$root/scripts/prepare_versioned_images.sh" "$work/versioned" "$work/versioned-verified"
}
prepare_versioned
for mutation in '.pipeline=98' '.image_job=110' '.tree="wrong"' '.version="v9.9.9"' '.build_number="743"' '.sha256=("0"*64)'; do
  jq "$mutation" "$work/versioned-source.json" > "$work/versioned/built-image-amd64/source.json"
  if prepare_versioned > "$work/versioned-rejected.log" 2>&1; then
    printf 'Unexpected versioned archive acceptance: %s\n' "$mutation" >&2
    exit 1
  fi
done
echo 'PASS: versioned archives retain exact pipeline, image job, source, version, count, and digest'

printf 'second MR\n' >> source
git commit -qam second
CI_COMMIT_SHA=$(git rev-parse HEAD)
git push -q origin main
jq --arg target "$CI_COMMIT_SHA" '.[0].squash_commit_sha=$target' "$work/good-api/mrs.json" > "$FIXTURE_DIR/mrs.json"
expect_rejection 'images from before another merged MR'
echo 'Release image boundary tests: PASS'
bash "$root/scripts/release_publish_test.sh" "$work" "$first_target"
