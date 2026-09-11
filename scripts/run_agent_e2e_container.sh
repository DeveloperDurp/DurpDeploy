#!/usr/bin/env bash
set -euo pipefail

: "${DURPDEPLOY_AGENT_E2E_BINARY:?agent binary is required}"
: "${DURPDEPLOY_AGENT_E2E_CONTAINER:?container name is required}"
: "${DURPDEPLOY_AGENT_LISTEN_ADDR:?agent listen address is required}"
: "${DURPDEPLOY_AGENT_STATE_DIR:?agent state directory is required}"

unit="${DURPDEPLOY_AGENT_E2E_CONTAINER}.service"

cleanup() {
	local status=$?
	podman rm -f "$DURPDEPLOY_AGENT_E2E_CONTAINER" >/dev/null 2>&1 || true
	systemctl --user stop "$unit" >/dev/null 2>&1 || true
	systemctl --user reset-failed "$unit" >/dev/null 2>&1 || true
	podman unshare chown -R 0:0 "$DURPDEPLOY_AGENT_STATE_DIR" \
		>/dev/null 2>&1 || true
	return "$status"
}
trap cleanup EXIT
trap 'exit 143' INT TERM

mkdir -p "$DURPDEPLOY_AGENT_STATE_DIR"
chmod 0711 "$(dirname "$DURPDEPLOY_AGENT_STATE_DIR")"
podman unshare chown -R 10001:10001 "$DURPDEPLOY_AGENT_STATE_DIR"

systemd-run --user --unit="$unit" --property=Delegate=yes \
	/bin/sleep infinity >/dev/null
control_group=$(systemctl --user show -p ControlGroup --value "$unit")
cgroup_root="/sys/fs/cgroup$control_group"
mkdir "$cgroup_root/manager"
manager_pid=0
for _ in $(seq 1 100); do
	manager_pid=$(systemctl --user show -p MainPID --value "$unit")
	[[ $manager_pid != 0 ]] && break
	sleep 0.01
done
[[ $manager_pid != 0 ]] || {
	printf 'delegated systemd unit did not start: %s\n' "$unit" >&2
	exit 1
}
printf '%s' "$manager_pid" >"$cgroup_root/manager/cgroup.procs"
printf '+cpu +memory +pids' >"$cgroup_root/cgroup.subtree_control"
mkdir "$cgroup_root/durpdeploy"
printf '+cpu +memory +pids' >"$cgroup_root/durpdeploy/cgroup.subtree_control"
podman unshare chown -R 10001:10001 "$cgroup_root/durpdeploy"
mkdir "$cgroup_root/agent"

podman run --detach --name "$DURPDEPLOY_AGENT_E2E_CONTAINER" \
	--network host --cgroupns host --read-only \
	--security-opt no-new-privileges:true \
	--security-opt apparmor=unconfined \
	--cap-drop ALL \
	--cap-add SETUID --cap-add SETGID --cap-add SETPCAP \
	--cap-add SYS_ADMIN --cap-add SYS_CHROOT \
	--tmpfs /tmp:size=64m,mode=1777 \
	-v "$cgroup_root:/sys/fs/cgroup:rw" \
	-v "$DURPDEPLOY_AGENT_STATE_DIR:/var/lib/durpdeploy-agent:rw" \
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
for _ in $(seq 1 100); do
	grep -q '/libpod-' "/proc/$container_pid/cgroup" && break
	sleep 0.01
done
printf '%s' "$container_pid" >"$cgroup_root/agent/cgroup.procs"
grep -Fq "$control_group/agent" "/proc/$container_pid/cgroup"
podman unshare chown 10001:10001 \
	"$cgroup_root/cgroup.procs" "$cgroup_root/agent/cgroup.procs"
podman logs --follow "$DURPDEPLOY_AGENT_E2E_CONTAINER" &
logs_pid=$!
container_status=$(podman wait "$DURPDEPLOY_AGENT_E2E_CONTAINER")
wait "$logs_pid"
exit "$container_status"
