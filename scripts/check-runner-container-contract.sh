#!/usr/bin/env bash
set -euo pipefail

root=${RUNNER_CONTAINER_CONTRACT_ROOT:-.}
image=${RUNNER_CONTAINER_IMAGE:-durpdeploy:runner-contract}
state_volume="durpdeploy-runner-contract-state-$$"
cleanup() {
	podman volume rm -f "$state_volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

for file in Dockerfile Makefile; do
	if grep -Eqi 'setpriv|ambient-caps|inh-caps|(CAP_)?(SETPCAP|SYS_ADMIN|SYS_CHROOT)|apparmor.?unconfined|privileged:' "$root/$file"; then
		printf 'runner container contract: %s contains a forbidden privilege\n' "$file" >&2
		exit 1
	fi
done
if grep -Eqi 'chroot|syscall\.Mount|\.Chroot|SysProcAttr\.Credential' \
	"$root/internal/runner/runner.go" "$root/internal/runner/sandbox_linux.go"; then
	printf '%s\n' 'runner container contract: runner source changes privileges' >&2
	exit 1
fi
for file in compose.yml compose.example.yml; do
	python3 - "$root/$file" "$file" <<'PY'
import pathlib
import sys

import yaml

path = pathlib.Path(sys.argv[1])
name = sys.argv[2]
app = yaml.safe_load(path.read_text())["services"]["app"]

def fail(message):
    raise SystemExit(f"runner container contract: {name} {message}")

if str(app.get("privileged", False)).lower() == "true":
    fail("enables privileged mode")
if str(app.get("pid", "")).lower() == "host":
    fail("shares the host PID namespace")
if str(app.get("network_mode", "")).lower() == "host":
    fail("shares the host network")
if str(app.get("cgroupns", "")).lower() == "host":
    fail("shares the host cgroup namespace")
if app.get("read_only") is not True:
    fail("permits a writable image root")
if sorted(str(value).casefold() for value in app.get("cap_add", [])) != ["setgid", "setuid"]:
    fail("does not limit startup capabilities to SETUID and SETGID")
if [str(value).casefold() for value in app.get("cap_drop", [])] != ["all"]:
    fail("does not drop all capabilities")
if str(app.get("user", "")) != "0":
    fail("does not enable the socket-aware identity entrypoint")
app_text = str(app).casefold()
if any(value in app_text for value in ("setpriv", "ambient-caps", "inh-caps")):
    fail("contains a forbidden privilege")
security_options = [str(value).casefold() for value in app.get("security_opt", [])]
if any("unconfined" in value for value in security_options):
    fail("contains a forbidden privilege")
volumes = str(app.get("volumes", [])).casefold()
if "/var/run/docker.sock:/var/run/durpdeploy-runtime.sock" not in volumes:
    fail("does not mount the Docker socket at the runtime path")
if "/sys/fs/cgroup" in volumes:
    fail("mounts the host cgroup filesystem")
PY
done
grep -Fq 'chmod 0700 /data' "$root/Dockerfile"
grep -Fq 'ln -s /usr/bin/podman-remote /usr/bin/podman' "$root/Dockerfile"
grep -Fq 'docker-cli' "$root/Dockerfile"
grep -Fq 'su-exec' "$root/Dockerfile"
grep -Fq 'container-entrypoint' "$root/Dockerfile"
! grep -Fq 'openssh-client' "$root/Dockerfile"
! grep -Fq 'chmod g+rw "$socket"' "$root/container-entrypoint.sh"
grep -Fq 'DURPDEPLOY_CONTAINER_RUNTIME: podman' "$root/compose.podman.yml"
grep -Fq 'podman/podman.sock:/var/run/durpdeploy-runtime.sock' \
	"$root/compose.podman.yml"
grep -Fq 'label=disable' "$root/compose.podman.yml"

if [ "${RUNNER_CONTAINER_CONTRACT_STATIC_ONLY:-0}" = 1 ]; then
	printf '%s\n' 'runner container contract: static PASS'
	exit 0
fi

podman build -t "$image" "$root"
podman run --rm --user 0 --read-only --security-opt no-new-privileges:true \
	--cap-drop ALL --cap-add SETUID --cap-add SETGID \
	--memory 512m --cpus 1.0 --pids-limit 256 \
	--tmpfs /tmp:size=64m,mode=1777 \
	--volume "$state_volume:/data" \
	--entrypoint /bin/sh "$image" -ceu '
	exec su-exec 10001:10001 /bin/sh -ceu '\''
	test "$(id -u)" = 10001
	test -w /data
	test ! -w /
	test "$(getent passwd 10001 | cut -d : -f 6)" = /home/durpdeploy
	test -x /usr/bin/podman
	test -x /usr/bin/docker
	/usr/bin/podman --remote --url=unix:///run/podman/podman.sock --version
	printf private > /data/service-private
	chmod 0600 /data/service-private
	for capability_set in CapInh CapPrm CapEff CapAmb; do
		test "$(grep "^$capability_set:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 0000000000000000
	done
	test "$(grep '^CapBnd:' /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 00000000000000c0
	test "$(grep "^NoNewPrivs:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 1
	cat > /tmp/runner-probe.sh <<"EOF"
test "$(id -u)" = 10001
test -r /data/service-private
test ! -w /
for capability_set in CapInh CapPrm CapEff CapAmb; do
	test "$(grep "^$capability_set:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 0000000000000000
done
test "$(grep '^CapBnd:' /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 00000000000000c0
test "$(grep "^NoNewPrivs:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 1
test "$(cat /sys/fs/cgroup/memory.max)" = 536870912
test "$(cat /sys/fs/cgroup/pids.max)" = 256
test "$(cat /sys/fs/cgroup/cpu.max)" = "100000 100000"
EOF
	chmod 0755 /tmp/runner-probe.sh
	/bin/bash /tmp/runner-probe.sh
'\''
'

printf '%s\n' 'runner container contract: PASS'
