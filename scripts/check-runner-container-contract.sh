#!/usr/bin/env bash
set -euo pipefail

root=${RUNNER_CONTAINER_CONTRACT_ROOT:-.}
image=${RUNNER_CONTAINER_IMAGE:-durpdeploy:runner-contract}
state_volume="durpdeploy-runner-contract-state-$$"
cleanup() {
	podman volume rm -f "$state_volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

for file in Dockerfile server-entrypoint.sh compose.yml compose.example.yml; do
	if grep -Eq "SYS_ADMIN|SYS_CHROOT|apparmor.?unconfined|privileged:|pid:[[:space:]]*['\"]?host['\"]?|network_mode:[[:space:]]*['\"]?host['\"]?" "$root/$file"; then
		printf 'runner container contract: %s contains a forbidden privilege\n' "$file" >&2
		exit 1
	fi
done
if grep -Eqi 'chroot|syscall\.Mount|\.Chroot' \
	"$root/internal/runner/runner.go" "$root/internal/runner/sandbox_linux.go"; then
	printf '%s\n' 'runner container contract: runner source invokes chroot' >&2
	exit 1
fi
for file in compose.yml compose.example.yml; do
	grep -Fq 'read_only: true' "$root/$file"
	if grep -Eq "read_only:[[:space:]]*['\"]?false['\"]?" \
		"$root/$file"; then
		printf 'runner container contract: %s permits a writable image root\n' \
			"$file" >&2
		exit 1
	fi
	if grep -Fq '/var/run/docker.sock' "$root/$file"; then
		printf 'runner container contract: %s mounts a container socket\n' \
			"$file" >&2
		exit 1
	fi
done
grep -Fq 'durpdeploy-runner' "$root/Dockerfile"
grep -Fq 'chmod 0700 /data' "$root/Dockerfile"

podman build -t "$image" "$root"
podman run --rm --read-only --security-opt no-new-privileges:true \
	--cap-drop ALL \
	--cap-add SETUID --cap-add SETGID --cap-add SETPCAP \
	--memory 512m --cpus 1.0 --pids-limit 256 \
	--tmpfs /tmp:size=64m,mode=1777 \
	--volume "$state_volume:/data" \
	"$image" sh -ceu '
	test "$(id -u)" = 10001
	test -w /data
	test ! -w /
	printf private > /data/service-private
	chmod 0600 /data/service-private
	cat > /tmp/runner-probe.sh <<"EOF"
test "$(id -u)" = 10002
test ! -r /data/service-private
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
	setpriv --reuid=10002 --regid=10002 --clear-groups \
		--bounding-set=-all --inh-caps=-all --ambient-caps=-all \
		--no-new-privs -- /bin/bash /tmp/runner-probe.sh
'

printf '%s\n' 'runner container contract: PASS'
