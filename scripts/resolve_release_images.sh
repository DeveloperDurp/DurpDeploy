#!/usr/bin/env bash
# Download only the scanned images associated with this merged source tree.
set -euo pipefail

destination=${1:?usage: resolve_release_images.sh DESTINATION}
repository=${GITHUB_REPOSITORY:?}
[[ "${GITHUB_SHA:?}" =~ ^[0-9a-f]{40}$ ]]
target=$(git rev-parse "${GITHUB_SHA:?}^{commit}")
tree=$(git rev-parse "$target^{tree}")
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]
mkdir -p "$destination"
destination=$(cd "$destination" && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

git fetch --no-tags origin main
git merge-base --is-ancestor "$target" origin/main || {
	echo 'Release source must belong to main.' >&2
	exit 1
}
gh api --paginate "repos/$repository/commits/$target/pulls?per_page=100" |
	jq -s --arg target "$target" --arg repository "$repository" '
		[.[][] | select(.merged_at != null and .merge_commit_sha == $target
			and .base.ref == "main" and .base.repo.full_name == $repository)]
		| if length == 1 then .[0] else error("No unique merged PR") end
	' > "$scratch/pr.json"
head=$(jq -er '.head.sha | select(test("^[0-9a-f]{40}$"))' "$scratch/pr.json")
gh api --paginate \
	"repos/$repository/actions/workflows/release.yml/runs?event=pull_request&head_sha=$head&per_page=100" |
	jq -s --arg head "$head" --arg repository "$repository" '
		[.[].workflow_runs[] | select(.head_sha == $head
			and .event == "pull_request" and .path == ".github/workflows/release.yml"
			and .repository.full_name == $repository)] | sort_by(.id) | last
		| select(.status == "completed" and .conclusion == "success")
	' > "$scratch/run.json"
run=$(jq -er '.id | select(type == "number" and . > 0)' "$scratch/run.json")
attempt=$(jq -er '.run_attempt | select(type == "number" and . > 0)' "$scratch/run.json")
gh api --paginate "repos/$repository/actions/runs/$run/attempts/$attempt/jobs?per_page=100" |
	jq -s '[.[].jobs[]]' > "$scratch/jobs.json"
gh api --paginate "repos/$repository/actions/runs/$run/artifacts?per_page=100" |
	jq -s '[.[].artifacts[]]' > "$scratch/artifacts.json"

for arch in amd64 arm64; do
	jq -e --arg name "Image scan ($arch)" '
		[.[] | select(.name == $name)] | length == 1 and
		all(.[]; .status == "completed" and .conclusion == "success")
	' "$scratch/jobs.json" > /dev/null
	jq -e --arg name "release-image-$arch-$attempt" --arg head "$head" \
		--argjson run "$run" '
		[.[] | select(.name == $name)]
		| if length == 1 then .[0] else error("No unique image artifact") end
		| select(.expired == false and .workflow_run.id == $run
			and .workflow_run.head_sha == $head and .size_in_bytes < 2147483648)
	' "$scratch/artifacts.json" > "$scratch/artifact.json"
	id=$(jq -er '.id | select(type == "number" and . > 0)' "$scratch/artifact.json")
	digest=$(jq -er '.digest | select(test("^sha256:[0-9a-f]{64}$"))' "$scratch/artifact.json")
	gh api "repos/$repository/actions/artifacts/$id/zip" > "$scratch/image.zip"
	printf '%s  %s\n' "${digest#sha256:}" "$scratch/image.zip" | sha256sum -c -
	# Extract only these two regular data files. Never unpack archive paths.
	[[ "$(unzip -Z1 "$scratch/image.zip" | LC_ALL=C sort)" == $'image.tar\nsource.json' ]]
	LC_ALL=C unzip -l "$scratch/image.zip" | awk '
		$4 == "image.tar" { image = $1 }
		$4 == "source.json" { metadata = $1 }
		END { exit !(image > 0 && image < 2147483648 && metadata > 0 && metadata < 65536) }
	'
	mkdir -p "$destination/$arch"
	unzip -p "$scratch/image.zip" source.json > "$destination/$arch/source.json"
	jq -e --arg tree "$tree" --arg head "$head" --arg arch "$arch" \
		--argjson run "$run" --argjson attempt "$attempt" '
		.schema == 1 and .tree == $tree and .head == $head and .arch == $arch
		and .run == $run and .attempt == $attempt
		and (.commit | test("^[0-9a-f]{40}$"))
		and (.base | test("^[0-9a-f]{40}$"))
		and (.sha256 | test("^[0-9a-f]{64}$"))
	' "$destination/$arch/source.json" > /dev/null
	commit=$(jq -er '.commit' "$destination/$arch/source.json")
	base=$(jq -er '.base' "$destination/$arch/source.json")
	gh api "repos/$repository/git/commits/$commit" |
		jq -e --arg tree "$tree" --arg head "$head" --arg base "$base" '
			.tree.sha == $tree and ([.parents[].sha] == [$base, $head])
		' > /dev/null
	unzip -p "$scratch/image.zip" image.tar > "$destination/$arch/image.tar"
	archive_hash=$(jq -er '.sha256' "$destination/$arch/source.json")
	printf '%s  %s\n' "$archive_hash" "$destination/$arch/image.tar" | sha256sum -c -
	skopeo inspect --config "oci-archive:$destination/$arch/image.tar" |
		jq -e --arg arch "$arch" '.os == "linux" and .architecture == $arch' > /dev/null
	skopeo inspect --raw "oci-archive:$destination/$arch/image.tar" > "$destination/$arch/manifest.json"
	# Refuse nested indexes or attestations in place of a scanned platform image.
	jq -e '.schemaVersion == 2 and .mediaType == "application/vnd.oci.image.manifest.v1+json"' \
		"$destination/$arch/manifest.json" > /dev/null
done
[[ "$(jq -r '.commit' "$destination/amd64/source.json")" == \
	"$(jq -r '.commit' "$destination/arm64/source.json")" ]]
printf 'Verified PR images from run %s, attempt %s, source tree %s.\n' "$run" "$attempt" "$tree"
