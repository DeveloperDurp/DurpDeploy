#!/usr/bin/env bash
# Sourced by e2e_test.sh; owns this invocation's isolated server and files.
CLIENT_ONLY=${DURPDEPLOY_E2E_CLIENT_ONLY:-0}
PORT=${DURPDEPLOY_E2E_PORT:-0}
TMP=$(mktemp -d)
COOKIES="$TMP/admin-cookies"
SERVER_PID=""

cleanup() {
	local status=$?
	e2e_release_port
	if ((status != 0)) && [[ -f "$TMP/server.log" ]]; then
		tail -n 100 "$TMP/server.log" >&2
	fi
    if [[ -n "$SERVER_PID" ]]; then
        local server_status=0
        printf 'E2E server shutdown requested at %s\n' "$(date -u +%FT%TZ)"
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || server_status=$?
        printf 'E2E server shutdown exit=%s at %s\n' "$server_status" "$(date -u +%FT%TZ)"
        # Only lifecycle/removal messages: never print request or step data.
        if [[ -f "$TMP/server.log" ]]; then
            grep -E '"msg":"(shutdown signal received, draining|remove container on shutdown failed|container step cleanup failed|agent shutdown failed)"' \
                "$TMP/server.log" || true
        fi
    fi
	# Keep the database and its logs alive until the server has stopped.
	rm -rf "$TMP"
	return "$status"
}
trap cleanup EXIT
trap 'exit 143' INT TERM

ADMIN_EMAIL="${DURPDEPLOY_ADMIN_EMAIL:-e2e-admin@test.local}"
ADMIN_PASS="${DURPDEPLOY_ADMIN_PASSWORD:-e2e-admin-password-1234}"
BASH_IMAGE=docker.io/library/bash:5.2
PYTHON_IMAGE=docker.io/library/python:3.12-alpine
PWSH_IMAGE=mcr.microsoft.com/powershell:latest
CONTROL_PLANE_PORT=${DURPDEPLOY_E2E_CONTROL_PLANE_PORT:-$PORT}

if [[ "$CLIENT_ONLY" == "1" ]]; then
    BASE="${DURPDEPLOY_BASE_URL:-http://localhost:8080}"
    BASE="${BASE%/}"
    CONTROL_PLANE_PORT=${DURPDEPLOY_E2E_CONTROL_PLANE_PORT:-$(python3 - "$BASE" <<'PY'
import sys
from urllib.parse import urlsplit

try:
    url = urlsplit(sys.argv[1])
    if url.scheme not in ("http", "https") or not url.hostname:
        raise ValueError("unsupported URL")
    print(url.port if url.port is not None else {"http": 80, "https": 443}[url.scheme])
except ValueError:
    sys.exit("FAIL: invalid E2E base URL")
PY
    )}
    echo "=== Running client-only E2E assertions against $BASE ==="
    if ! curl -fsS "$BASE/healthz" >/dev/null; then
        echo "FAIL: DurpDeploy server is unavailable at $BASE (set DURPDEPLOY_BASE_URL)" >&2
        exit 1
    fi
else
    if [[ ! "$PORT" =~ ^[0-9]{1,5}$ ]] || ((10#$PORT > 65535)); then
        echo "FAIL: DURPDEPLOY_E2E_PORT must be an integer from 0 to 65535" >&2
        exit 2
    fi
    BASE="http://localhost:$PORT"
    namespace="e2e_${TMP##*/}"
    namespace=${namespace//[^a-zA-Z0-9_-]/_}
    export DURPDEPLOY_CONTAINER_NAMESPACE=${DURPDEPLOY_CONTAINER_NAMESPACE:-$namespace}
    export AUTH_MFA_ARTIFACT_DIR=${AUTH_MFA_ARTIFACT_DIR:-"$ROOT_DIR/artifacts/auth-mfa/$DURPDEPLOY_CONTAINER_NAMESPACE"}
    cd "$ROOT_DIR"

    DB_DSN="$TMP/durpdeploy.db"

    if ! command -v openssl >/dev/null 2>&1; then
        echo "ERROR: openssl not found." >&2
        exit 1
    fi
    DURPDEPLOY_SECRET_KEY=$(openssl rand -base64 32)
    export DURPDEPLOY_SECRET_KEY

    echo "=== Building and starting server ==="
    go build -o "$TMP/durpdeploy" ./cmd/server

    # Seed the first admin user via the CLI. The CLI runs migrations on its
    # own, so the server's startup migration is a no-op. This mirrors the
    # production flow described in docs/deploy.md.
    DURPDEPLOY_DB="$DB_DSN" "$TMP/durpdeploy" admin create \
        --email "$ADMIN_EMAIL" --password "$ADMIN_PASS" >/dev/null

    if ((10#$PORT == 0)); then
        e2e_reserve_port "$TMP"
        PORT=$E2E_RESERVED_PORT
    fi
    BASE="http://localhost:$PORT"
    e2e_release_port

    # Start the server. The migrations it would normally run are a no-op
    # because the admin CLI just created the schema.
    DURPDEPLOY_ADDR="127.0.0.1:$PORT" \
		TMPDIR="$TMP" \
        DURPDEPLOY_AGENT_LISTEN_ADDR="127.0.0.1:0" \
        DURPDEPLOY_AGENT_PUBLIC_URL="https://localhost" \
        DURPDEPLOY_AGENT_IDENTITY_DIR="$TMP/agent-identity" \
        DURPDEPLOY_DB="$DB_DSN" \
        DURPDEPLOY_EXECUTION_BOUNDARY=service \
        DURPDEPLOY_URL="$BASE" \
        "$TMP/durpdeploy" >"$TMP/server.log" 2>&1 &
    SERVER_PID=$!
    BASE=$(e2e_wait_for_server "$SERVER_PID" "$TMP/server.log")
    PORT=${BASE##*:}
    CONTROL_PLANE_PORT=${DURPDEPLOY_E2E_CONTROL_PLANE_PORT:-$PORT}
    printf 'E2E instance: url=%s namespace=%s\n' "$BASE" "$DURPDEPLOY_CONTAINER_NAMESPACE"
    if [[ -n ${DURPDEPLOY_E2E_START_GATE:-} ]]; then
        for attempt in {1..720}; do
            [[ -f $DURPDEPLOY_E2E_START_GATE ]] && break
            kill -0 "$SERVER_PID" 2>/dev/null || exit 1
            if ((attempt == 720)); then
                echo "FAIL: concurrent E2E start gate timed out" >&2
                exit 1
            fi
            sleep 0.25
        done
    fi
fi

if [[ ! $CONTROL_PLANE_PORT =~ ^[0-9]{1,5}$ ]] || \
    ((10#$CONTROL_PLANE_PORT < 1 || 10#$CONTROL_PLANE_PORT > 65535)); then
    echo 'FAIL: control-plane probe port must be from 1 to 65535' >&2
    exit 2
fi
