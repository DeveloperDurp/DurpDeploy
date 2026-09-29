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
	'name: DURPDEPLOY_CONTAINER_RUNTIME' \
	'name: DURPDEPLOY_CONTAINER_URL' \
	'name: DURPDEPLOY_CONTAINER_NAMESPACE' \
	'name: container-runtime-ssh-sync' \
	'name: agent-identity' \
	'name: container-runtime-ssh' \
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
	helm template contract "$root/charts/durpdeploy" \
		--set containerRuntime.enabled=true \
		--set containerRuntime.type=docker \
		--set containerRuntime.url=ssh://exec@runtime.example/run/user/1234/docker.sock \
		--set containerRuntime.namespace=contract \
		--set containerRuntime.sshSecret=runtime-ssh \
		| grep -Fq 'name: container-runtime-ssh'
fi

printf '%s\n' 'agent Helm contract: PASS'
