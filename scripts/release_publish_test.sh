#!/usr/bin/env bash
# Called by release_images_test.sh with its real Git and OCI fixtures.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=${1:?}
export RELEASE_PUBLISH_TEST_WORK="$work"
older=${2:?}
newer=${CI_COMMIT_SHA:?}
engine=${DURPDEPLOY_CONTAINER_RUNTIME:-docker}
real_skopeo=$(command -v skopeo)
transport=$(mktemp -d)
registry=
cleanup() {
	local result=$?
	if [[ -n "$registry" ]]; then
		if [[ "$result" != 0 ]]; then
			"$engine" logs "$registry" >&2
		fi
		"$engine" rm -f "$registry" > /dev/null
	fi
	rm -rf "$transport"
}
trap cleanup EXIT
cat > "$transport/config.yml" <<'YAML'
version: 0.1
database:
  enabled: false
log:
  level: error
storage:
  filesystem:
    rootdirectory: /tmp/data
http:
  addr: :5000
YAML
chmod 755 "$transport"
chmod 644 "$transport/config.yml"
registry=$("$engine" run -d --user 0 --entrypoint /usr/bin/registry \
	-p 127.0.0.1::5000 --tmpfs /tmp \
	-v "$transport/config.yml:/config.yml:ro,Z" \
	harbor.durp.info/gitlab/gitlab-org/build/cng/gitlab-container-registry@sha256:d275d58502f89db09a5d6eda50862e9ad8621bc4ea924570b667f7e1a65e1264 \
	serve /config.yml)
port=$("$engine" port "$registry" 5000/tcp | sed 's/.*://')
export TEST_REGISTRY="localhost:$port/release-test"
export TEST_GITLAB_REGISTRY="localhost:$port/gitlab-release-test"
ready=false
for _ in {1..50}; do
	if curl -fsS "http://localhost:$port/v2/" > /dev/null 2>&1; then
		ready=true
		break
	fi
	sleep 0.1
done
[[ "$ready" == true ]]
mkdir -p "$transport/bin"
export TEST_SKOPEO="$real_skopeo"
export TEST_ENGINE="$engine"
if [[ "$engine" == docker ]]; then
	export TEST_DOCKER
	TEST_DOCKER=$(command -v docker)
fi
cat > "$transport/bin/skopeo" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
operation=$1
shift
arguments=()
for argument in "$@"; do
	argument=${argument//ghcr.io\/fixture\/project/$TEST_REGISTRY}
	arguments+=("${argument//registry.gitlab.example\/fixture\/project/$TEST_GITLAB_REGISTRY}")
done
case "$operation" in
  copy) flags=(--src-tls-verify=false --dest-tls-verify=false) ;;
  inspect|list-tags) flags=(--tls-verify=false) ;;
  *) exit 1 ;;
