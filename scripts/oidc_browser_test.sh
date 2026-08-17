#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
EVIDENCE_DIR=${DURPDEPLOY_OIDC_BROWSER_EVIDENCE_DIR:-"$ROOT_DIR/.omo/evidence/task-17-oidc-browser"}
RUN_DIR=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-oidc-browser.XXXXXX")
READY_FILE="$RUN_DIR/readiness.json"
FIXTURE_PID=""
FIXTURE_STOPPED=false
EVIDENCE_ACTIVE=false

cleanup() {
    local deadline
    if [[ -n $FIXTURE_PID ]] && kill -0 "$FIXTURE_PID" 2>/dev/null; then
        kill -TERM "$FIXTURE_PID" 2>/dev/null || true
        deadline=$((SECONDS + 10))
        while kill -0 "$FIXTURE_PID" 2>/dev/null && (( SECONDS < deadline )); do
            sleep 0.1
        done
        if kill -0 "$FIXTURE_PID" 2>/dev/null; then
            kill -KILL "$FIXTURE_PID" 2>/dev/null || true
        fi
        wait "$FIXTURE_PID" 2>/dev/null || true
        FIXTURE_STOPPED=true
    fi
    rm -rf -- "$RUN_DIR"
    if [[ $EVIDENCE_ACTIVE == true ]]; then
        printf '{\n  "fixture_process_stopped": %s,\n  "run_directory_removed": true\n}\n' \
            "$FIXTURE_STOPPED" > "$EVIDENCE_DIR/cleanup.json"
        printf 'Cleanup receipt: fixture process stopped=%s; run directory removed.\n' \
            "$FIXTURE_STOPPED" >> "$EVIDENCE_DIR/notepad.log"
    fi
}

on_signal() {
    cleanup
    trap - EXIT
    exit 143
}

trap cleanup EXIT
trap on_signal INT TERM

usage() {
    echo "usage: $0 [--outage | --self-test]" >&2
}

wait_for_readiness() {
    local deadline=$((SECONDS + 15))
    while (( SECONDS < deadline )); do
        if [[ -f $READY_FILE ]]; then
            node "$ROOT_DIR/scripts/oidc_browser_test.mjs" \
                --validate-ready-file "$READY_FILE"
            return 0
        fi
        if ! kill -0 "$FIXTURE_PID" 2>/dev/null; then
            echo "OIDC browser harness: tagged fixture exited before readiness" >&2
            return 1
        fi
        sleep 0.1
    done
    echo "OIDC browser harness: timed out waiting for tagged fixture readiness" >&2
    return 1
}

OUTAGE=false
case ${1:-} in
    "") ;;
    --outage) OUTAGE=true ;;
    --self-test)
        cd "$ROOT_DIR"
        bash -n "$ROOT_DIR/scripts/oidc_browser_test.sh"
        node --check "$ROOT_DIR/scripts/oidc_browser_test.mjs"
        node "$ROOT_DIR/scripts/oidc_browser_test.mjs" --self-test
        go run -tags=oidctest ./scripts/oidc-browser-fixture --self-test
        go test -tags=oidctest ./scripts/oidc-browser-fixture
        exit 0
        ;;
    *) usage; exit 2 ;;
esac

mkdir -p -- "$EVIDENCE_DIR"
chmod 700 "$EVIDENCE_DIR"
EVIDENCE_ACTIVE=true
node "$ROOT_DIR/scripts/oidc_browser_test.mjs" --preflight --evidence-dir "$EVIDENCE_DIR"

cd "$ROOT_DIR"
go build -tags oidctest -o "$RUN_DIR/oidc-browser-fixture" ./scripts/oidc-browser-fixture
FIXTURE_ARGS=(--ready-file "$READY_FILE")
BROWSER_ARGS=(--ready-file "$READY_FILE" --evidence-dir "$EVIDENCE_DIR")
if [[ $OUTAGE == true ]]; then
    FIXTURE_ARGS+=(--outage)
    BROWSER_ARGS+=(--outage)
fi
"$RUN_DIR/oidc-browser-fixture" "${FIXTURE_ARGS[@]}" >/dev/null 2>&1 &
FIXTURE_PID=$!
wait_for_readiness
node "$ROOT_DIR/scripts/oidc_browser_test.mjs" "${BROWSER_ARGS[@]}"
