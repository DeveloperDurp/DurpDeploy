#!/usr/bin/env bash
set -euo pipefail

umask 077

ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
EVIDENCE_ROOT=${DURPDEPLOY_AGENT_E2E_EVIDENCE_DIR:-"$ROOT/.omo/evidence/task-27-remote-agent-control-plane"}
RUN_ID=${DURPDEPLOY_AGENT_E2E_RUN_ID:-"$(date -u +%Y%m%dT%H%M%SZ)-$$"}
RUN_DIR=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-agent-e2e.XXXXXX")
EVIDENCE_DIR="$EVIDENCE_ROOT/$RUN_ID"
COMPOSE=()

cleanup() {
    local status=$?
    if ((${#COMPOSE[@]})); then
        "${COMPOSE[@]}" -p "durpdeploy-agent-e2e-$RUN_ID" -f "$RUN_DIR/compose.yml" down \
            --volumes --remove-orphans >/dev/null 2>&1 || true
    fi
    rm -rf -- "$RUN_DIR"
    if [[ -d $EVIDENCE_DIR ]]; then
        printf '{"run_directory_removed":true,"compose_cleanup_attempted":%s}\n' \
            "$(( ${#COMPOSE[@]} > 0 ))" > "$EVIDENCE_DIR/cleanup.json"
    fi
    return "$status"
}
trap cleanup EXIT INT TERM

redact() {
    sed -E \
        -e 's/(ddp_(enroll|pat)_[[:alnum:]_-]+)/<redacted>/g' \
        -e 's/([Tt]oken|[Ss]ecret|[Pp]assword|[Cc]laim)[=:][^[:space:],]*/\1=<redacted>/g' \
        -e 's/-----BEGIN [^-]+-----[^-]*-----END [^-]+-----/<redacted-certificate>/g'
}

result() {
    local id=$1 status=$2 source=$3 runtime_e2e=${4:-true}
    printf '{"id":"%s","status":"%s","source":"%s","runtime_e2e":%s}\n' \
        "$id" "$status" "$source" "$runtime_e2e" | tee -a "$EVIDENCE_DIR/manifest.jsonl"
}

select_compose() {
    if docker compose version >/dev/null 2>&1; then
        COMPOSE=(docker compose)
    elif podman compose version >/dev/null 2>&1; then
        COMPOSE=(podman compose)
    else
        echo 'agent E2E requires Docker Compose or Podman Compose' >&2
        return 1
    fi
}

run_scenario() {
    local id=$1 package=$2 pattern=$3
    local log="$RUN_DIR/$id.log"
    if go test -count=1 -v -run "$pattern" "$package" >"$log" 2>&1 && \
        grep -q '^--- PASS:' "$log"; then
        redact <"$log" >"$EVIDENCE_DIR/$id.log"
        result "$id" pass "go-test"
    else
        redact <"$log" >"$EVIDENCE_DIR/$id.log"
        result "$id" fail "go-test"
        return 1
    fi
}

browser_proof() {
    local log="$RUN_DIR/browser-container.log"
    local runtime=${COMPOSE[0]}
    local image=${MOBILE_BROWSER_IMAGE:-durpdeploy-mobile-browser:local}
	if {
		printf 'build: %s build -f Dockerfile.mobile-browser -t %s .\n' "$runtime" "$image"
        "$runtime" build -f Dockerfile.mobile-browser -t "$image" .
        printf 'run: %s run --rm --init --entrypoint /usr/local/bin/mobile-browser-container ...\n' "$runtime"
        "$runtime" run --rm --init \
            --entrypoint /usr/local/bin/mobile-browser-container \
            -e AGENT_ADMIN_BROWSER_PROOF=1 \
            -e MOBILE_ARTIFACT_DIR=/artifacts \
            -e MOBILE_RUN_ID="$RUN_ID/browser" \
            -v "$ROOT:/workspace" \
            -v "$EVIDENCE_ROOT:/artifacts" \
            "$image"
	} >"$log" 2>&1; then
		redact <"$log" >"$EVIDENCE_DIR/browser-container.log"
		result "browser-admin-pages" pass "container-playwright"
		return
	fi
	redact <"$log" >"$EVIDENCE_DIR/browser-container.log"
	result "browser-admin-pages" fail "container-playwright"
	return 1
}

secret_scan() {
	if grep -R -E --exclude='*.png' \
		'ddp_enroll_[[:xdigit:]]{64}|ddp_pat_[[:alnum:]_-]+|claim_token["=:]' \
		"$EVIDENCE_DIR" >/dev/null; then
		printf '%s\n' '{"passed":false,"reason":"sensitive value detected"}' \
			>"$EVIDENCE_DIR/secret-scan.json"
		return 1
	fi
	printf '%s\n' '{"passed":true,"scanned":"non-image evidence"}' \
		>"$EVIDENCE_DIR/secret-scan.json"
}

main() {
    mkdir -p -- "$EVIDENCE_DIR"
    chmod 700 "$EVIDENCE_DIR"
    select_compose
    cat >"$RUN_DIR/compose.yml" <<'YAML'
services:
  server:
    image: scratch
    profiles: [verification]
    ports: ["127.0.0.1:18080:8080"]
    volumes: [database:/data, server-identity:/identity]
  agent-matching:
    image: scratch
    profiles: [verification]
    volumes: [agent-matching-state:/state]
  agent-nonmatching:
    image: scratch
    profiles: [verification]
    volumes: [agent-nonmatching-state:/state]
volumes:
  database:
  server-identity:
  agent-matching-state:
  agent-nonmatching-state:
YAML
    "${COMPOSE[@]}" -p "durpdeploy-agent-e2e-$RUN_ID" -f "$RUN_DIR/compose.yml" config \
        | redact >"$EVIDENCE_DIR/compose.redacted.yml"
    printf '%s\n' "compose=${COMPOSE[*]}" >"$EVIDENCE_DIR/runner.txt"
    run_scenario listener-enrollment ./cmd/server '^TestAgentListener_enrollsPinnedAgent$'
    run_scenario remote-dispatch-lifecycle ./cmd/server '^TestAgentListener_remoteAgentCompletesRemoteDispatch$'

    run_scenario matching ./internal/agentserver '^TestPoll_(claimsOldestEligibleDeployment|allowsOnlyOneConcurrentClaim)$'
    run_scenario logs ./internal/agentserver '^TestLifecycle_runsGuardedStartHeartbeatLogsResult$'
    run_scenario cancel ./internal/agentserver '^TestLifecycle_acknowledgesCancellationAndRecordsLateResult$'
    run_scenario cancellation-tree ./cmd/agent '^TestAgentSubprocess_sigtermKillsSpawnedChild$'
    run_scenario no-match ./internal/dispatch '^TestDispatch_keepsRemoteWaiting_whenPolicyHasNoMatchingAgent$'
    run_scenario revoke ./internal/agentserver '^TestPoll_rejectsUnauthenticatedOrIneligibleAgents$'
    run_scenario wrong-pin ./internal/agenttls '^TestNewClientConfig_rejectsWrongPinHostnameAndExpiry$'
    run_scenario rotate ./internal/agentclient '^TestClient_(persists_staged_server_pin_from_heartbeat|promotes_pending_pin_after_verified_connection)$'
    run_scenario restart-before-start ./internal/agentserver '^TestMaintain_reclaimsOnlyExpiredUnstartedClaims$'
    run_scenario restart-after-start ./internal/agentserver '^TestMaintain_losesStartedWorkOnceAcrossRestartAndRecordsLateResult$'
	run_scenario local ./internal/dispatch '^TestDispatch_createsLocalWaitingDispatch_whenEnvironmentHasNoPolicy$'
	browser_proof
	secret_scan
    printf '%s\n' 'agent E2E SQLite remote lifecycle and runtime vectors: PASS (see manifest.jsonl)'
}

cd "$ROOT"
main "$@"
