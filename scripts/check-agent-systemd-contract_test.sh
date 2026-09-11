#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
checker="$repo_root/scripts/check-agent-systemd-contract.sh"

assert_rejected() {
	local setting=$1 diagnostic=$2 fixture output
	fixture=$(mktemp -d)
	mkdir -p "$fixture/systemd"
	cp "$repo_root/systemd/durpdeploy.service" "$fixture/systemd/"
	python3 - "$fixture/systemd/durpdeploy.service" "$setting" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
text = path.read_text()
text = text.replace("[Service]\n", "[Service]\n" + sys.argv[2] + "\n", 1)
path.write_text(text)
PY
	if output=$(AGENT_SYSTEMD_CONTRACT_ROOT="$fixture" bash "$checker" 2>&1); then
		echo "agent systemd contract negative test: accepted $setting" >&2
		rm -rf "$fixture"
		exit 1
	fi
	if ! grep -Fq "$diagnostic" <<<"$output"; then
		printf 'agent systemd contract negative test: missing diagnostic %s\n%s\n' \
			"$diagnostic" "$output" >&2
		rm -rf "$fixture"
		exit 1
	fi
	rm -rf "$fixture"
}

assert_rejected 'CapabilityBoundingSet=CAP_SYS_ADMIN' 'forbidden CAP_SYS_ADMIN'
assert_rejected 'AmbientCapabilities=CAP_SYS_CHROOT' 'forbidden CAP_SYS_CHROOT'
assert_rejected 'Delegate=true' 'forbidden Delegate=true'
assert_rejected 'BindReadOnlyPaths=/data' 'forbidden BindReadOnlyPaths=/data'
assert_rejected 'BindPaths=/var/run/docker.sock' 'forbidden docker.sock'

printf '%s\n' 'agent systemd contract negative test: PASS'
