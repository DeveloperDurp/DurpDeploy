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
cgroup_parent=$(python3 "$(dirname "${BASH_SOURCE[0]}")/ci_rootless_cgroup.py")
printf '[containers]\ncgroup_parent = "%s"\n[engine]\ncgroup_manager = "cgroupfs"\nevents_logger = "file"\n' "$cgroup_parent" > "$CONTAINERS_CONF"
cat > "$CONTAINERS_STORAGE_CONF" <<'TOML'
[storage]
driver = "vfs"
runroot = "/tmp/durpdeploy-ci-podman/runroot"
graphroot = "/tmp/durpdeploy-ci-podman/graphroot"
TOML
chown pwuser:pwuser "$CONTAINERS_STORAGE_CONF" "$CONTAINERS_CONF"
printf '%s\n' "$$" > "/sys/fs/cgroup$cgroup_parent/processes/cgroup.procs"
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
  podman --version
  podman info --format "{{.Host.Security.Rootless}}" | grep -qx true
  podman info --format "rootless={{.Host.Security.Rootless}} cgroup_manager={{.Host.CgroupManager}} controllers={{json .Host.CgroupControllers}}"
  printf "Rootless CI process cgroup:\n"
  cat /proc/self/cgroup
  findmnt -n -o TARGET,FSROOT,FSTYPE,OPTIONS /sys/fs/cgroup
  for control in cgroup.controllers cgroup.subtree_control cgroup.procs pids.max; do
    file=/sys/fs/cgroup/$control
    if [[ -e $file ]]; then
      stat -c "%n owner=%u:%g mode=%a" "$file"
      if [[ -w $file ]]; then
        printf "%s writable=true\n" "$file"
      else
        printf "%s writable=false\n" "$file"
      fi
    fi
  done
  cat /sys/fs/cgroup/cgroup.controllers /sys/fs/cgroup/cgroup.subtree_control
  bash -euo pipefail
'
