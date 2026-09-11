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

assert_rejected 'CapabilityBoundingSet=CAP_SYS_ADMIN' 'forbidden capability grant'
assert_rejected 'CapabilityBoundingSet=cap_sys_admin' 'forbidden capability grant'
assert_rejected 'AmbientCapabilities=CAP_SYS_CHROOT' 'forbidden capability grant'
assert_rejected 'AmbientCapabilities=cap_sys_chroot' 'forbidden capability grant'
assert_rejected 'CapabilityBoundingSet=CAP_SETUID' 'forbidden capability grant'
assert_rejected 'CapabilityBoundingSet=cap_setgid' 'forbidden capability grant'
assert_rejected 'AmbientCapabilities=CAP_SETPCAP' 'forbidden capability grant'
assert_rejected 'AmbientCapabilities=cap_net_admin' 'forbidden capability grant'
assert_rejected 'Delegate=true' 'forbidden Delegate=true'
assert_rejected 'ProtectControlGroups=false' 'forbidden ProtectControlGroups=false'
assert_rejected 'RestrictNamespaces=false' 'forbidden RestrictNamespaces=false'
assert_rejected 'BindReadOnlyPaths=/data' 'forbidden BindReadOnlyPaths=/data'
assert_rejected 'BindPaths=/var/run/docker.sock' 'forbidden docker.sock'
assert_rejected 'BindPaths=/run/podman/podman.sock' 'forbidden podman.sock'

comment_fixture=$(mktemp -d)
trap 'rm -rf "$comment_fixture"' EXIT
mkdir -p "$comment_fixture/systemd"
cp "$repo_root/systemd/durpdeploy.service" "$comment_fixture/systemd/"
python3 - "$comment_fixture/systemd/durpdeploy.service" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
text = path.read_text().replace(
    "[Service]\n",
    "[Service]\n# CAP_SYS_ADMIN and /run/podman/podman.sock are forbidden\n",
    1,
)
path.write_text(text)
PY
AGENT_SYSTEMD_CONTRACT_ROOT="$comment_fixture" bash "$checker" >/dev/null

printf '%s\n' 'agent systemd contract negative test: PASS'
