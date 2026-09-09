#!/usr/bin/env bash
set -euo pipefail

grep -Fq 'DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943' compose.example.yml
grep -Fq 'DURPDEPLOY_AGENT_PUBLIC_URL=https://<agent-control-host>' compose.example.yml
grep -Fq 'DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity' compose.example.yml
grep -Fq '"10943:10943"' compose.example.yml
printf '%s\n' 'agent compose contract: PASS'
