#!/usr/bin/env bash
set -euo pipefail

require_text() {
	grep -Fq -- "$2" "$1" || { echo "agent documentation contract: $3" >&2; exit 1; }
}

require_text README.md 'No SSH-based deployment targets' 'README still describes remote deployment as unsupported'
require_text README.md 'v0.1.0' 'agent compatibility is missing'
require_text docs/agents.md 'DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943' 'listener port is missing'
require_text docs/agents.md '/agent/v1/pairings/server-init' 'pairing callback is missing'
require_text docs/agents.md 'expire after 10 minutes' 'pairing expiry is incorrect'
require_text docs/agent-protocol.md 'completion_ack: false' 'pairing acknowledgement contract is missing'
require_text docs/agent-protocol.md 'does not fall back to local execution' 'fallback behavior is missing'
require_text docs/agent-protocol.md 'agent/1' 'protocol version is missing'
require_text docs/agents.md 'does **not** use a per-step `chroot`' 'container mode still promises per-step chroot'
require_text docs/agents.md 'operator or user is responsible' 'script responsibility warning is missing'
require_text docs/agents.md 'runner UID' 'container runner UID is missing'
require_text docs/agents.md 'NoNewPrivs' 'container privilege boundary is missing'
require_text docs/agents.md 'no host or control-plane database' 'container mount boundary is missing'
require_text docs/deploy.md 'does not use a per-step' 'control-plane deployment summary omits no-chroot mode'
if grep -Eq 'chroot.d|scratch chroot|chroot/namespaces' README.md docs/agents.md docs/deploy.md docs/security.md; then
	printf '%s\n' 'agent documentation contract: obsolete chroot claim found' >&2
	exit 1
fi
require_text docs/deploy.md 'operator is responsible' 'direct runner responsibility warning is missing'
printf '%s\n' 'agent documentation contract: PASS'
