#!/usr/bin/env bash
# Verify this run's stamped archives before the publisher receives them.
set -euo pipefail
source_directory=${1:?usage: prepare_versioned_images.sh SOURCE DESTINATION}
destination=${2:?}
[[ "${GITHUB_SHA:?}" =~ ^[0-9a-f]{40}$ ]]
target=$(git rev-parse "${GITHUB_SHA:?}^{commit}")
tree=$(git rev-parse "$target^{tree}")
for arch in amd64 arm64; do
	source="$source_directory/built-image-$arch"
	jq -e --arg commit "$target" --arg tree "$tree" --arg arch "$arch" \
		--arg version "${BUILD_VERSION:?}" --arg number "${BUILD_NUMBER:?}" \
		--argjson run "${GITHUB_RUN_ID:?}" --argjson attempt "${GITHUB_RUN_ATTEMPT:?}" '
		.schema == 1 and .commit == $commit and .tree == $tree and .arch == $arch
		and .version == $version and .build_number == $number and .run == $run
		and (.attempt | type == "number") and .attempt > 0 and .attempt <= $attempt
		and (.sha256 | test("^[0-9a-f]{64}$"))
	' "$source/source.json" > /dev/null
	archive_hash=$(jq -er '.sha256' "$source/source.json")
	printf '%s  %s\n' "$archive_hash" "$source/image.tar" | sha256sum -c -
	skopeo inspect --config "oci-archive:$source/image.tar" |
		jq -e --arg arch "$arch" --arg commit "$target" \
			--arg version "$BUILD_VERSION" --arg number "$BUILD_NUMBER" '
			.os == "linux" and .architecture == $arch
			and .config.Labels["org.opencontainers.image.revision"] == $commit
			and .config.Labels["org.opencontainers.image.version"] == $version
			and .config.Labels["info.durpdeploy.build-number"] == $number
		' > /dev/null
	mkdir -p "$destination/$arch"
	skopeo inspect --raw "oci-archive:$source/image.tar" > "$destination/$arch/manifest.json"
	jq -e '.schemaVersion == 2 and .mediaType == "application/vnd.oci.image.manifest.v1+json"' \
		"$destination/$arch/manifest.json" > /dev/null
	cp "$source/image.tar" "$source/source.json" "$destination/$arch/"
done
