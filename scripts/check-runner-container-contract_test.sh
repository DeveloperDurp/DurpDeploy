#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
checker="$repo_root/scripts/check-runner-container-contract.sh"

if ! grep -Fq 'RUNNER_CONTAINER_CONTRACT_ROOT' "$checker"; then
	echo 'runner container contract negative test: checker has no fixture root' >&2
	exit 1
fi

assert_rejected() {
	local target=$1 payload=$2 diagnostic=$3 fixture
	fixture=$(mktemp -d)
	mkdir -p "$fixture/internal/runner"
	cp "$repo_root/Dockerfile" "$repo_root/Makefile" \
		"$repo_root/compose.yml" \
		"$repo_root/compose.example.yml" "$fixture/"
	cp "$repo_root/internal/runner/runner.go" \
		"$repo_root/internal/runner/sandbox_linux.go" "$fixture/internal/runner/"
	printf '\n%s\n' "$payload" >> "$fixture/$target"
	if output=$(RUNNER_CONTAINER_CONTRACT_ROOT="$fixture" \
		bash "$checker" 2>&1); then
		echo "runner container contract negative test: accepted $diagnostic" >&2
		rm -rf "$fixture"
		exit 1
	fi
	if ! grep -Fq "$diagnostic" <<<"$output"; then
		printf 'runner container contract negative test: missing diagnostic %s\n%s\n' \
			"$diagnostic" "$output" >&2
		rm -rf "$fixture"
		exit 1
	fi
	rm -rf "$fixture"
}

assert_compose_rejected() {
	local value=$1 diagnostic=$2 fixture
	fixture=$(mktemp -d)
	mkdir -p "$fixture/internal/runner"
	cp "$repo_root/Dockerfile" "$repo_root/Makefile" \
		"$repo_root/compose.yml" \
		"$repo_root/compose.example.yml" "$fixture/"
	cp "$repo_root/internal/runner/runner.go" \
		"$repo_root/internal/runner/sandbox_linux.go" "$fixture/internal/runner/"
	python3 - "$fixture/compose.yml" "$value" <<'PY'
import pathlib
import sys

import yaml

path = pathlib.Path(sys.argv[1])
document = yaml.safe_load(path.read_text())
key, value = sys.argv[2].split("=", 1)
document["services"]["app"][key] = yaml.safe_load(value)
path.write_text(yaml.safe_dump(document, sort_keys=False))
PY
	if output=$(RUNNER_CONTAINER_CONTRACT_ROOT="$fixture" \
		RUNNER_CONTAINER_CONTRACT_STATIC_ONLY=1 bash "$checker" 2>&1); then
		echo "runner container contract negative test: accepted $diagnostic" >&2
		rm -rf "$fixture"
		exit 1
	fi
	if ! grep -Fq "$diagnostic" <<<"$output"; then
		printf 'runner container contract negative test: missing diagnostic %s\n%s\n' \
			"$diagnostic" "$output" >&2
		rm -rf "$fixture"
		exit 1
	fi
	rm -rf "$fixture"
}

assert_rejected internal/runner/runner.go '// chroot' \
	'runner source changes privileges'
assert_rejected internal/runner/runner.go '// SysProcAttr.Credential' \
	'runner source changes privileges'
assert_rejected server-entrypoint.sh 'setpriv' \
	'capability entrypoint remains'
assert_rejected Dockerfile 'SYS_CHROOT' 'Dockerfile contains a forbidden privilege'
assert_rejected Dockerfile 'SYS_ADMIN' 'Dockerfile contains a forbidden privilege'
assert_compose_rejected 'privileged=true' 'compose.yml enables privileged mode'
assert_compose_rejected 'privileged="true"' 'compose.yml enables privileged mode'
assert_compose_rejected 'pid=host' 'compose.yml shares the host PID namespace'
assert_compose_rejected 'pid="host"' 'compose.yml shares the host PID namespace'
assert_compose_rejected 'network_mode=host' 'compose.yml shares the host network'
assert_compose_rejected 'network_mode="host"' 'compose.yml shares the host network'
assert_compose_rejected 'read_only=false' 'compose.yml permits a writable image root'
assert_compose_rejected 'read_only="false"' 'compose.yml permits a writable image root'
for capability in SETUID setuid CAP_SETUID cap_setuid \
	SETGID setgid CAP_SETGID cap_setgid \
	SETPCAP setpcap CAP_SETPCAP cap_setpcap \
	SYS_ADMIN sys_admin CAP_SYS_ADMIN cap_sys_admin \
	NET_ADMIN net_admin CAP_NET_ADMIN cap_net_admin; do
	assert_compose_rejected "cap_add=[\"$capability\"]" \
		'compose.yml contains a forbidden privilege'
done
assert_compose_rejected 'cgroupns=host' \
	'compose.yml shares the host cgroup namespace'
assert_compose_rejected 'volumes=["/var/run/docker.sock:/var/run/docker.sock"]' \
	'compose.yml mounts a container socket'
assert_compose_rejected 'volumes=["/run/podman/podman.sock:/run/podman/podman.sock"]' \
	'compose.yml mounts a container socket'

comment_fixture=$(mktemp -d)
trap 'rm -rf "$comment_fixture"' EXIT
mkdir -p "$comment_fixture/internal/runner"
	cp "$repo_root/Dockerfile" "$repo_root/Makefile" \
	"$repo_root/compose.yml" \
	"$repo_root/compose.example.yml" "$comment_fixture/"
cp "$repo_root/internal/runner/runner.go" \
	"$repo_root/internal/runner/sandbox_linux.go" \
	"$comment_fixture/internal/runner/"
printf '%s\n' \
	'# privileged: true; cap_add: [sys_admin]; /run/podman/podman.sock' \
	>> "$comment_fixture/compose.yml"
RUNNER_CONTAINER_CONTRACT_ROOT="$comment_fixture" \
	RUNNER_CONTAINER_CONTRACT_STATIC_ONLY=1 bash "$checker" >/dev/null

if grep -RqiE 'setpriv|--cap-add|ambient-caps|inh-caps' \
	"$repo_root/Dockerfile"; then
	echo 'runner container contract negative test: capability bootstrap remains' >&2
	exit 1
fi

printf '%s\n' 'runner container contract negative test: PASS'
