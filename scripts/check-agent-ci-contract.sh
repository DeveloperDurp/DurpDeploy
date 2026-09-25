#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
bash "$root/scripts/check-ci-contract.sh" "$@"

ci="$root/.github/workflows/ci.yml"
[[ -f "$ci" ]] || {
	printf 'agent CI contract: missing .github/workflows/ci.yml\n' >&2
	exit 1
}
for required in \
	'agent-rollout:' \
	'make agent-rollout-gate' \
	'agent-smoke:' \
	'agent-smoke-container' \
	'AGENT_E2E_EVIDENCE_DIR=/artifacts/agent-smoke' \
	'Dockerfile.mobile-browser'; do
	grep -Fq "$required" "$ci" || {
		printf 'agent CI contract: missing %s\n' "$required" >&2
		exit 1
	}
done

printf '%s\n' 'agent CI rollout contract: PASS'
