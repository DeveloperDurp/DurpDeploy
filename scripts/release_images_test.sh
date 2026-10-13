#!/usr/bin/env bash
# Exercise source/run/digest boundaries with real Git, ZIP, and OCI archives.
# GitHub is the only simulated service in the resolver scenarios.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export FIXTURE_DIR="$work/api"
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
merge=$(printf 'test merge\n' | git commit-tree "$tree" -p "$base" -p "$head")
git switch -q main
git merge --squash feature > /dev/null
git commit -qm squash
export GITHUB_SHA
GITHUB_SHA=$(git rev-parse HEAD)
first_target=$GITHUB_SHA
git push -q origin main
export GITHUB_REPOSITORY=fixture/project

cat > "$work/bin/gh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
path=${*: -1}
case "$path" in
  */pulls\?*) cat "$FIXTURE_DIR/pr.json" ;;
  */release.yml/runs\?*) cat "$FIXTURE_DIR/run.json" ;;
  */attempts/*/jobs\?*) cat "$FIXTURE_DIR/jobs.json" ;;
  */runs/*/artifacts\?*) cat "$FIXTURE_DIR/artifacts.json" ;;
  */artifacts/101/zip) cat "$FIXTURE_DIR/amd64.zip" ;;
  */artifacts/102/zip) cat "$FIXTURE_DIR/arm64.zip" ;;
  */git/commits/*) cat "$FIXTURE_DIR/commit.json" ;;
  *) printf 'Unexpected GitHub request: %s\n' "$path" >&2; exit 1 ;;
esac
SH
chmod +x "$work/bin/gh"
export PATH="$work/bin:$PATH"
jq -n --arg target "$GITHUB_SHA" --arg head "$head" '
  [{merged_at:"2026-10-13T00:00:00Z",merge_commit_sha:$target,
    head:{sha:$head},base:{ref:"main",repo:{full_name:"fixture/project"}}}]
' > "$FIXTURE_DIR/pr.json"
jq -n --arg head "$head" '
  {workflow_runs:[{id:42,run_attempt:1,event:"pull_request",head_sha:$head,
    repository:{full_name:"fixture/project"},path:".github/workflows/release.yml",
    status:"completed",conclusion:"success"}]}
' > "$FIXTURE_DIR/run.json"
printf '%s\n' '{"jobs":[{"name":"Image scan (amd64)","status":"completed","conclusion":"success"},{"name":"Image scan (arm64)","status":"completed","conclusion":"success"}]}' > "$FIXTURE_DIR/jobs.json"
jq -n --arg tree "$tree" --arg base "$base" --arg head "$head" \
	'{tree:{sha:$tree},parents:[{sha:$base},{sha:$head}]}' > "$FIXTURE_DIR/commit.json"
for arch in amd64 arm64; do
	layout="$work/$arch/layout"
	mkdir -p "$layout/blobs/sha256" "$work/$arch/artifact"
	jq -nc --arg arch "$arch" '{architecture:$arch,os:"linux",config:{},rootfs:{type:"layers",diff_ids:[]}}' > "$work/config"
	config_hash=$(sha256sum "$work/config" | cut -d ' ' -f 1)
	cp "$work/config" "$layout/blobs/sha256/$config_hash"
	jq -nc --arg digest "sha256:$config_hash" --argjson size "$(wc -c < "$work/config")" '
	  {schemaVersion:2,mediaType:"application/vnd.oci.image.manifest.v1+json",
	   config:{mediaType:"application/vnd.oci.image.config.v1+json",digest:$digest,size:$size},layers:[]}
	' > "$work/manifest"
	manifest_hash=$(sha256sum "$work/manifest" | cut -d ' ' -f 1)
	cp "$work/manifest" "$layout/blobs/sha256/$manifest_hash"
	jq -nc --arg digest "sha256:$manifest_hash" --argjson size "$(wc -c < "$work/manifest")" --arg arch "$arch" '
	  {schemaVersion:2,manifests:[{mediaType:"application/vnd.oci.image.manifest.v1+json",
	    digest:$digest,size:$size,platform:{architecture:$arch,os:"linux"}}]}
	' > "$layout/index.json"
	printf '%s\n' '{"imageLayoutVersion":"1.0.0"}' > "$layout/oci-layout"
	tar -cf "$work/$arch/artifact/image.tar" -C "$layout" .
	archive_hash=$(sha256sum "$work/$arch/artifact/image.tar" | cut -d ' ' -f 1)
	jq -n --arg tree "$tree" --arg commit "$merge" --arg head "$head" --arg base "$base" \
		--arg sha256 "$archive_hash" --arg arch "$arch" \
		'{schema:1,tree:$tree,commit:$commit,head:$head,base:$base,sha256:$sha256,arch:$arch,run:42,attempt:1}' \
		> "$work/$arch/artifact/source.json"
	(cd "$work/$arch/artifact" && zip -q "$FIXTURE_DIR/$arch.zip" image.tar source.json)
done
refresh_artifacts() {
	jq -n --arg amd "sha256:$(sha256sum "$FIXTURE_DIR/amd64.zip" | cut -d ' ' -f 1)" \
		--arg arm "sha256:$(sha256sum "$FIXTURE_DIR/arm64.zip" | cut -d ' ' -f 1)" --arg head "$head" '
	  {artifacts:[{id:101,name:"release-image-amd64-1",digest:$amd},
	    {id:102,name:"release-image-arm64-1",digest:$arm}]
	    | map(. + {expired:false,size_in_bytes:10000,workflow_run:{id:42,head_sha:$head}})}
	' > "$FIXTURE_DIR/artifacts.json"
}
refresh_artifacts
cp -R "$FIXTURE_DIR" "$work/good-api"
expect_rejection() {
	if bash "$root/scripts/resolve_release_images.sh" "$work/rejected" > "$work/rejection.log" 2>&1; then
		echo "Unexpected acceptance: $1" >&2
		exit 1
	fi
	printf 'PASS: reject %s\n' "$1"
	cp "$work/good-api/"* "$FIXTURE_DIR/"
}

# Given a squash merge, the tested merge SHA differs but its source tree matches.
# When resolving images, both architectures must retain their exact archive bytes.
bash "$root/scripts/resolve_release_images.sh" "$work/verified"
for arch in amd64 arm64; do
	cmp "$work/$arch/artifact/image.tar" "$work/verified/$arch/image.tar"
done
echo 'PASS: promote the matching squash-merge artifacts'

# Each boundary failure must stop before any registry publication is possible.
jq '.workflow_runs[0].conclusion="failure"' "$work/good-api/run.json" > "$FIXTURE_DIR/run.json"
expect_rejection 'failed workflow'
jq '.jobs[1].conclusion="failure"' "$work/good-api/jobs.json" > "$FIXTURE_DIR/jobs.json"
expect_rejection 'failed ARM64 scan'
jq '.workflow_runs[0].run_attempt=2' "$work/good-api/run.json" > "$FIXTURE_DIR/run.json"
expect_rejection 'artifacts from an earlier attempt'
jq '.artifacts[1].expired=true' "$work/good-api/artifacts.json" > "$FIXTURE_DIR/artifacts.json"
expect_rejection 'expired image'
jq '.artifacts |= .[:1]' "$work/good-api/artifacts.json" > "$FIXTURE_DIR/artifacts.json"
expect_rejection 'missing architecture'
printf 'corruption' >> "$FIXTURE_DIR/amd64.zip"
expect_rejection 'artifact digest mismatch'
printf 'changed image' >> "$work/amd64/artifact/image.tar"
(cd "$work/amd64/artifact" && zip -q "$FIXTURE_DIR/amd64.zip" image.tar source.json)
refresh_artifacts
expect_rejection 'image bytes that differ from the recorded archive hash'
jq '.tree.sha="0000000000000000000000000000000000000000"' "$work/good-api/commit.json" > "$FIXTURE_DIR/commit.json"
expect_rejection 'a different tested source tree'
jq '.parents[1].sha="0000000000000000000000000000000000000000"' "$work/good-api/commit.json" > "$FIXTURE_DIR/commit.json"
expect_rejection 'a different PR head'
jq '.[0].base.ref="other"' "$work/good-api/pr.json" > "$FIXTURE_DIR/pr.json"
expect_rejection 'a PR merged outside main'

# Given another PR has merged, the old artifact cannot represent the new source.
printf 'second PR\n' >> source
git commit -qam second
GITHUB_SHA=$(git rev-parse HEAD)
git push -q origin main
jq --arg target "$GITHUB_SHA" '.[0].merge_commit_sha=$target' "$work/good-api/pr.json" > "$FIXTURE_DIR/pr.json"
expect_rejection 'images from before another merged PR'

echo 'Release image boundary tests: PASS'
bash "$root/scripts/release_publish_test.sh" "$work" "$first_target"
