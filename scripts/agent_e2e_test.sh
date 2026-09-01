#!/usr/bin/env bash
set -euo pipefail

umask 077

ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
RUN_ID=${DURPDEPLOY_AGENT_E2E_RUN_ID:-"$(date -u +%Y%m%dT%H%M%SZ)-$$"}
RUN_DIR=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-agent-e2e.XXXXXX")
ARTIFACT_DIR="$RUN_DIR/artifacts"
COMPOSE=()

cleanup() {
    local status=$?
    if ((${#COMPOSE[@]})); then
        "${COMPOSE[@]}" -p "durpdeploy-agent-e2e-$RUN_ID" -f "$RUN_DIR/compose.yml" down \
            --volumes --remove-orphans >/dev/null 2>&1 || true
	fi
	rm -rf -- "$RUN_DIR"
	return "$status"
}
trap cleanup EXIT INT TERM

redact() {
	sed -E \
		-e 's/(ddp_pat_[[:alnum:]_-]+)/<redacted>/g' \
		-e 's/([Tt]oken|[Ss]ecret|[Pp]assword|[Cc]laim)[=:][^[:space:],]*/\1=<redacted>/g' \
		-e 's/-----BEGIN [^-]+-----[^-]*-----END [^-]+-----/<redacted-certificate>/g'
}

print_browser_artifacts() {
	local container_log=$1
	local evidence_dir="$ARTIFACT_DIR/$RUN_ID/browser"

	printf '%s\n' '===== browser proof container log =====' >&2
	redact <"$container_log" >&2

	if [ -d "$evidence_dir" ]; then
		printf '%s\n' '===== browser proof evidence (redacted) =====' >&2
		local artifact
		shopt -s nullglob
		for artifact in "$evidence_dir"/*; do
			case "$artifact" in
				*.png | *.jpg | *.jpeg | *.webp) continue ;;
				esac
			if [ -f "$artifact" ]; then
				printf '\n--- %s ---\n' "$(basename -- "$artifact")" >&2
				redact <"$artifact" >&2
			fi
			done
		shopt -u nullglob
	fi
}

result() {
	local id=$1 status=$2 source=$3 runtime_e2e=${4:-true}
	printf '{"id":"%s","status":"%s","source":"%s","runtime_e2e":%s}\n' \
		"$id" "$status" "$source" "$runtime_e2e"
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
		result "$id" pass "go-test"
	else
		redact <"$log" >&2
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
			-v "$ARTIFACT_DIR:/artifacts" \
			"$image"
	} >"$log" 2>&1; then
		result "browser-admin-pages" pass "container-playwright"
		return
	fi
	print_browser_artifacts "$log"
	result "browser-admin-pages" fail "container-playwright"
	return 1
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
		| redact >/dev/null
	run_scenario remote-dispatch-lifecycle ./internal/agentclient '^TestClient_polls_and_acknowledges_cancellation$'

	run_scenario matching ./internal/agentserver '^TestPoll_claimedPayloadOpensOnlyForClaimedIdentity$'
    run_scenario logs ./internal/agentserver '^TestLifecycle_runsGuardedStartHeartbeatLogsResult$'
    run_scenario cancel ./internal/agentserver '^TestLifecycle_acknowledgesCancellationAndRecordsLateResult$'
    run_scenario cancellation-tree \
      github.com/DeveloperDurp/durpdeploy-agent/cmd/agent \
      '^TestAgentSubprocess_sigtermKillsSpawnedChild$'
    run_scenario wrong-pin ./internal/agenttls '^TestNewClientConfig_rejectsWrongPinHostnameAndExpiry$'
	run_scenario rotate ./internal/agentclient '^TestClient_persists_staged_server_pin_from_heartbeat$'
    run_scenario restart-before-start ./internal/agentserver '^TestMaintain_reclaimsOnlyExpiredUnstartedClaims$'
    run_scenario restart-after-start ./internal/agentserver '^TestMaintain_losesStartedWorkOnceAcrossRestartAndRecordsLateResult$'
	browser_proof
	secret_scan
	printf '%s\n' 'agent E2E SQLite remote lifecycle and runtime vectors: PASS'
}

cd "$ROOT"
main "$@"
