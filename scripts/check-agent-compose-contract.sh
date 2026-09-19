#!/usr/bin/env bash
set -euo pipefail

for file in compose.yml compose.example.yml; do
	app=$(awk '
		/^  app:$/ { in_app=1; next }
		/^  [[:alnum:]_-]+:$/ && in_app { exit }
		in_app { print }
	' "$file")
	caddy=$(awk '
		/^  caddy:$/ { in_caddy=1; next }
		/^  [[:alnum:]_-]+:$/ && in_caddy { exit }
		in_caddy { print }
	' "$file")
	grep -Fq 'DURPDEPLOY_AGENT_LISTEN_ADDR' <<<"$app"
	grep -Fq '0.0.0.0:10943' <<<"$app"
	grep -Fq 'DURPDEPLOY_AGENT_PUBLIC_URL' <<<"$app"
	grep -Fq 'https://localhost:10943' <<<"$app"
	grep -Fq 'DURPDEPLOY_AGENT_IDENTITY_DIR' <<<"$app"
	grep -Fq '/var/lib/durpdeploy/agent-identity' <<<"$app"
	grep -Fq '"10943:10943"' <<<"$app"
	grep -Fq 'durpdeploy-agent-identity:/var/lib/durpdeploy/agent-identity' <<<"$app"
	if grep -Fq '10943' <<<"$caddy"; then
		printf 'agent compose contract: Caddy owns agent port in %s\n' "$file" >&2
		exit 1
	fi
	grep -Fq 'durpdeploy-agent-identity:' "$file"
	grep -Fq 'DURPDEPLOY_EXECUTION_BOUNDARY: service' "$file" ||
		grep -Fq 'DURPDEPLOY_EXECUTION_BOUNDARY=service' "$file"
	grep -Fq 'cap_drop: [ALL]' "$file"
	grep -Fq 'user: "10001:10001"' <<<"$app"
	if grep -Eq '^[[:space:]]+cap_add:' <<<"$app"; then
		printf 'agent compose contract: app grants a Linux capability in %s\n' "$file" >&2
		exit 1
	fi
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
