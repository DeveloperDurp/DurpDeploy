#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
bash "$root/scripts/check-ci-contract.sh" "$@"

ci="$root/.gitlab/ci/test.yml"
[[ -f "$ci" ]] || {
	printf 'agent CI contract: missing .gitlab/ci/test.yml\n' >&2
	exit 1
}
for required in \
	'agent-rollout:' \
	'bash scripts/ci_test_shard.sh "rollout-$ROLLOUT_SHARD"' \
	'agent-smoke:' \
	'agent_smoke_container.sh' \
	'AGENT_E2E_EVIDENCE_DIR=' \
	'bash scripts/ci_rootless.sh'; do
	grep -Fq "$required" "$ci" || {
		printf 'agent CI contract: missing %s\n' "$required" >&2
		exit 1
	}
done

grep -Fq 'make agent-rollout-gate' "$root/scripts/ci_test_shard.sh" || {
	printf '%s\n' 'agent CI contract: dispatcher is missing rollout gate' >&2
	exit 1
}

printf '%s\n' 'agent CI rollout contract: PASS'
