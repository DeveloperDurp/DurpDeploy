#!/usr/bin/env bash
# Execute the Podman lane under an actual non-root identity.
set -euo pipefail
[[ ${GITLAB_CI:-} == true && ${CI_RUNTIME:-} == podman ]]
if [[ $(cat /proc/sys/user/max_user_namespaces) == 0 ]]; then
  echo 'Rootless Podman requires user namespaces on the runner node. Ask the runner administrator to enable them.' >&2
  exit 77
fi
if [[ -f /proc/sys/kernel/unprivileged_userns_clone && $(cat /proc/sys/kernel/unprivileged_userns_clone) == 0 ]]; then
  echo 'Rootless Podman requires kernel.unprivileged_userns_clone=1 on the runner node.' >&2
  exit 1
fi
export XDG_RUNTIME_DIR=/tmp/durpdeploy-ci-podman
export DURPDEPLOY_CONTAINER_RUNTIME=podman
export DURPDEPLOY_CONTAINER_URL="unix://$XDG_RUNTIME_DIR/podman.sock"
export DOCKER_HOST=$DURPDEPLOY_CONTAINER_URL
export TESTCONTAINERS_RYUK_DISABLED=true
export CONTAINERS_STORAGE_CONF="$XDG_RUNTIME_DIR/storage.conf"
export CONTAINERS_CONF="$XDG_RUNTIME_DIR/containers.conf"
printf '[engine]\ncgroup_manager = "cgroupfs"\nevents_logger = "file"\n' > "$CONTAINERS_CONF"
cat > "$CONTAINERS_STORAGE_CONF" <<'TOML'
[storage]
driver = "vfs"
runroot = "/tmp/durpdeploy-ci-podman/runroot"
graphroot = "/tmp/durpdeploy-ci-podman/graphroot"
TOML
chown pwuser:pwuser "$CONTAINERS_STORAGE_CONF" "$CONTAINERS_CONF"
exec runuser -u pwuser -- bash -euo pipefail -c '
  [[ $(id -u) != 0 ]]
  podman --cgroup-manager=cgroupfs system service --time=0 "$DOCKER_HOST" &
  service_pid=$!
  trap '\''kill "$service_pid" 2>/dev/null || true; wait "$service_pid" || true'\'' EXIT
  for _ in {1..60}; do
    if podman --remote --url "$DOCKER_HOST" info >/dev/null 2>&1; then
      exec_ready=true
      break
    fi
    sleep 1
  done
  [[ ${exec_ready:-false} == true ]]
  podman info --format "{{.Host.Security.Rootless}}" | grep -qx true
  bash -euo pipefail
'
