#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -r -- "$tmp"' EXIT
export DEV_AGENT_TEST_LOG="$tmp/commands"
cat >"$tmp/docker" <<'ENGINE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$DEV_AGENT_TEST_LOG"
case "$1" in
pull) exit "${DEV_AGENT_TEST_FAIL_PULL:-0}" ;;
stop) exit "${DEV_AGENT_TEST_FAIL_STOP:-0}" ;;
esac
ENGINE
chmod 0755 "$tmp/docker"
cp "$tmp/docker" "$tmp/podman"
python3 -c 'import socket,sys
with socket.socket(socket.AF_UNIX) as listener:
    listener.bind(sys.argv[1])' "$tmp/runtime.sock"

agent_version=$(awk '$1 == "github.com/DeveloperDurp/durpdeploy-agent" { print $2 }' "$root/go.mod")
agent_revision=${agent_version##*-}
[[ $agent_revision =~ ^[0-9a-f]{12}$ ]]
expected_tag=${agent_revision:0:7}
expected_image="ghcr.io/developerdurp/durpdeploy-agent:$expected_tag"

run_make() {
	make --no-print-directory -C "$root" "$@" \
		DEV_CONTAINER_ENGINE="$engine" \
		DEV_AGENT_CONTAINER=isolated-agent \
		DEV_AGENT_STATE_VOLUME=isolated-state >"$tmp/output" 2>&1
}

for engine in "$tmp/docker" "$tmp/podman"; do
    : >"$DEV_AGENT_TEST_LOG"
    run_make dev-agent
    if ! grep -Fxq "pull $expected_image" "$DEV_AGENT_TEST_LOG" ||
        ! grep -Fq -- "--env DURPDEPLOY_AGENT_VERSION=$expected_tag $expected_image" "$DEV_AGENT_TEST_LOG"; then
        printf 'FAIL: default agent must use source pin %s\n' "$expected_image" >&2
        cat "$DEV_AGENT_TEST_LOG" >&2
        exit 1
    fi
    if grep -q 'DURPDEPLOY_AGENT_CONTAINER_ENABLED\|/run/durpdeploy/runtime.sock' "$DEV_AGENT_TEST_LOG"; then
        echo 'FAIL: default dev agent received runtime access' >&2; exit 1
    fi
    : >"$DEV_AGENT_TEST_LOG"
    if run_make dev-agent DEV_AGENT_RUNTIME_SOCKET="$tmp/missing.sock"; then
        echo 'FAIL: missing runtime socket accepted' >&2; exit 1
    fi
    [[ ! -s "$DEV_AGENT_TEST_LOG" ]]
    run_make dev-agent DEV_AGENT_RUNTIME_SOCKET="$tmp/runtime.sock"
    grep -Fq -- "--volume $tmp/runtime.sock:/run/durpdeploy/runtime.sock:ro" "$DEV_AGENT_TEST_LOG"
    grep -Fq -- '--env DURPDEPLOY_AGENT_CONTAINER_ENABLED=true' "$DEV_AGENT_TEST_LOG"
    grep -Fq -- '--env DURPDEPLOY_AGENT_CONTAINER_SOCKET=unix:///run/durpdeploy/runtime.sock' "$DEV_AGENT_TEST_LOG"
    if [[ ${engine##*/} == podman ]]; then
        grep -Fq -- '--userns=keep-id:uid=10001,gid=10001' "$DEV_AGENT_TEST_LOG"
        grep -Fq -- '--volume isolated-state:/var/lib/durpdeploy-agent:U' "$DEV_AGENT_TEST_LOG"
    else
        grep -Fq -- "--group-add=$(stat -c %g "$tmp/runtime.sock")" "$DEV_AGENT_TEST_LOG"
    fi
	# An operator-selected image takes precedence over the default pin.
	: >"$DEV_AGENT_TEST_LOG"
	run_make dev-agent DEV_AGENT_IMAGE=localhost/operator-agent:override
	grep -Fxq 'pull localhost/operator-agent:override' "$DEV_AGENT_TEST_LOG"
	grep -Fq -- '--env DURPDEPLOY_AGENT_VERSION=override localhost/operator-agent:override' "$DEV_AGENT_TEST_LOG"
	# A failed pull must leave any existing container and state untouched.
	: >"$DEV_AGENT_TEST_LOG"
	if DEV_AGENT_TEST_FAIL_PULL=31 run_make dev-agent; then
		printf 'FAIL: failed image pull was accepted\n' >&2
		exit 1
	fi
	if grep -Eq '^(run|stop|volume rm) ' "$DEV_AGENT_TEST_LOG"; then
		printf 'FAIL: failed pull changed container or identity\n' >&2
		exit 1
	fi

	# Ordinary teardown retains identity and only stops the named agent.
	: >"$DEV_AGENT_TEST_LOG"
	run_make dev-agent-down
	grep -Fxq 'stop isolated-agent' "$DEV_AGENT_TEST_LOG"
	if grep -q '^volume rm ' "$DEV_AGENT_TEST_LOG"; then
		printf 'FAIL: ordinary teardown deleted pairing state\n' >&2
		exit 1
	fi

	# Reset removes the requested identity only after stop succeeds.
	: >"$DEV_AGENT_TEST_LOG"
	run_make dev-agent-reset
	grep -Fxq 'stop isolated-agent' "$DEV_AGENT_TEST_LOG"
	grep -Fxq 'volume rm isolated-state' "$DEV_AGENT_TEST_LOG"
	: >"$DEV_AGENT_TEST_LOG"
	if DEV_AGENT_TEST_FAIL_STOP=31 run_make dev-agent-reset; then
		printf 'FAIL: failed stop was accepted\n' >&2
		exit 1
	fi
	if grep -q '^volume rm ' "$DEV_AGENT_TEST_LOG"; then
		printf 'FAIL: reset deleted identity after stop failed\n' >&2
		exit 1
	fi
done
printf 'Development agent lifecycle contract: PASS\n'
