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
		"$repo_root/server-entrypoint.sh" "$repo_root/compose.yml" \
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

assert_rejected internal/runner/runner.go '// chroot' \
	'runner source invokes chroot'
assert_rejected Dockerfile 'SYS_CHROOT' 'Dockerfile contains a forbidden privilege'
assert_rejected Dockerfile 'SYS_ADMIN' 'Dockerfile contains a forbidden privilege'
assert_rejected compose.yml 'privileged: true' \
	'compose.yml contains a forbidden privilege'
assert_rejected compose.yml 'pid: host' \
	'compose.yml contains a forbidden privilege'
assert_rejected compose.yml 'network_mode: host' \
	'compose.yml contains a forbidden privilege'
assert_rejected compose.yml 'network_mode: "host"' \
	'compose.yml contains a forbidden privilege'
assert_rejected compose.yml 'read_only: false' \
	'compose.yml permits a writable image root'
assert_rejected compose.yml 'read_only: "false"' \
	'compose.yml permits a writable image root'
assert_rejected compose.yml '/var/run/docker.sock:/var/run/docker.sock' \
	'compose.yml mounts a container socket'

printf '%s\n' 'runner container contract negative test: PASS'
