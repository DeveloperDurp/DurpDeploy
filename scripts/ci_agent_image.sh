#!/usr/bin/env bash
# Build the locked agent once, then verify and load it in each private runtime.
set -euo pipefail
[[ ${GITLAB_CI:-} == true ]]
action=${1:?usage: ci_agent_image.sh build|load}
directory=artifacts/agent-build
image="localhost/durpdeploy-agent:gitlab-${CI_PIPELINE_ID:?}"
module=github.com/DeveloperDurp/durpdeploy-agent
version=$(go list -m -f '{{.Version}}' "$module")
sum=$(go list -m -f '{{.Sum}}' "$module")
case $action in
  build)
    source=$(go list -m -f '{{.Dir}}' "$module")
    private=$(mktemp -d)
    container=
    cleanup() {
      if [[ -n $container ]]; then docker rm -v "$container" >/dev/null; fi
      rm -rf -- "$private"
    }
    trap cleanup EXIT
    mkdir -p "$directory"
    sed -e 's@docker.io/@harbor.durp.info/dockerhub/@g' \
      -e 's@syntax=docker/dockerfile:@syntax=harbor.durp.info/dockerhub/docker/dockerfile:@' \
      "$source/Dockerfile" > "$private/Dockerfile"
    docker build -f "$private/Dockerfile" -t "$image" "$source"
    docker save --output "$private/image.tar" "$image"
    mv "$private/image.tar" "$directory/image.tar"
    container=$(docker create "$image")
    docker cp "$container:/usr/local/bin/durpdeploy-agent" "$directory/agent"
    chmod 0755 "$directory/agent"
    jq -n --arg commit "${CI_COMMIT_SHA:?}" --arg version "$version" \
      --arg sum "$sum" --arg image "$image" \
      --argjson pipeline "$CI_PIPELINE_ID" --argjson job "${CI_JOB_ID:?}" \
      --arg archive "$(sha256sum "$directory/image.tar" | cut -d ' ' -f 1)" \
      --arg binary "$(sha256sum "$directory/agent" | cut -d ' ' -f 1)" \
      '{commit:$commit,pipeline:$pipeline,job:$job,version:$version,sum:$sum,image:$image,
        archive:$archive,binary:$binary}' > "$directory/source.json"
    printf 'AGENT_BUILD_JOB_ID=%s\n' "$CI_JOB_ID" > "$directory/build.env"
    ;;
  load)
    jq -e --arg commit "${CI_COMMIT_SHA:?}" --arg version "$version" \
      --arg sum "$sum" --arg image "$image" --argjson pipeline "$CI_PIPELINE_ID" \
      --argjson job "${AGENT_BUILD_JOB_ID:?}" '
      .commit == $commit and .pipeline == $pipeline and .version == $version
      and .job == $job and .sum == $sum and .image == $image
      and (.archive | test("^[0-9a-f]{64}$"))
      and (.binary | test("^[0-9a-f]{64}$"))
      ' "$directory/source.json" >/dev/null
    for file in image.tar agent; do
      field=archive
      if [[ $file == agent ]]; then field=binary; fi
      digest=$(jq -er --arg field "$field" '.[$field]' "$directory/source.json")
      printf '%s  %s\n' "$digest" "$directory/$file" | sha256sum -c -
    done
    "${DURPDEPLOY_CONTAINER_RUNTIME:?}" load --input "$directory/image.tar"
    "${DURPDEPLOY_CONTAINER_RUNTIME}" image inspect "$image" >/dev/null
    chmod 0755 "$directory/agent"
    ;;
  *) echo 'Unknown agent image action' >&2; exit 2 ;;
esac
