#!/usr/bin/env bash
# Credentials are scoped to the protected production environment.
set -euo pipefail
[[ ${CI_COMMIT_REF_PROTECTED:-} == true ]]
[[ ${CI_PIPELINE_SOURCE:-} == push || ${CI_PIPELINE_SOURCE:-} == web ]]
[[ ${CI_COMMIT_BRANCH:-} == "${CI_DEFAULT_BRANCH:?}" || -n ${CI_COMMIT_TAG:-} ]]
export BUILD_VERSION=${software_version:?} BUILD_NUMBER=${build_number:?}
export IS_RELEASE=${is_release:?}
private=$(mktemp -d)
trap 'rm -rf "$private"' EXIT
case ${1:?usage: ci_publish.sh images|chart} in
  images)
    bash scripts/resolve_release_images.sh "$private/verified-mr-images"
    bash scripts/prepare_versioned_images.sh artifacts "$private/verified-images"
    printf '%s' "${GHCR_TOKEN:?}" |
      docker login ghcr.io -u "${GHCR_USER:?}" --password-stdin
    printf '%s' "${CI_REGISTRY_PASSWORD:?}" |
      docker login "${CI_REGISTRY:?}" -u "${CI_REGISTRY_USER:?}" --password-stdin
    for image in "${CI_REGISTRY_IMAGE:?}" "ghcr.io/${REGISTRY_OWNER:?}/${IMAGE_NAME:?}"; do
      bash scripts/publish_release_images.sh "$private/verified-images" "${version:?}" "$image"
    done
    ;;
  chart)
    mkdir "$private/bin"
    ln -s /usr/local/bin/helm3 "$private/bin/helm"
    export PATH="$private/bin:$PATH"
    printf '%s' "${GHCR_TOKEN:?}" |
      helm registry login ghcr.io -u "${GHCR_USER:?}" --password-stdin
    printf '%s' "${CI_REGISTRY_PASSWORD:?}" |
      helm registry login "${CI_REGISTRY:?}" -u "${CI_REGISTRY_USER:?}" --password-stdin
    helm lint charts/durpdeploy
    make templ-generate swagger-ui-copy
    bash scripts/go_test.sh -race -count=1 ./charts/durpdeploy
    helm package charts/durpdeploy --destination "$private" \
      --app-version "${version:?}" --version "${chart_version:?}"
    bash scripts/publish_release_chart.sh "$private/durpdeploy-$chart_version.tgz" "$chart_version"
    ;;
  *) exit 2 ;;
esac
