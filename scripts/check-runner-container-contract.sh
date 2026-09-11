#!/usr/bin/env bash
set -euo pipefail

root=${RUNNER_CONTAINER_CONTRACT_ROOT:-.}
image=${RUNNER_CONTAINER_IMAGE:-durpdeploy:runner-contract}
state_volume="durpdeploy-runner-contract-state-$$"
cleanup() {
	podman volume rm -f "$state_volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if [ -e "$root/server-entrypoint.sh" ]; then
	printf '%s\n' 'runner container contract: capability entrypoint remains' >&2
	exit 1
fi
for file in Dockerfile Makefile; do
	if grep -Eqi 'setpriv|--cap-add|ambient-caps|inh-caps|(CAP_)?(SETUID|SETGID|SETPCAP|SYS_ADMIN|SYS_CHROOT)|apparmor.?unconfined|privileged:' "$root/$file"; then
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
if app.get("cap_add"):
    fail("contains a forbidden privilege")
if [str(value).casefold() for value in app.get("cap_drop", [])] != ["all"]:
    fail("does not drop all capabilities")
if str(app.get("user", "")) != "10001:10001":
    fail("does not use the preselected service identity")
app_text = str(app).casefold()
if any(value in app_text for value in ("setpriv", "ambient-caps", "inh-caps")):
    fail("contains a forbidden privilege")
security_options = [str(value).casefold() for value in app.get("security_opt", [])]
if any("unconfined" in value for value in security_options):
    fail("contains a forbidden privilege")
volumes = str(app.get("volumes", [])).casefold()
if "docker.sock" in volumes or "podman.sock" in volumes:
    fail("mounts a container socket")
if "/sys/fs/cgroup" in volumes:
    fail("mounts the host cgroup filesystem")
PY
done
grep -Fq 'USER 10001' "$root/Dockerfile"
grep -Fq 'chmod 0700 /data' "$root/Dockerfile"

if [ "${RUNNER_CONTAINER_CONTRACT_STATIC_ONLY:-0}" = 1 ]; then
	printf '%s\n' 'runner container contract: static PASS'
	exit 0
fi

podman build -t "$image" "$root"
podman run --rm --read-only --security-opt no-new-privileges:true \
	--cap-drop ALL \
	--memory 512m --cpus 1.0 --pids-limit 256 \
	--tmpfs /tmp:size=64m,mode=1777 \
	--volume "$state_volume:/data" \
	--entrypoint /bin/sh "$image" -ceu '
	test "$(id -u)" = 10001
	test -w /data
	test ! -w /
	printf private > /data/service-private
	chmod 0600 /data/service-private
	for capability_set in CapInh CapPrm CapEff CapBnd CapAmb; do
		test "$(grep "^$capability_set:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 0000000000000000
	done
	test "$(grep "^NoNewPrivs:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 1
	cat > /tmp/runner-probe.sh <<"EOF"
test "$(id -u)" = 10001
test -r /data/service-private
test ! -w /
for capability_set in CapInh CapPrm CapEff CapBnd CapAmb; do
	test "$(grep "^$capability_set:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 0000000000000000
done
test "$(grep "^NoNewPrivs:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 1
test "$(cat /sys/fs/cgroup/memory.max)" = 536870912
test "$(cat /sys/fs/cgroup/pids.max)" = 256
test "$(cat /sys/fs/cgroup/cpu.max)" = "100000 100000"
EOF
	chmod 0755 /tmp/runner-probe.sh
	/bin/bash /tmp/runner-probe.sh
'

printf '%s\n' 'runner container contract: PASS'
