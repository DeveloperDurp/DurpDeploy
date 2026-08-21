#!/usr/bin/env bash
set -euo pipefail

root=${AGENT_DOCUMENTATION_CONTRACT_ROOT:-.}

require_text() {
	local file=$1 text=$2 description=$3
	if ! grep -Fq -- "$text" "$root/$file"; then
		echo "agent documentation contract: $description" >&2
		exit 1
	fi
}

require_absent() {
	local file=$1 text=$2 description=$3
	if grep -Fq -- "$text" "$root/$file"; then
		echo "agent documentation contract: $description" >&2
		exit 1
	fi
}

for file in README.md docs/deploy.md docs/agent-protocol.md docs/agents.md \
	docs/security.md docs/roles.md docs/attack-drill.md docs/backup-restore.md; do
	if [ ! -f "$root/$file" ]; then
		echo "agent documentation contract: missing $file" >&2
		exit 1
	fi
done

require_text README.md 'Remote agents' 'README must describe remote agents'
require_text README.md 'No SSH-based deployment transport' 'README must distinguish agents from SSH'
require_text docs/agents.md 'outbound-only' 'agent operator boundary is missing'
require_text docs/agent-protocol.md 'does not fall back to local execution' 'remote dispatch fallback must be explicit'
require_text docs/security.md 'Remote agents do not receive the server database' 'agent storage boundary is missing'
require_text docs/deploy.md 'DURPDEPLOY_AGENT_STATE_DIR=/var/lib/durpdeploy-agent' 'agent state directory example is missing'
require_text docs/backup-restore.md 'state is separate' 'agent state backup boundary is missing'
require_absent Makefile 'AGENT_LISTEN_ADDR' 'Makefile must not accept agent listener inputs'
require_absent Makefile 'AGENT_ENV_FILE' 'Makefile must not source agent environment files'

for variable in \
	'DURPDEPLOY_AGENT_STATE_DIR' \
	'DURPDEPLOY_AGENT_LISTEN_ADDR' \
	'DURPDEPLOY_AGENT_VERSION'; do
	require_text docs/agents.md "$variable" "source-supported variable $variable is missing"
done

for instruction in \
	'make build-agent' \
	'go run ./cmd/server' \
	'/usr/local/bin/durpdeploy-agent' \
	'docker compose --profile agent up -d --build agent' \
	'podman compose --profile agent up -d --build agent' \
	'systemctl enable --now durpdeploy-agent' \
	'journalctl -u durpdeploy-agent' \
	'pairing code' \
	'New agent' \
	'Re-pair' \
	'cancel_unconfirmed'; do
	require_text docs/agents.md "$instruction" "runnable agent instruction $instruction is missing"
done

for invariant in \
	'agent has no database' \
	'server owns the DurpDeploy database' \
	'not routed through Caddy' \
	'never falls back to local execution' \
	'one-time' \
	'port 10943' \
	'private state directory' \
	'After pairing, restart with `DURPDEPLOY_AGENT_STATE_DIR`' \
	'Do not use a central CA' \
	'Do not use a central CA, trust-on-first-' \
	'trust-all TLS'; do
	require_text docs/agents.md "$invariant" "agent safety invariant $invariant is missing"
done

require_absent README.md 'No remote deployment targets or SSH' 'stale no-remote claim remains'
require_absent docs/roles.md 'There is no project' 'stale project authorization claim remains'
require_absent docs/attack-drill.md 'P1 closes several more gaps' 'stale pre-agent security summary remains'
require_absent docs/attack-drill.md 'the secret variables stored in `release_variables.value`' 'stale plaintext secret claim remains'
require_absent compose.example.yml 'AKIA...' 'credential-shaped access key remains in examples'
require_absent docs/agents.md 'Caddy terminates port 10943' 'agent mTLS must not be documented through Caddy'
require_absent docs/agents.md 'Use trust-all TLS' 'agent docs must not recommend trust-all TLS'
require_absent docs/agents.md 'Use SSH for dispatch' 'agent docs must not recommend SSH dispatch'
require_absent docs/agents.md 'DURPDEPLOY_AGENT_SERVER_URL' 'agent docs must not require a manual server URL'
require_absent docs/agents.md 'DURPDEPLOY_AGENT_SERVER_FINGERPRINT' 'agent docs must not require a manual fingerprint'
require_absent docs/agents.md 'enrollment token' 'agent docs must not retain enrollment tokens'

if grep -RInE --exclude='check-agent-documentation-contract.sh' \
	'AKIA[0-9A-Z]{8,}|ghp_[A-Za-z0-9]{20,}|xox[bap]-[A-Za-z0-9-]{12,}' \
	"$root/README.md" "$root/docs" "$root/compose.example.yml"; then
	echo 'agent documentation contract: credential-shaped literal found' >&2
	exit 1
fi

printf '%s\n' 'agent documentation contract: PASS'
