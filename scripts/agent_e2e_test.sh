#!/usr/bin/env bash
set -euo pipefail

umask 077

ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
RUN_ID=${DURPDEPLOY_AGENT_E2E_RUN_ID:-"$(date -u +%Y%m%dT%H%M%SZ)-$$"}
RUN_DIR=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-agent-e2e.XXXXXX")
ARTIFACT_DIR=${AGENT_E2E_ARTIFACT_DIR:-"$ROOT/.omo/evidence/task-9-agent-label-routing/agent-e2e/$RUN_ID-$$"}

cleanup() {
    local status=$?
	rm -rf -- "$RUN_DIR"
	printf '{"temporaryDirectoryRemoved":true,"status":%d}\n' "$status" >"$ARTIFACT_DIR/cleanup.json"
	return "$status"
}
trap cleanup EXIT INT TERM

redact() {
	sed -E \
		-e 's/(ddp_pat_[[:alnum:]_-]+)/<redacted>/g' \
		-e 's/([Tt]oken|[Ss]ecret|[Pp]assword|[Cc]laim)[=:][^[:space:],]*/\1=<redacted>/g' \
		-e 's/-----BEGIN [^-]+-----[^-]*-----END [^-]+-----/<redacted-certificate>/g'
}

result() {
	local id=$1 status=$2 source=$3 runtime_e2e=${4:-true}
	printf '{"id":"%s","status":"%s","source":"%s","runtime_e2e":%s}\n' \
		"$id" "$status" "$source" "$runtime_e2e"
}

run_scenario() {
    local id=$1 package=$2 pattern=$3
	local log="$ARTIFACT_DIR/$id.log"
	if go test -timeout 180s -count=1 -v -run "$pattern" "$package" >"$log" 2>&1 && \
		grep -q '^--- PASS:' "$log"; then
		result "$id" pass "go-test"
	else
		redact <"$log" >&2
		result "$id" fail "go-test"
        return 1
    fi
}

browser_proof() {
    local log="$ARTIFACT_DIR/full-routing.txt"
    if AGENT_BROWSER_OUTPUT_DIR="$ARTIFACT_DIR/browser" \
        node scripts/agent_admin_browser_proof.mjs --scenario full-routing >"$log" 2>&1; then
        result "full-routing-three-real-agents" pass "native-playwright"
    else
        redact <"$log" >&2
        result "full-routing-three-real-agents" fail "native-playwright"
        return 1
    fi
}

secret_scan() {
	if grep -R -E --exclude='*.png' \
		'ddp_pat_[[:alnum:]_-]+|claim_token["=:]' \
		"$ARTIFACT_DIR" >/dev/null; then
		printf '%s\n' '{"secret_scan":"failed"}'
		return 1
	fi
	printf '%s\n' '{"secret_scan":"passed"}'
}

main() {
	mkdir -p -- "$ARTIFACT_DIR"
	run_scenario remote-dispatch-lifecycle github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient '^TestClient_polls_and_acknowledges_cancellation$'

	run_scenario matching ./internal/agentserver '^TestPoll_claimedPayloadOpensOnlyForClaimedIdentity$'
    run_scenario logs ./internal/agentserver '^TestLifecycle_runsGuardedStartHeartbeatLogsResult$'
    run_scenario cancel ./internal/agentserver '^TestLifecycle_acknowledgesCancellationAndRecordsLateResult$'
    run_scenario cancellation-tree \
      github.com/DeveloperDurp/durpdeploy-agent/cmd/agent \
      '^TestAgentSubprocess_sigtermKillsSpawnedChild$'
    run_scenario wrong-pin github.com/DeveloperDurp/durpdeploy-agent/transport '^TestNewClientConfig_rejectsWrongPinHostnameAndExpiry$'
	run_scenario rotate github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient '^TestClient_persists_staged_server_pin_from_heartbeat$'
    run_scenario restart-before-start ./internal/agentserver '^TestMaintain_reclaimsOnlyExpiredUnstartedClaims$'
    run_scenario restart-after-start ./internal/agentserver '^TestMaintain_losesStartedWorkOnceAcrossRestartAndRecordsLateResult$'
	browser_proof
	secret_scan
	printf '%s\n' 'agent E2E SQLite remote lifecycle and runtime vectors: PASS'
}

cd "$ROOT"
main "$@"
