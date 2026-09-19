#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
deployment="$root/charts/durpdeploy/templates/deployment.yaml"
service="$root/charts/durpdeploy/templates/service.yaml"
pvc="$root/charts/durpdeploy/templates/agent-identity-pvc.yaml"
values="$root/charts/durpdeploy/values.yaml"

for required in \
	'name: agent' \
	'containerPort: {{ .Values.agent.port }}' \
	'name: DURPDEPLOY_AGENT_LISTEN_ADDR' \
	'name: DURPDEPLOY_AGENT_PUBLIC_URL' \
	'name: DURPDEPLOY_AGENT_IDENTITY_DIR' \
	'name: agent-identity' \
	'claimName: {{ include "durpdeploy.agentIdentityClaimName" . }}'; do
	grep -Fq "$required" "$deployment" || {
		printf 'agent Helm contract: deployment missing %s\n' "$required" >&2
		exit 1
	}
done
grep -Fq 'targetPort: agent' "$service"
grep -Fq 'kind: PersistentVolumeClaim' "$pvc"
grep -Fq 'existingClaim:' "$values"
grep -Fq 'size: 1Gi' "$values"

if command -v helm >/dev/null 2>&1; then
	helm lint "$root/charts/durpdeploy" >/dev/null
	helm template contract "$root/charts/durpdeploy" | grep -Fq 'name: agent-identity'
fi

printf '%s\n' 'agent Helm contract: PASS'
