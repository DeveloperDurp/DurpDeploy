#!/usr/bin/env bash
set -euo pipefail

umask 077

ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
RUN_DIR=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-agent-e2e.XXXXXX")
EVIDENCE_DIR=${AGENT_E2E_EVIDENCE_DIR:-"$ROOT/.omo/evidence/continue-remote-agent-rollout"}
AGENT_SOURCE_REV=a63b344bf6ebd2a714351f5e86032b58cf4b5a39
EVENTS="$RUN_DIR/events.txt"
: >"$EVENTS"
MODE=happy
REQUIRE=

cleanup() {
	local status=$?
	local residue
	residue=$(pgrep -af "$RUN_DIR" || true)
	if [[ -n "$residue" ]]; then
		printf 'cleanup found task process: %s\n' "$residue" >&2
		status=1
	fi
	rm -rf -- "$RUN_DIR"
	printf 'cleanup temporary-directory=removed processes=%s status=%d\n' \
		"$([[ -z "$residue" ]] && printf none || printf found)" "$status"
	return "$status"
}
trap cleanup EXIT INT TERM

while (($#)); do
	case "$1" in
	--failure-matrix)
		MODE=failure
		shift
		;;
	--require)
		[[ $# -ge 2 ]] || { printf '%s\n' 'ERROR: --require needs a value' >&2; exit 2; }
		REQUIRE=$2
		shift 2
		;;
	*)
		printf 'ERROR: unknown argument: %s\n' "$1" >&2
		exit 2
		;;
	esac
done

EXPECTED=(
	wrong-token duplicate-poll pre-start-loss post-start-loss cancel-unconfirmed
	crash-before-first-pair-request lost-first-phase-response
	crash-before-server-activation crash-before-cleanup-ack
	lost-cleanup-ack-response lost-poll-response lost-start-response
	lost-heartbeat-response lost-logs-response lost-result-response
	lost-cancelled-response
)

default_require() {
	local joined=
	local name
	for name in "${EXPECTED[@]}"; do
		joined+="${joined:+|}$name"
	done
	printf '%s' "$joined"
}

verify_require() {
	local value=${REQUIRE:-$(default_require)}
	local -a provided=()
	IFS='|' read -r -a provided <<<"$value"
	[[ ${#provided[@]} -eq ${#EXPECTED[@]} ]] || {
		printf 'ERROR: --require has %d events, want %d\n' "${#provided[@]}" "${#EXPECTED[@]}" >&2
		return 1
	}
	local index
	for index in "${!EXPECTED[@]}"; do
		[[ ${provided[$index]} == "${EXPECTED[$index]}" ]] || {
			printf 'ERROR: required event %d is %q, want %q\n' \
				"$index" "${provided[$index]}" "${EXPECTED[$index]}" >&2
			return 1
		}
	done
}

run_failure_matrix() {
	verify_require
	node --test scripts/agent_fault_proxy_test.mjs
	local matrix_status=0
	local scenario output lifecycle_flag
	for scenario in "${EXPECTED[@]}"; do
		output="$RUN_DIR/$scenario.txt"
		lifecycle_flag=--lifecycle
		case "$scenario" in
		crash-before-first-pair-request|lost-first-phase-response|crash-before-server-activation|crash-before-cleanup-ack|lost-cleanup-ack-response)
			lifecycle_flag=
			;;
		esac
		if ! node scripts/agent_admin_browser_proof.mjs ${lifecycle_flag:+"$lifecycle_flag"} \
			--fault-scenario "$scenario" \
			--evidence-dir "$EVIDENCE_DIR/failure-matrix/$scenario" \
			>"$output" 2>&1; then
			printf 'FAIL %s\n' "$scenario" >&2
			matrix_status=1
		fi
		while IFS= read -r line; do
			printf '%s\n' "$line"
			[[ $line == PASS\ * ]] && printf '%s\n' "$line" >>"$EVENTS"
		done <"$output"
	done
	if ! node --input-type=module - "$EVENTS" "$(default_require)" <<'JS'
import { readFile } from "node:fs/promises";
import { verifyExactEvents } from "./scripts/agent_e2e_verify.mjs";

const [, , path, required] = process.argv;
const events = verifyExactEvents(await readFile(path, "utf8"), required.split("|"));
console.log(`failure-matrix exact-events=${events.length} result=pass`);
JS
	then
		matrix_status=1
	fi
	return "$matrix_status"
}

run_happy() {
	local agent_root=${DURPDEPLOY_AGENT_WORKTREE:-}
	if [[ -z $agent_root ]]; then
		agent_root="$RUN_DIR/agent-source"
		git init -q "$agent_root"
		git -C "$agent_root" remote add origin \
			https://github.com/DeveloperDurp/durpdeploy-agent.git
		git -C "$agent_root" fetch -q --depth=1 origin "$AGENT_SOURCE_REV"
		git -C "$agent_root" checkout -q --detach FETCH_HEAD
	fi
	[[ -f "$agent_root/go.mod" ]] || {
		printf 'ERROR: standalone agent source not found: %s\n' "$agent_root" >&2
		return 1
	}
	mkdir -p "$EVIDENCE_DIR/browser"
	DURPDEPLOY_AGENT_WORKTREE="$agent_root" node \
		scripts/agent_admin_browser_proof.mjs --lifecycle \
		--evidence-dir "$EVIDENCE_DIR/browser"
	printf '%s\n' 'agent E2E SQLite paired remote lifecycle: PASS'
}

cd "$ROOT"
case "$MODE" in
failure) run_failure_matrix ;;
happy) run_happy ;;
esac
