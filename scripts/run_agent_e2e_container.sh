#!/usr/bin/env bash
set -euo pipefail

: "${DURPDEPLOY_AGENT_E2E_BINARY:?agent binary is required}"
: "${DURPDEPLOY_AGENT_E2E_CONTAINER:?container name is required}"
: "${DURPDEPLOY_AGENT_E2E_STATE_VOLUME:?state volume is required}"
: "${DURPDEPLOY_AGENT_LISTEN_ADDR:?agent listen address is required}"
listen_port=${DURPDEPLOY_AGENT_LISTEN_ADDR##*:}

cleanup() {
	local status=$?
	podman rm -f "$DURPDEPLOY_AGENT_E2E_CONTAINER" >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT
trap 'exit 143' INT TERM

podman run --detach --name "$DURPDEPLOY_AGENT_E2E_CONTAINER" \
	--network slirp4netns:allow_host_loopback=true --read-only \
	--publish "127.0.0.1:$listen_port:$listen_port" \
	--security-opt no-new-privileges:true \
	--cap-drop ALL \
	--memory 512m --cpus 1.0 --pids-limit 128 \
	--tmpfs /tmp:size=64m,mode=1777 \
	-v "$DURPDEPLOY_AGENT_E2E_STATE_VOLUME:/var/lib/durpdeploy-agent" \
	-v "$DURPDEPLOY_AGENT_E2E_BINARY:/usr/local/bin/durpdeploy-agent:ro" \
	-e DURPDEPLOY_AGENT_LISTEN_ADDR \
	-e DURPDEPLOY_AGENT_STATE_DIR=/var/lib/durpdeploy-agent \
	-e DURPDEPLOY_AGENT_VERSION \
	-e DURPDEPLOY_EXTRA_SCRUB_PATTERNS \
	-e LANG \
	localhost/durpdeploy-agent:contract >/dev/null
container_pid=0
for _ in $(seq 1 100); do
	container_pid=$(podman inspect --format '{{.State.Pid}}' \
		"$DURPDEPLOY_AGENT_E2E_CONTAINER")
	[[ $container_pid != 0 ]] && break
	container_status=$(podman inspect --format '{{.State.Status}}' \
		"$DURPDEPLOY_AGENT_E2E_CONTAINER")
	[[ $container_status == running || $container_status == created ]] || break
	sleep 0.01
done
if [[ $container_pid == 0 ]]; then
	podman logs "$DURPDEPLOY_AGENT_E2E_CONTAINER" >&2 || true
	printf 'agent container did not start: status=%s\n' \
		"${container_status:-unknown}" >&2
	exit 1
fi
podman logs --follow "$DURPDEPLOY_AGENT_E2E_CONTAINER" &
logs_pid=$!
container_status=$(podman wait "$DURPDEPLOY_AGENT_E2E_CONTAINER")
wait "$logs_pid"
exit "$container_status"
