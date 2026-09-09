#!/usr/bin/env bash
set -euo pipefail

unit=systemd/durpdeploy.service
grep -Fq 'DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943' "$unit"
grep -Fq 'DURPDEPLOY_AGENT_PUBLIC_URL=https://<agent-control-host>' "$unit"
grep -Fq 'DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity' "$unit"
grep -Fq 'ReadWritePaths=/var/lib/durpdeploy /var/lib/durpdeploy/agent-identity' "$unit"
printf '%s\n' 'agent systemd contract: PASS'