esac
exec "$TEST_SKOPEO" "$operation" "${flags[@]}" "${arguments[@]}"
SH
cat > "$transport/bin/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
arguments=()
for argument in "$@"; do
	argument=${argument//ghcr.io\/fixture\/project/$TEST_REGISTRY}
	arguments+=("${argument//registry.gitlab.example\/fixture\/project/$TEST_GITLAB_REGISTRY}")
done
if [[ "$TEST_ENGINE" == docker ]]; then
	exec "$TEST_DOCKER" "${arguments[@]}"
fi
exec "$TEST_ENGINE" run --rm --network host --entrypoint docker \
	harbor.durp.info/dockerhub/library/docker@sha256:1a4c7cb63513f349bdad01fcc6e0f3f2f67d37b9da86f14dc0d4a0942eecda00 "${arguments[@]}"
SH
chmod +x "$transport/bin/"*
export PATH="$transport/bin:$PATH"
export REGISTRY_OWNER=fixture IMAGE_NAME=project IS_RELEASE=true

# Given verified image archives, publish and verify the AMD64 platform digest.
export CI_COMMIT_SHA="$older"
bash "$root/scripts/publish_release_images.sh" "$work/verified" v1.0.0
skopeo inspect --raw docker://ghcr.io/fixture/project:v1.0.0 > "$transport/index.json"
for arch in amd64; do
	expected="sha256:$(sha256sum "$work/verified/$arch/manifest.json" | cut -d ' ' -f 1)"
	jq -e --arg arch "$arch" --arg digest "$expected" \
		'any(.manifests[]; .platform.architecture == $arch and .digest == $digest)' \
		"$transport/index.json" > /dev/null
done
echo 'PASS: real registry retains the scanned AMD64 platform digest'

# Given a normalized SemVer build tag, publish the same scanned manifests.
bash "$root/scripts/publish_release_images.sh" "$work/verified" v1.0.0_build.4
skopeo inspect --raw docker://ghcr.io/fixture/project:v1.0.0_build.4 > "$transport/build.json"
cmp "$transport/index.json" "$transport/build.json"
echo 'PASS: normalized SemVer build metadata publishes the scanned images'

# Given a newer release already exists, an older publisher cannot replace latest.
export CI_COMMIT_SHA="$newer"
bash "$root/scripts/publish_release_images.sh" "$work/verified" v1.0.0-build.4
skopeo inspect --raw docker://ghcr.io/fixture/project:v1.0.0-build.4 |
	jq -e --arg newer "$newer" '.annotations["org.opencontainers.image.revision"] == $newer' > /dev/null
skopeo inspect --raw docker://ghcr.io/fixture/project:v1.0.0_build.4 |
	jq -e --arg older "$older" '.annotations["org.opencontainers.image.revision"] == $older' > /dev/null
echo 'PASS: build metadata and prerelease tags publish separate source revisions'
bash "$root/scripts/publish_release_images.sh" "$work/verified" v2.0.0
export CI_COMMIT_SHA="$older"
bash "$root/scripts/publish_release_images.sh" "$work/verified" v1.1.0
skopeo inspect --raw docker://ghcr.io/fixture/project:latest |
	jq -e --arg newer "$newer" '.annotations["org.opencontainers.image.revision"] == $newer' > /dev/null
echo 'PASS: an older publisher cannot roll back latest'

# Given an immutable version exists, another source revision cannot replace it.
if bash "$root/scripts/publish_release_images.sh" "$work/verified" v2.0.0 > "$transport/conflict.log" 2>&1; then
	echo 'An existing version was overwritten.' >&2
	exit 1
fi
skopeo inspect --raw docker://ghcr.io/fixture/project:v2.0.0 |
	jq -e --arg newer "$newer" '.annotations["org.opencontainers.image.revision"] == $newer' > /dev/null
echo 'PASS: existing version tags reject different images'

# The second registry receives the same verified platform and index bytes.
export CI_REGISTRY_IMAGE=registry.gitlab.example/fixture/project
bash "$root/scripts/publish_release_images.sh" "$work/verified" v1.0.0 "$CI_REGISTRY_IMAGE"
skopeo inspect --raw "docker://$CI_REGISTRY_IMAGE:v1.0.0" > "$transport/gitlab.json"
cmp "$transport/index.json" "$transport/gitlab.json"
if bash "$root/scripts/publish_release_images.sh" "$work/verified" v1.0.0 registry.other.example/project \
  > "$transport/unrelated.log" 2>&1; then
	echo 'An unrelated publication target was accepted.' >&2
	exit 1
fi
echo 'PASS: both registries retain identical verified image indexes'

export TEST_HELM
TEST_HELM=$(command -v helm3)
cat > "$transport/bin/helm" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
arguments=()
for argument in "$@"; do
  argument=${argument//ghcr.io\/fixture\/charts/$TEST_REGISTRY\/charts}
  arguments+=("${argument//registry.gitlab.example\/fixture\/project/$TEST_GITLAB_REGISTRY}")
done
case $1 in push|pull) arguments+=(--plain-http) ;; esac
exec "$TEST_HELM" "${arguments[@]}"
SH
chmod +x "$transport/bin/helm"
mkdir -p "$work/chart"
printf 'apiVersion: v2\nname: durpdeploy\nversion: 1.2.3\n' > "$work/chart/Chart.yaml"
helm package "$work/chart" --destination "$work"
export CHART_REPO=charts
bash "$root/scripts/publish_release_chart.sh" "$work/durpdeploy-1.2.3.tgz" 1.2.3
printf 'apiVersion: v2\nname: durpdeploy\nversion: 1.2.3+build.4\n' > "$work/chart/Chart.yaml"
helm package "$work/chart" --destination "$work"
bash "$root/scripts/publish_release_chart.sh" "$work/durpdeploy-1.2.3+build.4.tgz" 1.2.3+build.4
echo 'PASS: both registries retain identical Helm package bytes'
