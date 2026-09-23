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
	[[ -n ${binary_volume:-} ]] &&
		podman volume rm -f "$binary_volume" >/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT
trap 'exit 143' INT TERM

network=(--network slirp4netns:allow_host_loopback=true)
if ! command -v slirp4netns >/dev/null 2>&1; then
	network=(--network pasta:--map-host-loopback,169.254.1.2)
fi

# Sandboxed filesystems can deny container read access to host bind mounts
# (exec of a mounted binary segfaults); stage the binary through a volume.
binary_volume="${DURPDEPLOY_AGENT_E2E_CONTAINER}-bin"
podman volume rm -f "$binary_volume" >/dev/null 2>&1 || true
podman volume create "$binary_volume" >/dev/null
tar -C "$(dirname -- "$DURPDEPLOY_AGENT_E2E_BINARY")" \
	-cf - "$(basename -- "$DURPDEPLOY_AGENT_E2E_BINARY")" |
	podman volume import "$binary_volume" -
binary_mount=(-v "$binary_volume:/usr/local/bin:ro")

podman run --detach --name "$DURPDEPLOY_AGENT_E2E_CONTAINER" \
	"${network[@]}" --read-only \
	--publish "127.0.0.1:$listen_port:$listen_port" \
	--security-opt no-new-privileges:true \
	--cap-drop ALL \
	--memory 512m --cpus 1.0 --pids-limit 128 \
	--tmpfs /tmp:size=64m,mode=1777 \
	-v "$DURPDEPLOY_AGENT_E2E_STATE_VOLUME:/var/lib/durpdeploy-agent" \
	"${binary_mount[@]}" \
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
