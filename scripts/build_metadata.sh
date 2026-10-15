#!/usr/bin/env bash
# Share build identity between local builds, containers, and CI.
set -euo pipefail

BUILD_COMMIT=${BUILD_COMMIT:-$(git rev-parse --verify HEAD 2>/dev/null || printf unknown)}
if [[ -z "${BUILD_NUMBER:-}" ]]; then
	BUILD_NUMBER=unknown
	if [[ "$(git rev-parse --is-shallow-repository 2>/dev/null || true)" == false ]]; then
		BUILD_NUMBER=$(git rev-list --count HEAD)
	fi
fi
if [[ -z "${BUILD_MODIFIED:-}" ]]; then
	BUILD_MODIFIED=false
	if [[ "$BUILD_COMMIT" != unknown ]]; then
		if ! git diff --quiet HEAD -- 2>/dev/null || \
			[[ -n "$(git ls-files --others --exclude-standard 2>/dev/null)" ]]; then
			BUILD_MODIFIED=true
		fi
	fi
fi
semver='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
IS_RELEASE=false
if [[ -z "${BUILD_VERSION:-}" ]]; then
	if [[ "${GITHUB_REF_TYPE:-}" == tag ]]; then
		BUILD_VERSION=${GITHUB_REF_NAME:?}
		[[ "$BUILD_VERSION" =~ $semver ]] || {
			echo 'Tag must be a valid SemVer with a v prefix.' >&2
			exit 1
		}
	elif [[ -z "${GITHUB_REF_TYPE:-}" && "$BUILD_MODIFIED" == false ]]; then
		candidate=$(git describe --tags --exact-match --match 'v[0-9]*' HEAD 2>/dev/null || true)
		if [[ "$candidate" =~ $semver ]]; then
			BUILD_VERSION=$candidate
		fi
	fi
fi
if [[ -z "${BUILD_VERSION:-}" ]]; then
	BUILD_VERSION=dev
	if [[ "$BUILD_COMMIT" != unknown && "$BUILD_NUMBER" != unknown ]]; then
		BUILD_VERSION="v0.0.0-dev.${BUILD_NUMBER}+g${BUILD_COMMIT:0:12}"
		if [[ "$BUILD_MODIFIED" == true ]]; then
			BUILD_VERSION+=.dirty
		fi
	fi
fi
[[ "$BUILD_VERSION" == dev || "$BUILD_VERSION" =~ $semver ]]
[[ "$BUILD_COMMIT" == unknown || "$BUILD_COMMIT" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]]
[[ "$BUILD_NUMBER" == unknown || "$BUILD_NUMBER" =~ ^(0|[1-9][0-9]*)$ ]]
[[ "$BUILD_MODIFIED" == true || "$BUILD_MODIFIED" == false ]]
if [[ "${GITHUB_REF_TYPE:-}" == tag ]]; then
	[[ "$BUILD_VERSION" == "${GITHUB_REF_NAME:?}" && "$BUILD_MODIFIED" == false ]]
	IS_RELEASE=true
fi
IMAGE_VERSION=${BUILD_VERSION//+/_}
[[ ${#IMAGE_VERSION} -le 128 ]]
if [[ "$BUILD_VERSION" == dev ]]; then
	CHART_VERSION=0.0.0-dev
else
	CHART_VERSION=${BUILD_VERSION#v}
fi

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	printf 'software_version=%s\nversion=%s\nchart_version=%s\nbuild_number=%s\ncommit=%s\nmodified=%s\nis_release=%s\n' \
		"$BUILD_VERSION" "$IMAGE_VERSION" "$CHART_VERSION" "$BUILD_NUMBER" \
		"$BUILD_COMMIT" "$BUILD_MODIFIED" "$IS_RELEASE"
fi
