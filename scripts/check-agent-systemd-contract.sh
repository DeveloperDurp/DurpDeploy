#!/usr/bin/env bash
set -euo pipefail

unit=${AGENT_SYSTEMD_UNIT:-systemd/durpdeploy-agent.service}
required=(
	'User=durpdeploy-agent'
	'Group=durpdeploy-agent'
	'#   sudo useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin durpdeploy-runner'
	'EnvironmentFile=/etc/durpdeploy-agent.env'
	'ExecStart=/usr/local/bin/durpdeploy-agent'
	'StateDirectory=durpdeploy-agent'
	'StateDirectoryMode=0700'
	'NoNewPrivileges=true'
	'ProtectSystem=strict'
	'ProtectHome=true'
	'PrivateTmp=true'
	'PrivateDevices=true'
	'ProtectControlGroups=false'
	'Delegate=true'
	'RestrictNamespaces=true'
	'RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6'
	'MemoryMax=512M'
	'TasksMax=128'
	'CapabilityBoundingSet=CAP_SETUID CAP_SETGID CAP_SETPCAP CAP_SYS_ADMIN CAP_SYS_CHROOT'
	'AmbientCapabilities=CAP_SETUID CAP_SETGID CAP_SETPCAP CAP_SYS_ADMIN CAP_SYS_CHROOT'
)
for value in "${required[@]}"; do
	grep -Fqx "$value" "$unit" || {
		 echo "agent systemd contract: missing $value" >&2
		exit 1
	}
done

forbidden=(
	'PrivateNetwork=true'
	'BindReadOnlyPaths=/var/lib/durpdeploy'
	'BindReadOnlyPaths=/data'
	'docker.sock'
)
for value in "${forbidden[@]}"; do
	if grep -Fq "$value" "$unit"; then
		echo "agent systemd contract: forbidden $value" >&2
		exit 1
	fi
done

if command -v systemd-analyze >/dev/null 2>&1; then
	set +e
	output=$(systemd-analyze verify "$unit" 2>&1)
	status=$?
	set -e
	if [ "$status" -ne 0 ] && ! grep -Fq 'Command /usr/local/bin/durpdeploy-agent is not executable' <<<"$output"; then
		printf '%s\n' "$output" >&2
		exit "$status"
	fi
	if [ "$status" -ne 0 ]; then
		echo 'agent systemd contract: static PASS (agent binary is not installed)'
	else
		echo 'agent systemd contract: systemd-analyze PASS'
	fi
else
	echo 'agent systemd contract: static PASS (systemd-analyze unavailable)'
fi
