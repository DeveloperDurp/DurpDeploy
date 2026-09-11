#!/usr/bin/env bash
set -euo pipefail

grep -Fq 'DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943' compose.example.yml
grep -Fq 'DURPDEPLOY_AGENT_PUBLIC_URL=https://<agent-control-host>' compose.example.yml
grep -Fq 'DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity' compose.example.yml
grep -Fq '"10943:10943"' compose.example.yml
for file in compose.yml compose.example.yml; do
	grep -Fq 'DURPDEPLOY_EXECUTION_BOUNDARY: service' "$file" ||
		grep -Fq 'DURPDEPLOY_EXECUTION_BOUNDARY=service' "$file"
	grep -Fq 'cap_drop: [ALL]' "$file"
	python3 - "$file" <<'PY'
import pathlib
import sys

import yaml

app = yaml.safe_load(pathlib.Path(sys.argv[1]).read_text())["services"]["app"]
if app.get("cap_add"):
    raise SystemExit("agent compose contract: app grants a Linux capability")
if str(app.get("user", "")) != "10001:10001":
    raise SystemExit("agent compose contract: app service identity is not fixed")
PY
	grep -Fq 'read_only: true' "$file"
	grep -Fq 'no-new-privileges:true' "$file"
	grep -Fq 'mode: 0400' "$file"
	if grep -Eq 'SYS_ADMIN|SYS_CHROOT|privileged:|apparmor.?unconfined|pid: host|network_mode: host' "$file"; then
		printf 'agent compose contract: forbidden privilege in %s\n' "$file" >&2
		exit 1
	fi
done
if grep -Eq -- '--network host|--cgroupns host|apparmor.?unconfined|SYS_ADMIN|SYS_CHROOT|/sys/fs/cgroup.*:rw' scripts/run_agent_e2e_container.sh; then
	printf '%s\n' 'agent compose contract: privileged E2E container configuration found' >&2
	exit 1
fi
printf '%s\n' 'agent compose contract: PASS'
