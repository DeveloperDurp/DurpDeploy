#!/usr/bin/env bash
# Run only after prepare_versioned_images.sh, inside the serialized publisher.
set -euo pipefail

directory=${1:?usage: publish_release_images.sh DIRECTORY VERSION [IMAGE]}
version=${2:?}
[[ "${CI_COMMIT_SHA:?}" =~ ^[0-9a-f]{40}$ ]]
target=$(git rev-parse "${CI_COMMIT_SHA:?}^{commit}")
github_image="ghcr.io/${REGISTRY_OWNER:?}/${IMAGE_NAME:?}"
[[ "$github_image" =~ ^ghcr\.io/[a-z0-9_.-]+/[a-z0-9_.-]+$ ]]
image=${3:-$github_image}
[[ "$image" == "$github_image" || ( -n ${CI_REGISTRY_IMAGE:-} && "$image" == "$CI_REGISTRY_IMAGE" ) ]]
[[ "$image" =~ ^[a-z0-9.-]+(:[0-9]+)?/[a-z0-9_./-]+$ ]]
[[ "$version" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]]
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
digests=()
for arch in amd64; do
	digest="sha256:$(sha256sum "$directory/$arch/manifest.json" | cut -d ' ' -f 1)"
	# Digest-derived staging tags never overwrite another candidate image.
	skopeo copy --preserve-digests --digestfile "$scratch/copied" \
		"oci-archive:$directory/$arch/image.tar" "docker://$image:sha256-${digest#sha256:}"
	[[ "$(cat "$scratch/copied")" == "$digest" ]]
	digests+=("$image@$digest")
done

# Generate the index once, including the real merged source revision.
index=$(docker buildx imagetools create --dry-run \
	--annotation "index:org.opencontainers.image.revision=$target" \
	"${digests[@]}")
# Buildx prints a newline for --dry-run. It is not part of the registry digest.
printf '%s' "$index" > "$scratch/index.json"
expected="sha256:$(sha256sum "$scratch/index.json" | cut -d ' ' -f 1)"

# Missing tags are the only acceptable lookup failure. Network/auth errors fail.
existing=$(skopeo list-tags "docker://$image")
if jq -e --arg version "$version" '.Tags | index($version) != null' <<< "$existing" > /dev/null; then
	skopeo inspect --raw "docker://$image:$version" > "$scratch/existing.json"
	[[ "sha256:$(sha256sum "$scratch/existing.json" | cut -d ' ' -f 1)" == "$expected" ]] || {
		echo 'Refusing to replace an existing version with different images.' >&2
		exit 1
	}
fi
docker buildx imagetools create --tag "$image:$version" \
	--annotation "index:org.opencontainers.image.revision=$target" "${digests[@]}"
skopeo inspect --raw "docker://$image:$version" > "$scratch/published.json"
[[ "sha256:$(sha256sum "$scratch/published.json" | cut -d ' ' -f 1)" == "$expected" ]]

if [[ "${IS_RELEASE:?}" == true ]]; then
	if jq -e '.Tags | index("latest") != null' <<< "$existing" > /dev/null; then
		skopeo inspect --raw "docker://$image:latest" > "$scratch/latest.json"
		previous=$(jq -r '.annotations["org.opencontainers.image.revision"] // empty' "$scratch/latest.json")
		if [[ -z "$previous" ]]; then
			# Migrate the existing Buildx release's config label on first promotion.
			previous=$(skopeo inspect "docker://$image:latest" |
				jq -er '.Labels["org.opencontainers.image.revision"]')
		fi
		[[ "$previous" =~ ^[0-9a-f]{40}$ ]]
		if git merge-base --is-ancestor "$previous" "$target"; then
			:
		else
			status=$?
			[[ "$status" == 1 ]] || exit "$status"
			echo 'Retained latest because it contains a newer or unrelated source revision.'
			exit 0
		fi
	fi
	skopeo copy --all --preserve-digests "docker://$image@$expected" "docker://$image:latest"
fi
