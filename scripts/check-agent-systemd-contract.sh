#!/usr/bin/env bash
set -euo pipefail

unit=systemd/durpdeploy.service
grep -Fq 'DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943' "$unit"
grep -Fq 'DURPDEPLOY_AGENT_PUBLIC_URL=https://<agent-control-host>' "$unit"
grep -Fq 'DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity' "$unit"
grep -Fq 'ReadWritePaths=/var/lib/durpdeploy /var/lib/durpdeploy/agent-identity' "$unit"
grep -Fq 'Environment=DURPDEPLOY_EXECUTION_BOUNDARY=service' "$unit"
grep -Fq 'AmbientCapabilities=CAP_SETUID CAP_SETGID CAP_SETPCAP' "$unit"
grep -Fq 'CapabilityBoundingSet=CAP_SETUID CAP_SETGID CAP_SETPCAP' "$unit"
grep -Fq 'NoNewPrivileges=true' "$unit"
grep -Fq 'ProtectSystem=strict' "$unit"
grep -Fq 'PrivateTmp=true' "$unit"
grep -Fq 'PrivateMounts=true' "$unit"
grep -Fq 'ProtectControlGroups=true' "$unit"
grep -Fq 'MemoryMax=512M' "$unit"
grep -Fq 'TasksMax=256' "$unit"
grep -Fq 'CPUQuota=100%' "$unit"
if grep -Eq 'CAP_SYS_(ADMIN|CHROOT)|chroot|bind-mount' "$unit"; then
	printf '%s\n' 'agent systemd contract: obsolete privileged sandbox found' >&2
	exit 1
fi
printf '%s\n' 'agent systemd contract: PASS'
