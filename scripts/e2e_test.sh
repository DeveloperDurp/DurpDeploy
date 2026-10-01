#!/usr/bin/env bash
set -euo pipefail

umask 077

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
CLIENT_ONLY=${DURPDEPLOY_E2E_CLIENT_ONLY:-0}
PORT=${DURPDEPLOY_E2E_PORT:-8080}
TMP=$(mktemp -d)
COOKIES="$TMP/admin-cookies"
SERVER_PID=""

cleanup() {
	local status=$?
	if ((status != 0)) && [[ -f "$TMP/server.log" ]]; then
		tail -n 100 "$TMP/server.log" >&2
	fi
    rm -rf "$TMP"
    if [[ -n "$SERVER_PID" ]]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
	return "$status"
}
trap cleanup EXIT

ADMIN_EMAIL="${DURPDEPLOY_ADMIN_EMAIL:-e2e-admin@test.local}"
ADMIN_PASS="${DURPDEPLOY_ADMIN_PASSWORD:-e2e-admin-password-1234}"
BASH_IMAGE=docker.io/library/bash:5.2
PYTHON_IMAGE=docker.io/library/python:3.12-alpine
PWSH_IMAGE=mcr.microsoft.com/powershell:latest
CONTROL_PLANE_PORT=${DURPDEPLOY_E2E_CONTROL_PLANE_PORT:-$PORT}

if [[ "$CLIENT_ONLY" == "1" ]]; then
    BASE="${DURPDEPLOY_BASE_URL:-http://localhost:8080}"
    BASE="${BASE%/}"
    echo "=== Running client-only E2E assertions against $BASE ==="
    if ! curl -fsS "$BASE/healthz" >/dev/null; then
        echo "FAIL: DurpDeploy server is unavailable at $BASE (set DURPDEPLOY_BASE_URL)" >&2
        exit 1
    fi
else
    if [[ ! "$PORT" =~ ^[0-9]+$ ]] || ((PORT < 1 || PORT > 65535)); then
        echo "FAIL: DURPDEPLOY_E2E_PORT must be an integer from 1 to 65535" >&2
        exit 2
    fi
    BASE="http://localhost:$PORT"
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

    # Start the server. The migrations it would normally run are a no-op
    # because the admin CLI just created the schema.
    DURPDEPLOY_ADDR="127.0.0.1:$PORT" \
        DURPDEPLOY_AGENT_LISTEN_ADDR="127.0.0.1:0" \
        DURPDEPLOY_AGENT_PUBLIC_URL="https://localhost" \
        DURPDEPLOY_AGENT_IDENTITY_DIR="$TMP/agent-identity" \
        DURPDEPLOY_DB="$DB_DSN" \
        DURPDEPLOY_EXECUTION_BOUNDARY=service \
        DURPDEPLOY_URL="$BASE" \
        "$TMP/durpdeploy" >"$TMP/server.log" 2>&1 &
    SERVER_PID=$!
    sleep 2
fi

if [[ "${DURPDEPLOY_AUTH_MFA_HTTP_MATRIX:-0}" == "1" ]]; then
    if [[ "$CLIENT_ONLY" == "1" ]]; then
        echo "FAIL: the auth/MFA HTTP matrix requires the isolated E2E lifecycle" >&2
        exit 2
    fi
    DURPDEPLOY_AUTH_MFA_HTTP_MATRIX_CLIENT_ONLY=1 \
        DURPDEPLOY_BASE_URL="$BASE" \
        DURPDEPLOY_AUTH_MFA_DB="$TMP/durpdeploy.db" \
        DURPDEPLOY_AUTH_MFA_SERVER_LOG="$TMP/server.log" \
        DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL="$ADMIN_EMAIL" \
        DURPDEPLOY_AUTH_MFA_ADMIN_PASSWORD="$ADMIN_PASS" \
        "${DURPDEPLOY_AUTH_MFA_HTTP_MATRIX_SCRIPT:-$SCRIPT_DIR/auth_mfa_sqlite_http_matrix.sh}" \
        ${DURPDEPLOY_AUTH_MFA_HTTP_MATRIX_ARGS:-}
    exit $?
fi

# Helpers. All helpers pass -b $COOKIES so the session cookie is
# attached automatically. State-changing methods append csrf_token=$CSRF
# to the form data so every write satisfies CSRFMiddleware. DELETEs
# pass the token via the X-CSRF-Token header instead — Go's stdlib
# does not parse form bodies for DELETE, so form data would 403.
curl_silent() { curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" "$@"; }
curl_body() { curl -s -b "$COOKIES" "$@"; }
do_delete() { curl -s -b "$COOKIES" -H "X-CSRF-Token: $CSRF" -o /dev/null -w "%{http_code}" -X DELETE "$1"; }
csrf_from_cookies() {
    curl -s -b "$1" "$BASE/" \
        | grep -oP '<meta name="csrf-token" content="\K[^"]+' \
        | head -1
}

# mint_web_token creates an API token via the web form and prints the
# plaintext by consuming the single-use flash record. The redirect
# Location carries only the opaque flash id — never the token
# (issue #32). Pass the cookie jar, token name, and CSRF token.
mint_web_token() {
    local jar=$1 name=$2 csrf=$3 redirect
    redirect=$(curl -s -b "$jar" -D - -o /dev/null \
        -X POST -d "name=$name&csrf_token=$csrf" \
        "$BASE/settings/tokens" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
    case "$redirect" in
        /settings/tokens?flash=*) ;;
        *) return 1 ;;
    esac
    curl -s -b "$jar" "$BASE$redirect" \
        | grep -oE 'ddp_pat_[0-9a-f]{64}' | head -1
}

# Log in. Captures the session cookie into $COOKIES and gets the CSRF
# token from the authenticated page metadata. Asserts a 303 redirect (success).
echo "=== F0: Login ==="
CODE=$(curl -s -c "$COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "email=$ADMIN_EMAIL&password=$ADMIN_PASS" "$BASE/login")
[[ "$CODE" == "303" ]] || { echo "FAIL: login got $CODE, want 303"; exit 1; }
CSRF=$(csrf_from_cookies "$COOKIES")
[[ -n "$CSRF" ]] || { echo "FAIL: no CSRF token on authenticated page"; exit 1; }
echo "  Login + CSRF retrieved: OK"

api_get() { curl -s -H "Authorization: Bearer $API_TOKEN" "$@"; }
api_get_code() { curl -s -H "Authorization: Bearer $API_TOKEN" -o /dev/null -w "%{http_code}" "$@"; }
api_post() { curl -s -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" -X POST -d "$1" "$2"; }
api_put() { curl -s -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" -X PUT -d "$1" "$2"; }
api_post_code() { curl -s -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" -X POST -d "$1" -o /dev/null -w "%{http_code}" "$2"; }
api_post_noauth() { curl -s -H "Content-Type: application/json" -X POST -d "$1" -o /dev/null -w "%{http_code}" "$2"; }
api_item_id_by_name() {
    local url=$1
    local name=$2
    api_get "$url?limit=1000" | python3 -c '
import json
import sys

name = sys.argv[1]
for item in json.load(sys.stdin)["items"]:
    if item["name"] == name:
        print(item["id"])
        break
' "$name"
}
api_lifecycle_stage_id() {
    local lifecycle_id=$1
    local environment_id=$2
    api_get "$BASE/api/v1/lifecycles/$lifecycle_id" | python3 -c '
import json
import sys

environment_id = int(sys.argv[1])
for stage in json.load(sys.stdin)["stages"]:
    if stage["environment_id"] == environment_id:
        print(stage["id"])
        break
' "$environment_id"
}

API_TOKEN=$(mint_web_token "$COOKIES" e2e-api "$CSRF") \
    || { echo "FAIL: could not mint API token"; exit 1; }
[[ -n "$API_TOKEN" ]] || { echo "FAIL: could not mint API token"; exit 1; }
ADMIN_ID=$(api_get "$BASE/api/v1/users/me" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$ADMIN_ID" ]] || { echo "FAIL: could not resolve current admin user"; exit 1; }
echo "  API token minted via /settings/tokens: OK"

# A request with no cookie must redirect to /login.
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/")
[[ "$CODE" == "303" ]] || { echo "FAIL: unauth GET / got $CODE, want 303"; exit 1; }
echo "  Unauth redirect: OK"

# A request with the session cookie but no CSRF on a POST must 403.
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "name=NoCSRF" "$BASE/projects")
[[ "$CODE" == "403" ]] || { echo "FAIL: POST without CSRF got $CODE, want 403"; exit 1; }
echo "  CSRF gate: OK"

echo "=== F3.1: Happy Path ==="
CODE=$(curl_silent -X POST -d "name=TestProject&csrf_token=$CSRF" "$BASE/projects")
[[ "$CODE" == "303" ]] || { echo "FAIL: create project got $CODE"; exit 1; }
PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | head -1)
echo "Project ID: $PROJECT_ID"

CODE=$(curl_silent -X POST -d "name=TestEnv&csrf_token=$CSRF" "$BASE/environments")
[[ "$CODE" == "303" ]] || { echo "FAIL: create env got $CODE"; exit 1; }
ENV_ID=$(curl_body "$BASE/environments" | grep -oP 'href="/environments/\K[0-9]+' | head -1)
echo "Env ID: $ENV_ID"

CODE=$(curl_silent -X POST \
    --data-urlencode "name=Step1" \
    --data-urlencode 'script_body=test "$VAR1" = hello && echo default-variable=$VAR1' \
    -d "container_image=$BASH_IMAGE&csrf_token=$CSRF" \
    "$BASE/projects/$PROJECT_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create step got $CODE"; exit 1; }

# Verify the dedicated steps page renders.
CODE=$(curl_silent "$BASE/projects/$PROJECT_ID/steps-page")
[[ "$CODE" == "200" ]] || { echo "FAIL: steps-page got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "name=VAR1&value=hello&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/variables")
[[ "$CODE" == "303" ]] || { echo "FAIL: create variable got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "version=1.0.0&release_notes=first&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create release got $CODE"; exit 1; }
RELEASE_ID=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "Release ID: $RELEASE_ID"

DEP_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$RELEASE_ID&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/deploy" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
DEP_ID=$(echo "$DEP_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$DEP_ID" ]] || { echo "FAIL: create deployment did not redirect"; exit 1; }
echo "Deployment ID: $DEP_ID"

for i in {1..100}; do
  DEP_STATUS=$(curl_body "$BASE/deployments/$DEP_ID/status")
  echo "$DEP_STATUS" | grep -qE 'failed|succeeded|cancelled' && break
  sleep 0.2
done
echo "$DEP_STATUS" | grep -q succeeded || {
    echo "FAIL: default-variable deployment did not succeed"; exit 1;
}
curl_body "$BASE/deployments/$DEP_ID/logs.txt" | \
    grep -q 'default-variable=hello' || {
        echo "FAIL: empty variable restriction did not pass VAR1"; exit 1;
    }
echo "  Empty variable restriction passes all project variables: OK"

CODE=$(curl_silent "$BASE/deployments/$DEP_ID")
[[ "$CODE" == "200" ]] || { echo "FAIL: deployment page got $CODE"; exit 1; }

echo "=== F3.1b: Deployment Note ==="
# Submit a second deployment with a note via the project-scoped deploy page.
curl -s -b "$COOKIES" -o /dev/null -X POST \
    -d "release_id=$RELEASE_ID&environment_id=$ENV_ID&note=smoke-test-audit&csrf_token=$CSRF" \
    "$BASE/projects/$PROJECT_ID/deploy"

# Extract the new deployment ID from the deployments list (latest).
NOTE_DEP=$(curl_body "$BASE/deployments" | grep -oP 'href="/deployments/\K[0-9]+' | sort -n | tail -1)
[[ -n "$NOTE_DEP" ]] || { echo "FAIL: could not extract note deployment ID"; exit 1; }
echo "Note Deployment ID: $NOTE_DEP"

# The new deployment's detail page must contain the note text.
NOTE_PAGE=$(curl_body "$BASE/deployments/$NOTE_DEP")
echo "$NOTE_PAGE" | grep -q "smoke-test-audit" || { echo "FAIL: note text missing from deployment detail"; exit 1; }
echo "  Note appears on deployment detail: OK"

# The first deployment (F3.1) has no note — prove notes are per-deployment.
FIRST_PAGE=$(curl_body "$BASE/deployments/$DEP_ID")
echo "$FIRST_PAGE" | grep -q "smoke-test-audit" && { echo "FAIL: first deployment should not have note text"; exit 1; } || true
echo "  First deployment lacks note: OK"

echo "=== F3.2: Cancel Path ==="
curl -s -b "$COOKIES" -o /dev/null -X POST -d "name=LongStep&script_body=sleep+10&container_image=$BASH_IMAGE&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps"
curl -s -b "$COOKIES" -o /dev/null -X POST -d "version=1.0.1&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/releases"
CANCEL_REL=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "Cancel Release ID: $CANCEL_REL"

CANCEL_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$CANCEL_REL&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/deploy" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
CANCEL_DEP=$(echo "$CANCEL_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$CANCEL_DEP" ]] || { echo "FAIL: cancel deployment did not redirect"; exit 1; }
echo "Cancel Deployment ID: $CANCEL_DEP"

for i in {1..50}; do
  if curl_body "$BASE/deployments/$CANCEL_DEP/status" | grep -q 'running'; then break; fi
  sleep 0.1
done
CANCEL_STATUS=$(curl_body "$BASE/deployments/$CANCEL_DEP/status")
CANCEL_STATE=$(echo "$CANCEL_STATUS" | grep -oP 'badge [^"]+">\K[a-z_]+' | head -1)
echo "$CANCEL_STATUS" | grep -q 'running' || {
	curl_body "$BASE/deployments/$CANCEL_DEP/logs.txt" >&2
  echo "FAIL: cancel deployment did not reach running (state=${CANCEL_STATE:-unknown})"; exit 1;
}
CODE=$(curl_silent -X POST -d "csrf_token=$CSRF" "$BASE/deployments/$CANCEL_DEP/cancel")
[[ "$CODE" == "303" ]] || { echo "FAIL: cancel got $CODE"; exit 1; }
for i in {1..200}; do
  CANCEL_STATUS=$(curl_body "$BASE/deployments/$CANCEL_DEP/status")
  echo "$CANCEL_STATUS" | grep -q 'cancelled' && break
  sleep 0.1
done
echo "$CANCEL_STATUS" | grep -q 'cancelled' || { echo "FAIL: web cancellation did not finish"; exit 1; }

echo "=== F3.2b: Per-Step Timeout ==="
STEPS_PAGE=$(curl_body "$BASE/projects/$PROJECT_ID/steps-page")
LONG_STEP_ID=$(echo "$STEPS_PAGE" | grep -oP 'step-row-\K[0-9]+' | sort -n | tail -1)
if [[ -n "$LONG_STEP_ID" ]]; then
  do_delete "$BASE/projects/$PROJECT_ID/steps/$LONG_STEP_ID"
fi

CODE=$(curl_silent -X POST -d "name=TimeoutStep&script_body=sleep+10&timeout_seconds=1&container_image=$BASH_IMAGE&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create timeout step got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "version=1.0.2&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create timeout release got $CODE"; exit 1; }
TIMEOUT_REL=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "Timeout Release ID: $TIMEOUT_REL"

TIMEOUT_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$TIMEOUT_REL&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/deploy" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
TIMEOUT_DEP=$(echo "$TIMEOUT_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$TIMEOUT_DEP" ]] || { echo "FAIL: timeout deployment did not redirect"; exit 1; }
echo "Timeout Deployment ID: $TIMEOUT_DEP"

START=$(date +%s)
for i in {1..100}; do
  STATUS_BODY=$(curl_body "$BASE/deployments/$TIMEOUT_DEP/status")
  if echo "$STATUS_BODY" | grep -qE 'failed|succeeded|cancelled'; then break; fi
  sleep 0.2
done
END=$(date +%s)
ELAPSED=$((END - START))

echo "$STATUS_BODY" | grep -q 'failed' || { echo "FAIL: timeout deploy did not fail, got status: $STATUS_BODY"; exit 1; }
[[ $ELAPSED -lt 25 ]] || { echo "FAIL: timeout deploy took ${ELAPSED}s, expected <25s"; exit 1; }
curl_body "$BASE/deployments/$TIMEOUT_DEP/logs.txt" | grep -q 'timed out' || { echo "FAIL: timeout log missing"; exit 1; }
echo "  Per-step timeout killed long sleep: OK (failed in ${ELAPSED}s)"

echo "=== F3.2c: Re-run ==="
# Re-run the failed timeout deployment. The endpoint bypasses the gate and
# creates a new deployment with the same release + env.
REDIR=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "csrf_token=$CSRF" "$BASE/deployments/$TIMEOUT_DEP/redeploy" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
NEW_DEP=$(echo "$REDIR" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$NEW_DEP" ]] || { echo "FAIL: redeploy did not redirect"; exit 1; }
[[ "$NEW_DEP" != "$TIMEOUT_DEP" ]] || { echo "FAIL: redeploy returned same deployment ID $NEW_DEP"; exit 1; }
echo "Re-run Deployment ID: $NEW_DEP"

# Poll the new deployment until it reaches a terminal state (same sleep/timeout
# step, so it will also fail).
for i in {1..100}; do
  RERUN_STATUS=$(curl_body "$BASE/deployments/$NEW_DEP/status")
  if echo "$RERUN_STATUS" | grep -qE 'failed|succeeded|cancelled'; then break; fi
  sleep 0.2
done
echo "$RERUN_STATUS" | grep -q 'failed' || { echo "FAIL: re-run deploy did not fail, got status: $RERUN_STATUS"; exit 1; }
echo "  Re-run deployment failed as expected: OK"

# The new deployment's note should record the lineage.
RERUN_PAGE=$(curl_body "$BASE/deployments/$NEW_DEP")
echo "$RERUN_PAGE" | grep -q "Re-run of #$TIMEOUT_DEP" || { echo "FAIL: re-run note missing lineage text"; exit 1; }
echo "  Re-run note records lineage: OK"

echo "=== F3.2d: Step Retry on Failure ==="
# A step with max_retries=2 and script_body=exit+1 should be retried twice,
# logging attempt and retry messages, before the deployment fails.
STEPS_PAGE=$(curl_body "$BASE/projects/$PROJECT_ID/steps-page")
TIMEOUT_STEP_ID=$(echo "$STEPS_PAGE" | grep -oP 'step-row-\K[0-9]+' | sort -n | tail -1)
if [[ -n "$TIMEOUT_STEP_ID" ]]; then
  do_delete "$BASE/projects/$PROJECT_ID/steps/$TIMEOUT_STEP_ID"
fi

CODE=$(curl_silent -X POST -d "name=RetryStep&script_body=exit+1&max_retries=2&container_image=$BASH_IMAGE&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create retry step got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "version=1.0.4&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create retry release got $CODE"; exit 1; }
RETRY_REL=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "Retry Release ID: $RETRY_REL"

RETRY_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$RETRY_REL&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/deploy" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
RETRY_DEP=$(echo "$RETRY_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$RETRY_DEP" ]] || { echo "FAIL: retry deployment did not redirect"; exit 1; }
echo "Retry Deployment ID: $RETRY_DEP"

for i in {1..100}; do
  RETRY_STATUS=$(curl_body "$BASE/deployments/$RETRY_DEP/status")
  if echo "$RETRY_STATUS" | grep -qE 'failed|succeeded|cancelled'; then break; fi
  sleep 0.2
done
echo "$RETRY_STATUS" | grep -q 'failed' || { echo "FAIL: retry deploy did not fail, got status: $RETRY_STATUS"; exit 1; }

LOG_LINES=$(curl_body "$BASE/deployments/$RETRY_DEP/logs.txt")
echo "$LOG_LINES" | grep -q "attempt 1" || { echo "FAIL: retry log missing attempt 1"; exit 1; }
echo "$LOG_LINES" | grep -q "retrying" || { echo "FAIL: retry log missing retrying"; exit 1; }
echo "  Step retry loop ran: OK"

echo "=== F3.3: Validation Path ==="
CODE=$(curl_silent -X POST -d "name=&csrf_token=$CSRF" "$BASE/projects")
[[ "$CODE" == "422" ]] || { echo "FAIL: empty project name should be 422, got $CODE"; exit 1; }

echo "=== F3.4: Variable Fallback ==="
curl -s -b "$COOKIES" -o /dev/null -X POST -d "name=StepMissing&script=echo+%24%7BMISSING%7D&container_image=$BASH_IMAGE&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps"
curl -s -b "$COOKIES" -o /dev/null -X POST -d "version=2.0.0&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/releases"
NEW_REL=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
curl -s -b "$COOKIES" -o /dev/null -X POST -d "release_id=$NEW_REL&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/deploy"

echo "=== F3.5: Lifecycle Gate ==="
# Separate project + envs + lifecycle so the F3.1 project stays free-floating.
LC_PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | head -1)
# We can't easily mint unique names via grep, so use a deterministic counter trick.
# Use the project list and grab the highest id.
LC_PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | sort -n | tail -1)
LC_NAME="LC-Project-$(date +%s)"
CODE=$(curl_silent -X POST -d "name=$LC_NAME&csrf_token=$CSRF" "$BASE/projects")
[[ "$CODE" == "303" ]] || { echo "FAIL: create lifecycle project got $CODE"; exit 1; }
LC_PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | sort -n | tail -1)
echo "Lifecycle Project ID: $LC_PROJECT_ID"

# Three envs: LC-Dev, LC-Test, LC-Prod + an "outside" env.
LC_TS=$(date +%s)
LC_DEV="LC-Dev-$LC_TS"
LC_TEST="LC-Test-$LC_TS"
LC_PROD="LC-Prod-$LC_TS"
LC_OUT="LC-Out-$LC_TS"
for E in "$LC_DEV" "$LC_TEST" "$LC_PROD" "$LC_OUT"; do
  CODE=$(curl_silent -X POST -d "name=$E&csrf_token=$CSRF" "$BASE/environments")
  [[ "$CODE" == "303" ]] || { echo "FAIL: create env $E got $CODE"; exit 1; }
done
LC_DEV_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_DEV</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
LC_TEST_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_TEST</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
LC_PROD_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_PROD</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
LC_OUT_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_OUT</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
echo "Env IDs: dev=$LC_DEV_ID test=$LC_TEST_ID prod=$LC_PROD_ID out=$LC_OUT_ID"

# Lifecycle: Dev -> Test -> Prod
LC_LIFECYCLE_NAME="LC-$LC_TS"
CODE=$(curl_silent -X POST -d "name=$LC_LIFECYCLE_NAME&csrf_token=$CSRF" "$BASE/lifecycles")
[[ "$CODE" == "303" ]] || { echo "FAIL: create lifecycle got $CODE"; exit 1; }
LC_LIFECYCLE_ID=$(curl_body "$BASE/lifecycles" | grep -oP 'href="/lifecycles/\K[0-9]+(?=" class="btn btn-sm btn-ghost">Edit)' | sort -n | tail -1)
echo "Lifecycle ID: $LC_LIFECYCLE_ID"

for EID in "$LC_DEV_ID" "$LC_TEST_ID" "$LC_PROD_ID"; do
  CODE=$(curl_silent -X POST -d "environment_id=$EID&csrf_token=$CSRF" "$BASE/lifecycles/$LC_LIFECYCLE_ID/stages")
  [[ "$CODE" == "303" ]] || { echo "FAIL: add stage env=$EID got $CODE"; exit 1; }
done

# Assign lifecycle to project.
CODE=$(curl_silent -X PUT -d "name=$LC_NAME&description=&lifecycle_id=$LC_LIFECYCLE_ID&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID")
[[ "$CODE" == "303" ]] || { echo "FAIL: assign lifecycle got $CODE"; exit 1; }

# Create one step + one release on the lifecycle project.
CODE=$(curl_silent -X POST -d "name=step1&script_body=exit+0&container_image=$BASH_IMAGE&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create step got $CODE"; exit 1; }
CODE=$(curl_silent -X POST -d "version=1.0.0&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create release got $CODE"; exit 1; }
LC_REL_ID=$(curl_body "$BASE/projects/$LC_PROJECT_ID/releases" | grep -oP 'href="/projects/'$LC_PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "LC Release ID: $LC_REL_ID"

CODE=$(curl -s -b "$COOKIES" -D "$TMP/lc-dev-headers" -o /dev/null -w '%{http_code}' -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_DEV_ID&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "303" ]] || { echo "FAIL: deploy v1 to dev got $CODE, want 303"; exit 1; }
LC_DEV_URL=$(grep -i '^location:' "$TMP/lc-dev-headers" | awk '{print $2}' | tr -d '\r')
LC_DEV_DEP=$(echo "$LC_DEV_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$LC_DEV_DEP" ]] || { echo "FAIL: deploy v1 to dev did not redirect"; exit 1; }
for i in {1..200}; do
  LC_DEV_STATUS=$(curl_body "$BASE/deployments/$LC_DEV_DEP/status")
  echo "$LC_DEV_STATUS" | grep -qE 'failed|succeeded|cancelled' && break
  sleep 0.1
done
echo "$LC_DEV_STATUS" | grep -q succeeded || { echo "FAIL: lifecycle dev deployment did not succeed"; exit 1; }

# Now deploy v1 to Prod directly (skipping Test) -> 422
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_PROD_ID&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy v1 to prod (skipping test) got $CODE, want 422"; exit 1; }
echo "  Dev->Prod skip blocked: OK (422)"

CODE=$(curl -s -b "$COOKIES" -D "$TMP/lc-test-headers" -o /dev/null -w '%{http_code}' -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_TEST_ID&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "303" ]] || { echo "FAIL: deploy v1 to test got $CODE, want 303"; exit 1; }
LC_TEST_URL=$(grep -i '^location:' "$TMP/lc-test-headers" | awk '{print $2}' | tr -d '\r')
LC_TEST_DEP=$(echo "$LC_TEST_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$LC_TEST_DEP" ]] || { echo "FAIL: deploy v1 to test did not redirect"; exit 1; }
for i in {1..200}; do
  LC_TEST_STATUS=$(curl_body "$BASE/deployments/$LC_TEST_DEP/status")
  echo "$LC_TEST_STATUS" | grep -qE 'failed|succeeded|cancelled' && break
  sleep 0.1
done
echo "$LC_TEST_STATUS" | grep -q succeeded || { echo "FAIL: lifecycle test deployment did not succeed"; exit 1; }

# Deploy v1 to Prod after Test succeeded -> 303
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_PROD_ID&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "303" ]] || { echo "FAIL: deploy v1 to prod after test got $CODE, want 303"; exit 1; }
echo "  Full Dev->Test->Prod chain: OK (303)"

# Now create v2 release, attempt to deploy to Prod without going through Dev/Test -> 422
CODE=$(curl_silent -X POST -d "version=2.0.0&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create v2 got $CODE"; exit 1; }
V2_REL_ID=$(curl_body "$BASE/projects/$LC_PROJECT_ID/releases" | grep -oP 'href="/projects/'$LC_PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
CODE=$(curl_silent -X POST -d "release_id=$V2_REL_ID&environment_id=$LC_PROD_ID&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy v2 to prod (no chain) got $CODE, want 422"; exit 1; }
echo "  New version without chain: blocked (422)"

echo "=== F3.5b: Approval Gate ==="
# A separate lifecycle where the prod stage requires approval. Deployments
# to prod should pause at pending_approval until explicitly approved.
for E in app-dev app-staging app-prod; do
  CODE=$(curl_silent -X POST -d "name=$E&csrf_token=$CSRF" "$BASE/environments")
  [[ "$CODE" == "303" ]] || { echo "FAIL: create env $E got $CODE"; exit 1; }
done
APP_DEV_ID=$(api_item_id_by_name "$BASE/api/v1/environments" "app-dev")
APP_STAGING_ID=$(api_item_id_by_name "$BASE/api/v1/environments" "app-staging")
APP_PROD_ID=$(api_item_id_by_name "$BASE/api/v1/environments" "app-prod")
echo "App Env IDs: dev=$APP_DEV_ID staging=$APP_STAGING_ID prod=$APP_PROD_ID"

CODE=$(curl_silent -X POST -d "name=app-lifecycle&csrf_token=$CSRF" "$BASE/lifecycles")
[[ "$CODE" == "303" ]] || { echo "FAIL: create app-lifecycle got $CODE"; exit 1; }
APP_LC_ID=$(api_item_id_by_name "$BASE/api/v1/lifecycles" "app-lifecycle")
echo "App Lifecycle ID: $APP_LC_ID"

for EID in "$APP_DEV_ID" "$APP_STAGING_ID" "$APP_PROD_ID"; do
  CODE=$(curl_silent -X POST -d "environment_id=$EID&csrf_token=$CSRF" "$BASE/lifecycles/$APP_LC_ID/stages")
  [[ "$CODE" == "303" ]] || { echo "FAIL: add stage env=$EID got $CODE"; exit 1; }
done

APP_PROD_STAGE_ID=$(api_lifecycle_stage_id "$APP_LC_ID" "$APP_PROD_ID")
CODE=$(curl_silent -X PATCH -d "requires_approval=1&csrf_token=$CSRF" "$BASE/lifecycles/$APP_LC_ID/stages/$APP_PROD_STAGE_ID")
[[ "$CODE" == "303" ]] || { echo "FAIL: patch prod stage got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "name=AppProject&csrf_token=$CSRF" "$BASE/projects")
[[ "$CODE" == "303" ]] || { echo "FAIL: create app project got $CODE"; exit 1; }
APP_PROJ_ID=$(api_item_id_by_name "$BASE/api/v1/projects" "AppProject")
echo "App Project ID: $APP_PROJ_ID"

CODE=$(curl_silent -X PUT -d "name=AppProject&description=&lifecycle_id=$APP_LC_ID&csrf_token=$CSRF" "$BASE/projects/$APP_PROJ_ID")
[[ "$CODE" == "303" ]] || { echo "FAIL: assign lifecycle to app project got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "name=app-step&script_body=exit+0&container_image=$BASH_IMAGE&csrf_token=$CSRF" "$BASE/projects/$APP_PROJ_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create app step got $CODE"; exit 1; }
CODE=$(curl_silent -X POST -d "version=1.0.0&csrf_token=$CSRF" "$BASE/projects/$APP_PROJ_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create app release got $CODE"; exit 1; }
APP_REL_ID=$(curl_body "$BASE/projects/$APP_PROJ_ID/releases" | grep -oP 'href="/projects/'$APP_PROJ_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "App Release ID: $APP_REL_ID"

# Deploy to dev -> should succeed
DEV_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$APP_REL_ID&environment_id=$APP_DEV_ID&csrf_token=$CSRF" "$BASE/projects/$APP_PROJ_ID/deploy" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
DEV_DEP=$(echo "$DEV_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$DEV_DEP" ]] || { echo "FAIL: dev deployment did not redirect"; exit 1; }
echo "Dev Deployment ID: $DEV_DEP"
for i in {1..200}; do
  if curl_body "$BASE/deployments/$DEV_DEP/status" | grep -q 'succeeded'; then break; fi
  sleep 0.1
done
curl_body "$BASE/deployments/$DEV_DEP/status" | grep -q succeeded || { echo "FAIL: app dev deployment did not succeed"; exit 1; }
echo "  Dev deploy succeeded: OK"

# Deploy to staging -> should succeed
STAGING_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$APP_REL_ID&environment_id=$APP_STAGING_ID&csrf_token=$CSRF" "$BASE/projects/$APP_PROJ_ID/deploy" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
STAGING_DEP=$(echo "$STAGING_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$STAGING_DEP" ]] || { echo "FAIL: staging deployment did not redirect"; exit 1; }
echo "Staging Deployment ID: $STAGING_DEP"
for i in {1..200}; do
  if curl_body "$BASE/deployments/$STAGING_DEP/status" | grep -q 'succeeded'; then break; fi
  sleep 0.1
done
curl_body "$BASE/deployments/$STAGING_DEP/status" | grep -q succeeded || { echo "FAIL: app staging deployment did not succeed"; exit 1; }
echo "  Staging deploy succeeded: OK"

# Deploy to prod -> should be pending_approval
PROD_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$APP_REL_ID&environment_id=$APP_PROD_ID&csrf_token=$CSRF" "$BASE/projects/$APP_PROJ_ID/deploy" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
PROD_DEP=$(echo "$PROD_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$PROD_DEP" ]] || { echo "FAIL: prod deployment did not redirect"; exit 1; }
echo "Prod Deployment ID: $PROD_DEP"

PROD_STATUS=$(curl_body "$BASE/deployments/$PROD_DEP/status")
echo "$PROD_STATUS" | grep -q "pending_approval" || { echo "FAIL: prod deployment not pending_approval"; exit 1; }
echo "  Prod deploy pending_approval: OK"

# Approve and run
CODE=$(curl_silent -X POST -d "approved_by=alice&csrf_token=$CSRF" "$BASE/deployments/$PROD_DEP/approve")
[[ "$CODE" == "303" ]] || { echo "FAIL: approve prod deploy got $CODE"; exit 1; }

for i in {1..100}; do
  PROD_STATUS=$(curl_body "$BASE/deployments/$PROD_DEP/status")
  if echo "$PROD_STATUS" | grep -qE 'failed|succeeded|cancelled'; then break; fi
  sleep 0.2
done
echo "$PROD_STATUS" | grep -q 'succeeded' || { echo "FAIL: prod deploy did not succeed after approval, got status: $PROD_STATUS"; exit 1; }
echo "  Prod deploy succeeded after approval: OK"

APPROVAL_RECORDED=$(api_get "$BASE/api/v1/admin/audit?action=approve_deployment" | python3 -c '
import json
import sys

admin_id = int(sys.argv[1])
for item in json.load(sys.stdin)["items"]:
    user_id = item.get("user_id")
    if isinstance(user_id, dict):
        user_id = user_id.get("Int64") if user_id.get("Valid") else None
    if item["action"] == "approve_deployment" and user_id == admin_id:
        print("1")
        break
' "$ADMIN_ID")
[[ "$APPROVAL_RECORDED" == "1" ]] || { echo "FAIL: approval not recorded"; exit 1; }
echo "  Approval recorded for alice: OK"

echo "=== F3.6: Force Deploy ==="
# Create v3 release, deploy directly to Prod with force=true -> 303
CODE=$(curl_silent -X POST -d "version=3.0.0&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create v3 got $CODE"; exit 1; }
V3_REL_ID=$(curl_body "$BASE/projects/$LC_PROJECT_ID/releases" | grep -oP 'href="/projects/'$LC_PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
CODE=$(curl_silent -X POST -d "release_id=$V3_REL_ID&environment_id=$LC_PROD_ID&force=true&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "303" ]] || { echo "FAIL: force deploy v3 to prod got $CODE, want 303"; exit 1; }
echo "  Force deploy to prod: OK (303)"

echo "=== F3.7: Env Restriction ==="
# Project is bound to lifecycle. Try to deploy v3 to the "out" env (not in lifecycle).
# Force should NOT bypass this restriction.
CODE=$(curl_silent -X POST -d "release_id=$V3_REL_ID&environment_id=$LC_OUT_ID&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy to non-lifecycle env got $CODE, want 422"; exit 1; }
echo "  Deploy to non-lifecycle env (no force): blocked (422)"
CODE=$(curl_silent -X POST -d "release_id=$V3_REL_ID&environment_id=$LC_OUT_ID&force=true&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "422" ]] || { echo "FAIL: force deploy to non-lifecycle env got $CODE, want 422"; exit 1; }
echo "  Force deploy to non-lifecycle env: still blocked (422)"

echo "=== F3.8: Deploy Page ==="
# Verify the dedicated deploy page renders for the existing TestProject
# (free-floating, has release 1.0.0 and env TestEnv). A second test exercises
# the lifecycle-bound case via the LC project.
CODE=$(curl_silent "$BASE/projects/$PROJECT_ID/deploy")
[[ "$CODE" == "200" ]] || { echo "FAIL: GET /projects/$PROJECT_ID/deploy got $CODE"; exit 1; }
echo "  Free-floating deploy page renders: OK (200)"

# Page should contain the form with the release version and env name.
PAGE=$(curl_body "$BASE/projects/$PROJECT_ID/deploy")
echo "$PAGE" | grep -q "1.0.0" || { echo "FAIL: release 1.0.0 missing from deploy page"; exit 1; }
echo "$PAGE" | grep -q "TestEnv" || { echo "FAIL: TestEnv missing from deploy page"; exit 1; }
echo "$PAGE" | grep -q "action=\"/projects/$PROJECT_ID/deploy\"" || { echo "FAIL: form action missing"; exit 1; }

# Lifecycle-bound deploy page: only stage envs should appear.
CODE=$(curl_silent "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "200" ]] || { echo "FAIL: GET lifecycle deploy page got $CODE"; exit 1; }
LCPAGE=$(curl_body "$BASE/projects/$LC_PROJECT_ID/deploy")
echo "$LCPAGE" | grep -q "LC-Dev-$LC_TS" || { echo "FAIL: LC-Dev not in lifecycle deploy page"; exit 1; }
echo "$LCPAGE" | grep -q "LC-Test-$LC_TS" || { echo "FAIL: LC-Test not in lifecycle deploy page"; exit 1; }
echo "$LCPAGE" | grep -q "LC-Prod-$LC_TS" || { echo "FAIL: LC-Prod not in lifecycle deploy page"; exit 1; }
echo "$LCPAGE" | grep -q "LC-Out-$LC_TS" && { echo "FAIL: LC-Out should NOT appear in lifecycle deploy page"; exit 1; } || true
echo "  Lifecycle deploy page filters non-stage envs: OK"

# F3.9: POST to the deploy page — env restriction. Try to deploy an
# existing release to a non-lifecycle env via the new page -> 422. The
# success path is already covered by the existing F3.1 inline-form test
# and by the unit tests; the page-specific path is the gate behavior.
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_OUT_ID&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy page gate-block got $CODE, want 422"; exit 1; }
echo "  Deploy page env restriction: blocked (422)"

# F3.10: cross-project release rejected (400) — the project-scoped route
# validates that the release belongs to this project.
curl -s -b "$COOKIES" -o /dev/null -X POST -d "name=DP-cross-proj&csrf_token=$CSRF" "$BASE/projects"
CROSS_PROJ_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | sort -n | tail -1)
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_DEV_ID&csrf_token=$CSRF" "$BASE/projects/$CROSS_PROJ_ID/deploy")
[[ "$CODE" == "400" ]] || { echo "FAIL: cross-project deploy got $CODE, want 400"; exit 1; }
echo "  Cross-project release rejected: 400"

echo "=== F3.11: Scheduled Deployment ==="
CODE=$(curl_silent -X POST -d "release_id=$RELEASE_ID&environment_id=$ENV_ID&cron=*+*+*+*+*&note=e2e-scheduled&enabled=1&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/schedules")
[[ "$CODE" == "303" ]] || { echo "FAIL: create schedule got $CODE"; exit 1; }

BEFORE_DEP=$(curl_body "$BASE/deployments" | grep -oP 'href="/deployments/\K[0-9]+' | sort -n | tail -1)
echo "Latest deployment before schedule: $BEFORE_DEP"

echo "  Waiting for scheduler tick..."
AFTER_DEP="$BEFORE_DEP"
for i in {1..130}; do
    AFTER_DEP=$(curl_body "$BASE/deployments" | grep -oP 'href="/deployments/\K[0-9]+' | sort -n | tail -1)
    [[ "$AFTER_DEP" -gt "$BEFORE_DEP" ]] && break
    sleep 1
done
echo "Latest deployment after schedule: $AFTER_DEP"
[[ "$AFTER_DEP" -gt "$BEFORE_DEP" ]] || { echo "FAIL: scheduler did not create a new deployment"; exit 1; }

DEP_PAGE=$(curl_body "$BASE/deployments/$AFTER_DEP")
echo "$DEP_PAGE" | grep -q "Scheduled:" || { echo "FAIL: scheduled deployment note missing 'Scheduled:'"; exit 1; }
echo "  Scheduled deployment created with note: OK"

SCHEDULED_STATUS=""
for i in {1..200}; do
    SCHEDULED_STATUS=$(api_get "$BASE/api/v1/deployments/$AFTER_DEP/status" \
        | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])')
    [[ "$SCHEDULED_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.1
done
[[ "$SCHEDULED_STATUS" == "succeeded" ]] || {
    echo "FAIL: scheduled deployment status=$SCHEDULED_STATUS"; exit 1;
}
echo "  Scheduled deployment finished before later write contracts: OK"

SCHED_LIST=$(curl_body "$BASE/projects/$PROJECT_ID/schedules")
echo "$SCHED_LIST" | grep -qF "* * * * *" || { echo "FAIL: schedule missing from list"; exit 1; }
echo "$SCHED_LIST" | grep -q "On" || { echo "FAIL: schedule not enabled"; exit 1; }
echo "  Schedule list shows enabled schedule with future next_run_at: OK"

echo "=== F3.12: User Management (P1-2) ==="
# List the seed admin in /admin/users.
CODE=$(curl_silent "$BASE/admin/users")
[[ "$CODE" == "200" ]] || { echo "FAIL: admin GET /admin/users got $CODE, want 200"; exit 1; }
USERS_PAGE=$(curl_body "$BASE/admin/users")
echo "$USERS_PAGE" | grep -qF "$ADMIN_EMAIL" || { echo "FAIL: admin user not in /admin/users list"; exit 1; }
echo "  Admin lists /admin/users with seed admin: OK"

# Create a new deployer via POST /admin/users.
NEW_EMAIL="e2e-newdeployer@test.local"
NEW_PASS="newdeployer-pass-1234"
REDIR=$(curl -s -b "$COOKIES" -D - -o /dev/null \
    -X POST -d "email=$NEW_EMAIL&name=NewDeployer&role=deployer&password=$NEW_PASS&password_confirmation=$NEW_PASS&csrf_token=$CSRF" \
    "$BASE/admin/users" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
[[ "$REDIR" == /admin/users ]] || { echo "FAIL: create user redirect = $REDIR, want /admin/users"; exit 1; }
echo "  POST /admin/users created user without exposing password: OK"

# The new user can log in with the chosen password.
NEW_LOGIN=$(mktemp)
CODE=$(curl -s -c "$NEW_LOGIN" -o /dev/null -w "%{http_code}" \
    -X POST -d "email=$NEW_EMAIL&password=$NEW_PASS" "$BASE/login")
[[ "$CODE" == "303" ]] || { echo "FAIL: new user login got $CODE, want 303"; exit 1; }
echo "  New user can log in: OK"

# The freshly-created deployer cannot access /admin/users.
CODE=$(curl -s -b "$NEW_LOGIN" -o /dev/null -w "%{http_code}" "$BASE/admin/users")
[[ "$CODE" == "403" ]] || { echo "FAIL: non-admin GET /admin/users got $CODE, want 403"; exit 1; }
echo "  Non-admin gets 403 on /admin/users: OK"

# Promote the new user to admin via PUT /admin/users/{id}.
NEW_USER_ID=$(api_get "$BASE/api/v1/admin/users?limit=1000" | python3 -c '
import json
import sys

email = sys.argv[1]
for user in json.load(sys.stdin)["items"]:
    if user["email"] == email:
        print(user["id"])
        break
' "$NEW_EMAIL")
[[ -n "$NEW_USER_ID" ]] || { echo "FAIL: created user not found in API"; exit 1; }
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -H "X-CSRF-Token: $CSRF" \
    -X PUT -d "name=NewDeployer&role=admin" \
    "$BASE/admin/users/$NEW_USER_ID")
[[ "$CODE" == "303" || "$CODE" == "200" ]] || { echo "FAIL: PUT /admin/users/{id} got $CODE, want 303/200"; exit 1; }
NEW_ROLE=$(api_get "$BASE/api/v1/admin/users/$NEW_USER_ID" | python3 -c "import sys,json; print(json.load(sys.stdin)['role'])")
[[ "$NEW_ROLE" == "admin" ]] || { echo "FAIL: user role = $NEW_ROLE, want admin"; exit 1; }
echo "  PUT /admin/users/{id} promotes deployer to admin: OK"

# The new user's previous session should be deleted (role change).
CODE=$(curl -s -b "$NEW_LOGIN" -o /dev/null -w "%{http_code}" "$BASE/")
[[ "$CODE" == "303" ]] || { echo "FAIL: new user's session still exists after role change"; exit 1; }
echo "  Role change invalidated new user's session: OK"

# Admin cannot delete themselves.
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -H "X-CSRF-Token: $CSRF" \
    -X DELETE "$BASE/admin/users/$ADMIN_ID")
[[ "$CODE" == "422" ]] || { echo "FAIL: self-delete got $CODE, want 422"; exit 1; }
echo "  Self-delete rejected: OK"

# Demote the new user back to deployer and delete them (no project membership).
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -H "X-CSRF-Token: $CSRF" \
    -X PUT -d "name=NewDeployer&role=deployer" \
    "$BASE/admin/users/$NEW_USER_ID")
[[ "$CODE" == "303" || "$CODE" == "200" ]] || { echo "FAIL: demote got $CODE, want 303/200"; exit 1; }
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -H "X-CSRF-Token: $CSRF" \
    -X DELETE "$BASE/admin/users/$NEW_USER_ID")
[[ "$CODE" == "303" || "$CODE" == "200" ]] || { echo "FAIL: delete got $CODE, want 303/200"; exit 1; }
CODE=$(api_get_code "$BASE/api/v1/admin/users/$NEW_USER_ID")
[[ "$CODE" == "404" ]] || { echo "FAIL: deleted user still exists"; exit 1; }
echo "  Admin can delete the new user: OK"

# Audit log captured the user-management actions.
USER_AUDIT=$(api_get "$BASE/api/v1/admin/audit?limit=1000" | python3 -c '
import json
import sys

actions = {"create_user", "update_user", "delete_user"}
print(sum(item["action"] in actions for item in json.load(sys.stdin)["items"]))
')
[[ "$USER_AUDIT" -ge 4 ]] || { echo "FAIL: expected >=4 user audit rows, got $USER_AUDIT"; exit 1; }
echo "  Audit log captured create/update/delete_user: OK"

rm -f "$NEW_LOGIN"

echo "=== API tests ==="

# A2: Health check is public; authenticated call also succeeds.
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/v1/healthz")
[[ "$CODE" == "200" ]] || { echo "FAIL: public health check got $CODE, want 200"; exit 1; }
CODE=$(curl -s -H "Authorization: Bearer $API_TOKEN" -o /dev/null -w "%{http_code}" "$BASE/api/v1/healthz")
[[ "$CODE" == "200" ]] || { echo "FAIL: authenticated health check got $CODE, want 200"; exit 1; }
echo "  Health check: OK"

# A2.1: Agent skills discovery is public, no auth needed.
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/.well-known/skills/index.json")
[[ "$CODE" == "200" ]] || { echo "FAIL: skills index got $CODE, want 200"; exit 1; }
curl -s "$BASE/.well-known/skills/index.json" | grep -q '"durpdeploy"' || { echo "FAIL: skills index missing durpdeploy"; exit 1; }
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/.well-known/skills/durpdeploy/SKILL.md")
[[ "$CODE" == "200" ]] || { echo "FAIL: SKILL.md got $CODE, want 200"; exit 1; }
SKILL_DOCUMENT=$(curl -s "$BASE/.well-known/skills/durpdeploy/SKILL.md")
grep -q '^name: durpdeploy$' <<<"$SKILL_DOCUMENT" || { echo "FAIL: SKILL.md frontmatter missing name"; exit 1; }
grep -q '^## Runbooks$' <<<"$SKILL_DOCUMENT" || { echo "FAIL: SKILL.md missing runbook guidance"; exit 1; }
echo "  Agent skills discovery: OK"

# A3: Project CRUD.
API_PROJECT=$(api_post '{"name":"e2e-api-project"}' "$BASE/api/v1/projects")
API_PROJECT_ID=$(echo "$API_PROJECT" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$API_PROJECT_ID" ]] || { echo "FAIL: create project did not return id: $API_PROJECT"; exit 1; }
CODE=$(api_get_code "$BASE/api/v1/projects")
[[ "$CODE" == "200" ]] || { echo "FAIL: list projects got $CODE, want 200"; exit 1; }
API_PROJECT_NAME=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID" | python3 -c "import sys,json; print(json.load(sys.stdin)['name'])")
[[ "$API_PROJECT_NAME" == "e2e-api-project" ]] || { echo "FAIL: project name = $API_PROJECT_NAME"; exit 1; }
echo "  Project CRUD: OK ($API_PROJECT_ID)"

# A4: Environment CRUD.
API_ENV=$(api_post '{"name":"dev"}' "$BASE/api/v1/environments")
API_ENV_ID=$(echo "$API_ENV" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$API_ENV_ID" ]] || { echo "FAIL: create env did not return id: $API_ENV"; exit 1; }
echo "  Environment CRUD: OK ($API_ENV_ID)"

CODE=$(api_post_code "{\"name\":\"bare-path-step\",\"script_body\":\"echo ready\",\"container_image\":\"$BASH_IMAGE\"}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/steps")
[[ "$CODE" == "201" ]] || { echo "FAIL: bare-path step create got $CODE"; exit 1; }
STEP_ID=$(api_item_id_by_name "$BASE/api/v1/projects/$API_PROJECT_ID/steps" bare-path-step)
[[ -n "$STEP_ID" ]] || { echo "FAIL: bare-path step not persisted"; exit 1; }

CODE=$(api_post_code '{"name":"BARE_PATH_VAR","value":"ready"}' \
    "$BASE/api/v1/projects/$API_PROJECT_ID/variables")
[[ "$CODE" == "201" ]] || { echo "FAIL: bare-path variable create got $CODE"; exit 1; }
VAR_ID=$(api_item_id_by_name "$BASE/api/v1/projects/$API_PROJECT_ID/variables" BARE_PATH_VAR)
[[ -n "$VAR_ID" ]] || { echo "FAIL: bare-path variable not persisted"; exit 1; }

CODE=$(api_post_code "{\"name\":\"bare-path-template\",\"script_body\":\"echo ready\",\"container_image\":\"$BASH_IMAGE\"}" \
    "$BASE/api/v1/templates")
[[ "$CODE" == "201" ]] || { echo "FAIL: bare-path template create got $CODE"; exit 1; }
TEMPLATE_ID=$(api_item_id_by_name "$BASE/api/v1/templates" bare-path-template)
[[ -n "$TEMPLATE_ID" ]] || { echo "FAIL: bare-path template not persisted"; exit 1; }
echo "  Bare-path API step, variable, and template writes: OK"

# Exercise streams through the running binary's real middleware stack.
# Curl's timeout is expected: these endpoints stay open after replay.
assert_log_stream() {
    local path="$1" marker="$2" content_type="$3" code status=0
    code=$(curl -sS -N -m 2 -b "$COOKIES" \
        -H "Authorization: Bearer $API_TOKEN" \
        -D "$TMP/stream-headers" -o "$TMP/stream-body" -w '%{http_code}' \
        "$BASE$path" 2>/dev/null) || status=$?
    [[ "$status" == "28" && "$code" == "200" ]] || {
        echo "FAIL: stream $path returned code=$code curl=$status"; exit 1
    }
    grep -qi "^content-type: $content_type" "$TMP/stream-headers" || {
        echo "FAIL: stream $path has wrong content type"; exit 1
    }
    grep -q "$marker" "$TMP/stream-body" || {
        echo "FAIL: stream $path did not replay expected log"; exit 1
    }
}

echo "=== Runbook API and web contracts ==="
RUNBOOK_ENV=$(api_post '{"name":"runbook-e2e-env"}' "$BASE/api/v1/environments")
RUNBOOK_ENV_ID=$(echo "$RUNBOOK_ENV" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
RUNBOOK_CREATED=$(api_post "{\"name\":\"e2e-maintenance\",\"steps\":[{\"name\":\"inspect\",\"script_body\":\"printf runbook-e2e-v1\",\"interpreter\":\"bash\",\"container_image\":\"$BASH_IMAGE\"}]}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks")
RUNBOOK_ID=$(echo "$RUNBOOK_CREATED" | python3 -c 'import sys,json; print(json.load(sys.stdin)["runbook"]["id"])')
RUNBOOK_V1=$(echo "$RUNBOOK_CREATED" | python3 -c 'import sys,json; print(json.load(sys.stdin)["version"]["id"])')
RUNBOOK_UPDATED=$(api_put "{\"steps\":[{\"name\":\"inspect\",\"script_body\":\"printf runbook-e2e-v2\",\"interpreter\":\"bash\",\"container_image\":\"$BASH_IMAGE\"}]}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID")
RUNBOOK_V2=$(echo "$RUNBOOK_UPDATED" | python3 -c 'import sys,json; print(json.load(sys.stdin)["version"]["id"])')
[[ "$RUNBOOK_V1" != "$RUNBOOK_V2" ]] || { echo "FAIL: runbook version did not advance"; exit 1; }
RUNBOOK_PAGE=$(curl_body "$BASE/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID?version_id=$RUNBOOK_V1")
grep -q 'runbook-e2e-v1' <<<"$RUNBOOK_PAGE" || { echo "FAIL: browser cannot read pinned runbook version"; exit 1; }
if grep -q 'runbook-e2e-v2' <<<"$RUNBOOK_PAGE"; then
    echo "FAIL: browser version view changed with a later edit"; exit 1
fi
RUNBOOK_EXECUTION=$(api_post "{\"environment_id\":$RUNBOOK_ENV_ID,\"version_id\":$RUNBOOK_V1}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID/executions")
RUNBOOK_EXECUTION_ID=$(echo "$RUNBOOK_EXECUTION" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
RUNBOOK_HISTORY=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions?limit=1&offset=0")
echo "$RUNBOOK_HISTORY" | python3 -c 'import sys,json; p=json.load(sys.stdin); assert p["total"] >= 1 and p["limit"] == 1 and len(p["items"]) == 1'
CODE=$(api_get_code "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions?limit=0")
[[ "$CODE" == "400" ]] || { echo "FAIL: invalid runbook history limit got $CODE"; exit 1; }
for i in {1..100}; do
    RUNBOOK_STATUS=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RUNBOOK_EXECUTION_ID" \
        | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["runbook_name"] == "e2e-maintenance", d; print(d["status"])')
    [[ "$RUNBOOK_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.1
done
[[ "$RUNBOOK_STATUS" == "succeeded" ]] || { echo "FAIL: runbook execution status=$RUNBOOK_STATUS"; exit 1; }
RUNBOOK_LOGS=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RUNBOOK_EXECUTION_ID/logs")
grep -q 'runbook-e2e-v1' <<<"$RUNBOOK_LOGS" || { echo "FAIL: pinned runbook logs missing"; exit 1; }
if grep -q 'runbook-e2e-v2' <<<"$RUNBOOK_LOGS"; then
    echo "FAIL: pinned runbook used a later version"; exit 1
fi
assert_log_stream "/projects/$API_PROJECT_ID/runbooks/executions/$RUNBOOK_EXECUTION_ID/logs/stream" 'runbook-e2e-v1' 'text/event-stream'
assert_log_stream "/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RUNBOOK_EXECUTION_ID/logs/stream" 'runbook-e2e-v1' 'text/event-stream'
assert_log_stream "/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RUNBOOK_EXECUTION_ID/logs/stream?format=ndjson" 'runbook-e2e-v1' 'application/x-ndjson'
echo "  Web/API runbook streams: OK"
RUNBOOK_RETRY=$(api_post '{}' \
    "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RUNBOOK_EXECUTION_ID/retry")
RUNBOOK_RETRY_ID=$(echo "$RUNBOOK_RETRY" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
for i in {1..100}; do
    RUNBOOK_RETRY_STATUS=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RUNBOOK_RETRY_ID" \
        | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])')
    [[ "$RUNBOOK_RETRY_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.1
done
[[ "$RUNBOOK_RETRY_STATUS" == "succeeded" ]] || { echo "FAIL: runbook retry status=$RUNBOOK_RETRY_STATUS"; exit 1; }
RUNBOOK_SCHEDULE=$(api_post "{\"environment_id\":$RUNBOOK_ENV_ID,\"version_id\":$RUNBOOK_V1,\"cron\":\"0 3 * * *\"}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID/schedules")
RUNBOOK_SCHEDULE_ID=$(echo "$RUNBOOK_SCHEDULE" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
RUNBOOK_SCHEDULE_PAGE=$(curl_body "$BASE/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID")
grep -q 'space-y-3 md:hidden' <<<"$RUNBOOK_SCHEDULE_PAGE" || { echo "FAIL: mobile schedule cards missing"; exit 1; }
grep -q 'Next run:' <<<"$RUNBOOK_SCHEDULE_PAGE" || { echo "FAIL: schedule next run missing from browser"; exit 1; }
grep -q "/schedules/$RUNBOOK_SCHEDULE_ID/disable" <<<"$RUNBOOK_SCHEDULE_PAGE" || { echo "FAIL: schedule action missing from browser"; exit 1; }
if [[ "${DURPDEPLOY_RUNBOOK_BROWSER_E2E:-0}" == "1" ]]; then
    DURPDEPLOY_RUNBOOK_BROWSER_BASE="$BASE" \
    DURPDEPLOY_RUNBOOK_BROWSER_PROJECT_ID="$API_PROJECT_ID" \
    DURPDEPLOY_RUNBOOK_BROWSER_RUNBOOK_ID="$RUNBOOK_ID" \
    DURPDEPLOY_RUNBOOK_BROWSER_SCHEDULE_ID="$RUNBOOK_SCHEDULE_ID" \
    DURPDEPLOY_RUNBOOK_BROWSER_ENVIRONMENT_ID="$RUNBOOK_ENV_ID" \
    DURPDEPLOY_RUNBOOK_BROWSER_APPROVAL_PROJECT_ID="$APP_PROJ_ID" \
    DURPDEPLOY_RUNBOOK_BROWSER_APPROVAL_DEV_ID="$APP_DEV_ID" \
    DURPDEPLOY_RUNBOOK_BROWSER_APPROVAL_STAGING_ID="$APP_STAGING_ID" \
    DURPDEPLOY_RUNBOOK_BROWSER_APPROVAL_PROD_ID="$APP_PROD_ID" \
    DURPDEPLOY_RUNBOOK_BROWSER_API_TOKEN="$API_TOKEN" \
    DURPDEPLOY_RUNBOOK_BROWSER_EMAIL="$ADMIN_EMAIL" \
    DURPDEPLOY_RUNBOOK_BROWSER_PASSWORD="$ADMIN_PASS" \
        node "$SCRIPT_DIR/runbook_browser_test.mjs"
fi
RUNBOOK_LIFECYCLE=$(api_post '{"name":"runbook-e2e-lifecycle"}' "$BASE/api/v1/lifecycles")
RUNBOOK_LIFECYCLE_ID=$(echo "$RUNBOOK_LIFECYCLE" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
api_post "{\"environment_id\":$RUNBOOK_ENV_ID}" \
    "$BASE/api/v1/lifecycles/$RUNBOOK_LIFECYCLE_ID/stages" >/dev/null
api_put "{\"name\":\"e2e-api-project\",\"lifecycle_id\":$RUNBOOK_LIFECYCLE_ID}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID" >/dev/null
RUNBOOK_STAGE_ID=$(api_lifecycle_stage_id "$RUNBOOK_LIFECYCLE_ID" "$RUNBOOK_ENV_ID")
CODE=$(api_post_code '{}' "$BASE/api/v1/lifecycles/$RUNBOOK_LIFECYCLE_ID/stages/$RUNBOOK_STAGE_ID/delete")
[[ "$CODE" == "204" ]] || { echo "FAIL: remove runbook lifecycle stage got $CODE"; exit 1; }
RUNBOOK_SCHEDULE_PAGE=$(curl_body "$BASE/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID")
grep -q 'runbook-e2e-env' <<<"$RUNBOOK_SCHEDULE_PAGE" || { echo "FAIL: retained schedule lost environment label"; exit 1; }
if grep -q 'Unknown environment' <<<"$RUNBOOK_SCHEDULE_PAGE"; then
    echo "FAIL: retained schedule shows unknown environment"; exit 1
fi
api_put '{"name":"e2e-api-project","lifecycle_id":0}' \
    "$BASE/api/v1/projects/$API_PROJECT_ID" >/dev/null
api_post '{}' "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID/schedules/$RUNBOOK_SCHEDULE_ID/disable" \
    | python3 -c 'import sys,json; assert json.load(sys.stdin)["enabled"] == 0'
for i in {1..150}; do
    CODE=$(do_delete "$BASE/environments/$RUNBOOK_ENV_ID")
    [[ "$CODE" == "200" ]] && break
    [[ "$CODE" == "409" ]] || { echo "FAIL: web delete executed runbook environment got $CODE"; exit 1; }
    sleep 0.1
done
[[ "$CODE" == "200" ]] || { echo "FAIL: web delete executed runbook environment got $CODE"; exit 1; }
CODE=$(api_get_code "$BASE/api/v1/environments/$RUNBOOK_ENV_ID")
[[ "$CODE" == "404" ]] || { echo "FAIL: deleted runbook environment still exists, status=$CODE"; exit 1; }
echo "  Versioned runbook API execution, schedule, logs, and browser history: OK"

# A4b: Interpreter validation, mixed local execution, immutable snapshots,
# release refresh, and redeployment all use the public API.
echo "=== API interpreter tests ==="
INTERPRETER_PROJECT=$(api_post '{"name":"e2e-interpreters"}' "$BASE/api/v1/projects")
INTERPRETER_PROJECT_ID=$(echo "$INTERPRETER_PROJECT" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
INTERPRETER_ENV=$(api_post '{"name":"interpreter-env"}' "$BASE/api/v1/environments")
INTERPRETER_ENV_ID=$(echo "$INTERPRETER_ENV" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$INTERPRETER_PROJECT_ID" && -n "$INTERPRETER_ENV_ID" ]] || {
    echo "FAIL: could not create interpreter project/environment"; exit 1;
}
api_post \
    '{"name":"INTERPRETER_E2E","value":"container-value"}' \
    "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/variables" >/dev/null
CODE=$(api_post_code '{"name":"no-image","script_body":"true"}' \
    "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/steps")
[[ "$CODE" == "400" ]] || { echo "FAIL: API accepted a local step without an image ($CODE)"; exit 1; }
CODE=$(curl_silent -X POST -d "name=no-image&script_body=true&csrf_token=$CSRF" \
    "$BASE/projects/$INTERPRETER_PROJECT_ID/steps")
[[ "$CODE" == "422" ]] || { echo "FAIL: web accepted a local step without an image ($CODE)"; exit 1; }

CODE=$(api_post_code '{"name":"invalid","interpreter":"/bin/sh"}' \
    "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/steps")
[[ "$CODE" == "400" ]] || { echo "FAIL: invalid interpreter got $CODE, want 400"; exit 1; }

CODE=$(curl_silent -X POST \
    --data-urlencode 'name=invalid-web' \
    --data-urlencode 'script_body=echo nope' \
    -d "interpreter=/bin/sh&sort_order=1&execution_target=local&csrf_token=$CSRF" \
    "$BASE/projects/$INTERPRETER_PROJECT_ID/steps")
[[ "$CODE" == "422" ]] || { echo "FAIL: web invalid interpreter got $CODE, want 422"; exit 1; }

INTERPRETER_BASH_STEP=$(api_post \
    "{\"name\":\"bash-step\",\"script_body\":\"echo bash-e2e\",\"interpreter\":\"bash\",\"container_image\":\"$BASH_IMAGE\"}" \
    "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/steps")
INTERPRETER_PYTHON_STEP=$(api_post \
    "{\"name\":\"python-step\",\"script_body\":\"import os; print(\\\"python-e2e=\\\" + os.environ[\\\"INTERPRETER_E2E\\\"])\",\"interpreter\":\"python3\",\"container_image\":\"$PYTHON_IMAGE\"}" \
    "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/steps")
INTERPRETER_PYTHON_STEP_ID=$(echo "$INTERPRETER_PYTHON_STEP" | python3 -c \
    "import sys,json; d=json.load(sys.stdin); assert d['interpreter']=='python3'; print(d['id'])")
echo "$INTERPRETER_BASH_STEP" | python3 -c \
    "import sys,json; assert json.load(sys.stdin)['interpreter']=='bash'"

INTERPRETER_NEW_STEP_PAGE=$(curl_body \
    "$BASE/projects/$INTERPRETER_PROJECT_ID/steps/new")
echo "$INTERPRETER_NEW_STEP_PAGE" | python3 -c '
import sys

page = sys.stdin.read()
assert "name=\"interpreter\"" in page
assert all(f"value=\"{value}\"" in page for value in ("bash", "pwsh", "python3"))
'
INTERPRETER_EDIT_STEP_PAGE=$(curl_body \
    "$BASE/projects/$INTERPRETER_PROJECT_ID/steps/$INTERPRETER_PYTHON_STEP_ID/edit")
echo "$INTERPRETER_EDIT_STEP_PAGE" | python3 -c '
import re
import sys

page = sys.stdin.read()
assert re.search(r"<option[^>]*value=\"python3\"[^>]*selected", page), page
'
CODE=$(curl_silent -X PUT \
    --data-urlencode 'name=python-step' \
    --data-urlencode 'script_body=import os; print("python-e2e=" + os.environ["INTERPRETER_E2E"])' \
    -d "interpreter=python3&sort_order=2&execution_target=local&container_image=$PYTHON_IMAGE&csrf_token=$CSRF" \
    "$BASE/projects/$INTERPRETER_PROJECT_ID/steps/$INTERPRETER_PYTHON_STEP_ID")
[[ "$CODE" == "200" ]] || { echo "FAIL: web interpreter step update got $CODE"; exit 1; }
INTERPRETER_STEPS_PAGE=$(curl_body \
    "$BASE/projects/$INTERPRETER_PROJECT_ID/steps-page")
grep -q 'Interpreter: python3' <<<"$INTERPRETER_STEPS_PAGE" || {
    echo "FAIL: steps page did not display python3"; exit 1;
}

INTERPRETER_TEMPLATE=$(api_post \
    "{\"name\":\"python-template\",\"script_body\":\"print(\\\"template\\\")\",\"interpreter\":\"python3\",\"container_image\":\"$PYTHON_IMAGE\"}" \
    "$BASE/api/v1/templates")
INTERPRETER_TEMPLATE_ID=$(echo "$INTERPRETER_TEMPLATE" | python3 -c \
    "import sys,json; d=json.load(sys.stdin); assert d['interpreter']=='python3'; print(d['id'])")
INTERPRETER_NEW_TEMPLATE_PAGE=$(curl_body "$BASE/templates/new")
echo "$INTERPRETER_NEW_TEMPLATE_PAGE" | python3 -c '
import sys

page = sys.stdin.read()
assert "name=\"interpreter\"" in page
assert all(f"value=\"{value}\"" in page for value in ("bash", "pwsh", "python3"))
'
INTERPRETER_EDIT_TEMPLATE_PAGE=$(curl_body \
    "$BASE/templates/$INTERPRETER_TEMPLATE_ID/edit")
echo "$INTERPRETER_EDIT_TEMPLATE_PAGE" | python3 -c '
import re
import sys

page = sys.stdin.read()
assert re.search(r"<option[^>]*value=\"python3\"[^>]*selected", page), page
'
CODE=$(curl_silent -X PUT \
    --data-urlencode 'name=python-template' \
    --data-urlencode 'script_body=Write-Output template' \
    -d "interpreter=pwsh&container_image=$PWSH_IMAGE&csrf_token=$CSRF" \
    "$BASE/templates/$INTERPRETER_TEMPLATE_ID")
[[ "$CODE" == "303" ]] || { echo "FAIL: web template interpreter update got $CODE"; exit 1; }
INTERPRETER_TEMPLATES_PAGE=$(curl_body "$BASE/templates")
grep -q 'Interpreter: pwsh' <<<"$INTERPRETER_TEMPLATES_PAGE" || {
    echo "FAIL: templates page did not display pwsh"; exit 1;
}
INTERPRETER_TEMPLATE_HISTORY_PAGE=$(curl_body \
    "$BASE/templates/$INTERPRETER_TEMPLATE_ID/history")
grep -q 'Interpreter: python3' <<<"$INTERPRETER_TEMPLATE_HISTORY_PAGE" || {
    echo "FAIL: template history did not preserve python3"; exit 1;
}
grep -q 'Interpreter: pwsh' <<<"$INTERPRETER_TEMPLATE_HISTORY_PAGE" || {
    echo "FAIL: template history did not display pwsh"; exit 1;
}
INTERPRETER_TEMPLATE_API_UPDATE=$(api_put \
    "{\"name\":\"python-template\",\"script_body\":\"Write-Output template\",\"interpreter\":\"pwsh\",\"container_image\":\"$PWSH_IMAGE\"}" \
    "$BASE/api/v1/templates/$INTERPRETER_TEMPLATE_ID")
echo "$INTERPRETER_TEMPLATE_API_UPDATE" | python3 -c \
    "import sys,json; assert json.load(sys.stdin)['interpreter']=='pwsh'"
INTERPRETER_TEMPLATE_API_HISTORY=$(api_get \
    "$BASE/api/v1/templates/$INTERPRETER_TEMPLATE_ID/history")
echo "$INTERPRETER_TEMPLATE_API_HISTORY" | python3 -c '
import json
import sys

interpreters = {version["interpreter"] for version in json.load(sys.stdin)}
assert {"python3", "pwsh"} <= interpreters, interpreters
'
echo "  Web interpreter forms and template history: OK"

INTERPRETER_RELEASE=$(api_post '{"version":"mixed-v1"}' \
    "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/releases")
INTERPRETER_RELEASE_ID=$(echo "$INTERPRETER_RELEASE" | python3 -c '
import json
import sys

release = json.load(sys.stdin)
steps = {step["name"]: step["interpreter"] for step in json.loads(release["steps_json"])}
assert steps == {"bash-step": "bash", "python-step": "python3"}, steps
print(release["id"])
')
INTERPRETER_RELEASE_PAGE=$(curl_body \
    "$BASE/projects/$INTERPRETER_PROJECT_ID/releases/$INTERPRETER_RELEASE_ID")
grep -q '>python3<' <<<"$INTERPRETER_RELEASE_PAGE" || {
    echo "FAIL: release page did not display python3"; exit 1;
}

INTERPRETER_DEPLOYMENT=$(api_post \
    "{\"release_id\":$INTERPRETER_RELEASE_ID,\"environment_id\":$INTERPRETER_ENV_ID}" \
    "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/deployments")
INTERPRETER_DEPLOYMENT_ID=$(echo "$INTERPRETER_DEPLOYMENT" | python3 -c \
    "import sys,json; print(json.load(sys.stdin)['id'])")
for i in {1..100}; do
    INTERPRETER_STATUS=$(api_get \
        "$BASE/api/v1/deployments/$INTERPRETER_DEPLOYMENT_ID/status" \
        | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
    [[ "$INTERPRETER_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.1
done
[[ "$INTERPRETER_STATUS" == "succeeded" ]] || {
    echo "FAIL: mixed interpreter deployment status=$INTERPRETER_STATUS"; exit 1;
}
INTERPRETER_LOGS=$(api_get \
    "$BASE/api/v1/deployments/$INTERPRETER_DEPLOYMENT_ID/logs")
echo "$INTERPRETER_LOGS" | python3 -c '
import json
import sys

lines = "\n".join(item["line"] for item in json.load(sys.stdin))
assert "bash-e2e" in lines and "python-e2e=container-value" in lines, lines
'
INTERPRETER_DEPLOYMENT_PAGE=$(curl_body \
    "$BASE/deployments/$INTERPRETER_DEPLOYMENT_ID")
grep -q '>python3<' <<<"$INTERPRETER_DEPLOYMENT_PAGE" || {
    echo "FAIL: deployment page did not display python3"; exit 1;
}
echo "  Real Bash/Python containers and default variables: OK"

INTERPRETER_UPDATED_STEP=$(api_put \
    "{\"name\":\"python-step\",\"script_body\":\"Write-Output \\\"powershell-e2e=\\u0024env:INTERPRETER_E2E\\\"\",\"interpreter\":\"powershell\",\"container_image\":\"$PWSH_IMAGE\",\"sort_order\":2}" \
    "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/steps/$INTERPRETER_PYTHON_STEP_ID")
echo "$INTERPRETER_UPDATED_STEP" | python3 -c \
    "import sys,json; assert json.load(sys.stdin)['interpreter']=='pwsh'"
INTERPRETER_REFRESHED=$(api_post '{}' \
    "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/releases/$INTERPRETER_RELEASE_ID/refresh")
echo "$INTERPRETER_REFRESHED" | python3 -c '
import json
import sys

steps = {step["name"]: step["interpreter"] for step in json.loads(json.load(sys.stdin)["steps_json"])}
assert steps["python-step"] == "pwsh", steps
'
INTERPRETER_REFRESHED_RELEASE_PAGE=$(curl_body \
    "$BASE/projects/$INTERPRETER_PROJECT_ID/releases/$INTERPRETER_RELEASE_ID")
grep -q '>pwsh<' <<<"$INTERPRETER_REFRESHED_RELEASE_PAGE" || {
    echo "FAIL: refreshed release page did not display pwsh"; exit 1;
}

INTERPRETER_PWSH_DEPLOYMENT=$(api_post \
    "{\"release_id\":$INTERPRETER_RELEASE_ID,\"environment_id\":$INTERPRETER_ENV_ID}" \
    "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/deployments")
INTERPRETER_PWSH_DEPLOYMENT_ID=$(echo "$INTERPRETER_PWSH_DEPLOYMENT" | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
for i in {1..600}; do
    INTERPRETER_PWSH_STATUS=$(api_get \
        "$BASE/api/v1/deployments/$INTERPRETER_PWSH_DEPLOYMENT_ID/status" \
        | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
    [[ "$INTERPRETER_PWSH_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.2
done
[[ "$INTERPRETER_PWSH_STATUS" == "succeeded" ]] || {
    echo "FAIL: PowerShell deployment status=$INTERPRETER_PWSH_STATUS"; exit 1;
}
INTERPRETER_PWSH_LOGS=$(api_get \
    "$BASE/api/v1/deployments/$INTERPRETER_PWSH_DEPLOYMENT_ID/logs")
echo "$INTERPRETER_PWSH_LOGS" | python3 -c '
import json
import sys

lines = "\n".join(item["line"] for item in json.load(sys.stdin))
assert "powershell-e2e=container-value" in lines, lines
assert "PS />" not in lines, lines
assert "\x1b" not in lines, repr(lines)
'
echo "  Real PowerShell container, default variables, and clean logs: OK"

INTERPRETER_REDEPLOY=$(api_post '{}' \
    "$BASE/api/v1/deployments/$INTERPRETER_DEPLOYMENT_ID/redeploy")
INTERPRETER_REDEPLOY_ID=$(echo "$INTERPRETER_REDEPLOY" | python3 -c \
    "import sys,json; print(json.load(sys.stdin)['id'])")
for i in {1..100}; do
    INTERPRETER_REDEPLOY_STATUS=$(api_get \
        "$BASE/api/v1/deployments/$INTERPRETER_REDEPLOY_ID/status" \
        | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
    [[ "$INTERPRETER_REDEPLOY_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.1
done
[[ "$INTERPRETER_REDEPLOY_STATUS" == "succeeded" ]] || {
    echo "FAIL: frozen interpreter redeploy status=$INTERPRETER_REDEPLOY_STATUS"; exit 1;
}
INTERPRETER_REDEPLOY_LOGS=$(api_get \
    "$BASE/api/v1/deployments/$INTERPRETER_REDEPLOY_ID/logs")
echo "$INTERPRETER_REDEPLOY_LOGS" | python3 -c '
import json
import sys

lines = "\n".join(item["line"] for item in json.load(sys.stdin))
assert "python-e2e=container-value" in lines, lines
assert "powershell-e2e" not in lines, lines
'
INTERPRETER_REDEPLOY_PAGE=$(curl_body \
    "$BASE/deployments/$INTERPRETER_REDEPLOY_ID")
grep -q '>python3<' <<<"$INTERPRETER_REDEPLOY_PAGE" || {
    echo "FAIL: redeployment page did not display frozen python3"; exit 1;
}
if grep -q '>pwsh<' <<<"$INTERPRETER_REDEPLOY_PAGE"; then
    echo "FAIL: redeployment page displayed refreshed pwsh"; exit 1
fi
echo "  Interpreter validation and immutable redeploy snapshot: OK"

MISSING_PROJECT=$(api_post '{"name":"missing-image"}' "$BASE/api/v1/projects")
MISSING_PROJECT_ID=$(echo "$MISSING_PROJECT" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
MISSING_STEP=$(api_post '{"name":"missing-runtime-image","script_body":"echo should-not-run","container_image":"localhost/durpdeploy-e2e-missing:never"}' \
    "$BASE/api/v1/projects/$MISSING_PROJECT_ID/steps")
echo "$MISSING_STEP" | python3 -c 'import sys,json; assert json.load(sys.stdin)["container_image"] == "localhost/durpdeploy-e2e-missing:never"'
MISSING_RELEASE=$(api_post '{"version":"missing-image"}' \
    "$BASE/api/v1/projects/$MISSING_PROJECT_ID/releases")
MISSING_RELEASE_ID=$(echo "$MISSING_RELEASE" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
MISSING_DEP=$(api_post "{\"release_id\":$MISSING_RELEASE_ID,\"environment_id\":$INTERPRETER_ENV_ID}" \
    "$BASE/api/v1/projects/$MISSING_PROJECT_ID/deployments")
MISSING_DEP_ID=$(echo "$MISSING_DEP" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
for i in {1..200}; do
    MISSING_STATUS=$(api_get "$BASE/api/v1/deployments/$MISSING_DEP_ID/status" \
        | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])')
    [[ "$MISSING_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.1
done
[[ "$MISSING_STATUS" == "failed" ]] || { echo "FAIL: missing image status=$MISSING_STATUS"; exit 1; }
MISSING_LOGS=$(api_get "$BASE/api/v1/deployments/$MISSING_DEP_ID/logs")
grep -q 'missing-runtime-image.*failed' <<<"$MISSING_LOGS" || { echo "FAIL: missing image failure not logged"; exit 1; }
echo "  Missing execution image fails when pull fails: OK"

ISOLATION_PROJECT=$(api_post '{"name":"container-isolation"}' "$BASE/api/v1/projects")
ISOLATION_PROJECT_ID=$(echo "$ISOLATION_PROJECT" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ISOLATION_STEP=$(api_post "{\"name\":\"isolation\",\"script_body\":\"test ! -e /data/durpdeploy.db && test ! -e /run/podman/podman.sock && test ! -e /run/user && if (echo hi > /dev/tcp/127.0.0.1/$CONTROL_PLANE_PORT) 2>/dev/null; then exit 1; fi; echo isolated\",\"container_image\":\"$BASH_IMAGE\"}" \
    "$BASE/api/v1/projects/$ISOLATION_PROJECT_ID/steps")
echo "$ISOLATION_STEP" | python3 -c 'import sys,json; assert json.load(sys.stdin)["name"] == "isolation"'
ISOLATION_RELEASE=$(api_post '{"version":"v1"}' "$BASE/api/v1/projects/$ISOLATION_PROJECT_ID/releases")
ISOLATION_RELEASE_ID=$(echo "$ISOLATION_RELEASE" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ISOLATION_DEP=$(api_post "{\"release_id\":$ISOLATION_RELEASE_ID,\"environment_id\":$INTERPRETER_ENV_ID}" \
    "$BASE/api/v1/projects/$ISOLATION_PROJECT_ID/deployments")
ISOLATION_DEP_ID=$(echo "$ISOLATION_DEP" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
for i in {1..200}; do
    ISOLATION_STATUS=$(api_get "$BASE/api/v1/deployments/$ISOLATION_DEP_ID/status" \
        | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])')
    [[ "$ISOLATION_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.1
done
[[ "$ISOLATION_STATUS" == succeeded ]] || { echo "FAIL: step isolation status=$ISOLATION_STATUS"; exit 1; }
api_get "$BASE/api/v1/deployments/$ISOLATION_DEP_ID/logs" | grep -q isolated \
    || { echo "FAIL: isolation probe did not execute"; exit 1; }
echo "  Step cannot reach service state, engine socket, or control plane: OK"

CODE=$(curl_silent -X POST \
    -d "csrf_token=$CSRF" \
    "$BASE/projects/$INTERPRETER_PROJECT_ID/steps/$INTERPRETER_PYTHON_STEP_ID/save-as-template")
[[ "$CODE" == "303" ]] || { echo "FAIL: save step as template got $CODE"; exit 1; }
INTERPRETER_SAVED_TEMPLATE=$(api_get "$BASE/api/v1/templates?limit=1000" | python3 -c '
import json
import sys

for item in json.load(sys.stdin)["items"]:
    if item["name"] == "python-step":
        assert item["interpreter"] == "pwsh", item
        print(item["id"])
        break
')
[[ -n "$INTERPRETER_SAVED_TEMPLATE" ]] || {
    echo "FAIL: saved template was not exposed through the API"; exit 1;
}
CODE=$(curl_silent -X POST \
    -d "csrf_token=$CSRF" \
    "$BASE/projects/$INTERPRETER_PROJECT_ID/steps/from-template/$INTERPRETER_TEMPLATE_ID")
[[ "$CODE" == "200" ]] || { echo "FAIL: insert template got $CODE"; exit 1; }
api_get "$BASE/api/v1/projects/$INTERPRETER_PROJECT_ID/steps?limit=1000" | python3 -c '
import json
import sys

matches = [item for item in json.load(sys.stdin)["items"] if item["name"] == "python-template"]
assert len(matches) == 1 and matches[0]["interpreter"] == "pwsh", matches
'
echo "  Web template save/insert preserves interpreter: OK"

# A5: Step CRUD.
API_STEP=$(api_post "{\"name\":\"long-step\",\"script_body\":\"sleep 10\",\"container_image\":\"$BASH_IMAGE\"}" "$BASE/api/v1/projects/$API_PROJECT_ID/steps")
API_STEP_ID=$(echo "$API_STEP" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$API_STEP_ID" ]] || { echo "FAIL: create step did not return id: $API_STEP"; exit 1; }
echo "  Step CRUD: OK ($API_STEP_ID)"

# A6: Release CRUD.
API_RELEASE=$(api_post '{"version":"v1"}' "$BASE/api/v1/projects/$API_PROJECT_ID/releases")
API_RELEASE_ID=$(echo "$API_RELEASE" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$API_RELEASE_ID" ]] || { echo "FAIL: create release did not return id: $API_RELEASE"; exit 1; }
DELETE_RELEASE=$(api_post '{"version":"delete-api"}' "$BASE/api/v1/projects/$API_PROJECT_ID/releases")
DELETE_RELEASE_ID=$(echo "$DELETE_RELEASE" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
RELEASE_DELETE_URL="$BASE/api/v1/projects/$API_PROJECT_ID/releases/$DELETE_RELEASE_ID"
CODE=$(curl -s -H "Authorization: Bearer $API_TOKEN" -X DELETE -o /dev/null -w '%{http_code}' \
    "$BASE/api/v1/projects/$PROJECT_ID/releases/$DELETE_RELEASE_ID")
[[ "$CODE" == 404 ]] || { echo "FAIL: cross-project release delete got $CODE"; exit 1; }
CODE=$(curl -s -H "Authorization: Bearer $API_TOKEN" -X DELETE -o /dev/null -w '%{http_code}' \
    "$RELEASE_DELETE_URL")
[[ "$CODE" == 204 ]] || { echo "FAIL: API release delete got $CODE"; exit 1; }
[[ "$(api_get_code "$RELEASE_DELETE_URL")" == 404 ]] || { echo "FAIL: deleted release is still readable"; exit 1; }
[[ "$(api_post_code '{}' "$RELEASE_DELETE_URL/refresh")" == 404 ]] \
    || { echo "FAIL: refreshing a deleted release did not return 404"; exit 1; }
[[ "$(api_post_code "{\"release_id\":$DELETE_RELEASE_ID,\"environment_id\":$ENV_ID,\"cron\":\"0 9 * * *\"}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/schedules")" == 404 ]] \
    || { echo "FAIL: scheduling a deleted release did not return 404"; exit 1; }
[[ "$(api_post_code "{\"release_id\":$DELETE_RELEASE_ID,\"environment_id\":$ENV_ID}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/deployments")" == 404 ]] \
    || { echo "FAIL: deploying a deleted release did not return 404"; exit 1; }
[[ "$(curl -s -H "Authorization: Bearer $API_TOKEN" -X DELETE -o /dev/null -w '%{http_code}' "$RELEASE_DELETE_URL")" == 404 ]] \
    || { echo "FAIL: repeated release delete did not return 404"; exit 1; }
SCHEDULE_RELEASE=$(api_post '{"version":"delete-scheduled"}' "$BASE/api/v1/projects/$API_PROJECT_ID/releases")
SCHEDULE_RELEASE_ID=$(echo "$SCHEDULE_RELEASE" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
SCHEDULE=$(api_post "{\"release_id\":$SCHEDULE_RELEASE_ID,\"environment_id\":$ENV_ID,\"cron\":\"0 9 * * *\",\"enabled\":false}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/schedules")
SCHEDULE_ID=$(echo "$SCHEDULE" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
DELETE_HISTORY=$(api_post "{\"release_id\":$SCHEDULE_RELEASE_ID,\"environment_id\":$ENV_ID}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/deployments")
DELETE_HISTORY_ID=$(echo "$DELETE_HISTORY" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
DELETE_HISTORY_STATUS=""
for i in {1..300}; do
    DELETE_HISTORY_STATUS=$(api_get "$BASE/api/v1/deployments/$DELETE_HISTORY_ID/status" \
        | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])')
    [[ "$DELETE_HISTORY_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.1
done
[[ "$DELETE_HISTORY_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] \
    || { echo "FAIL: delete fixture deployment did not finish"; exit 1; }
[[ "$(curl -s -H "Authorization: Bearer $API_TOKEN" -X DELETE -o /dev/null -w '%{http_code}' \
    "$BASE/api/v1/projects/$API_PROJECT_ID/releases/$SCHEDULE_RELEASE_ID")" == 204 ]] \
    || { echo "FAIL: scheduled release was not deleted"; exit 1; }
[[ "$(api_get_code "$BASE/api/v1/projects/$API_PROJECT_ID/schedules/$SCHEDULE_ID")" == 404 ]] \
    || { echo "FAIL: release deletion retained schedule"; exit 1; }
[[ "$(api_get_code "$BASE/api/v1/deployments/$DELETE_HISTORY_ID/status")" == 404 ]] \
    || { echo "FAIL: release deletion retained deployment history"; exit 1; }
[[ "$(api_get_code "$BASE/api/v1/projects/$API_PROJECT_ID/releases/$SCHEDULE_RELEASE_ID")" == 404 ]] \
    || { echo "FAIL: release deletion retained release"; exit 1; }

WEB_DELETE=$(curl -s -b "$COOKIES" -X POST -d "version=delete-web&csrf_token=$CSRF" \
    -o /dev/null -w '%{http_code}' "$BASE/projects/$PROJECT_ID/releases")
[[ "$WEB_DELETE" == 303 ]] || { echo "FAIL: web release create got $WEB_DELETE"; exit 1; }
WEB_RELEASE_ID=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
WEB_URL="$BASE/projects/$PROJECT_ID/releases/$WEB_RELEASE_ID"
curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -q "hx-delete=\"/projects/$PROJECT_ID/releases/$WEB_RELEASE_ID\"" \
    || { echo "FAIL: release list has no delete control"; exit 1; }
curl_body "$WEB_URL" | grep -q "hx-delete=\"/projects/$PROJECT_ID/releases/$WEB_RELEASE_ID\"" \
    || { echo "FAIL: release detail has no delete control"; exit 1; }
[[ "$(curl_silent -X DELETE "$WEB_URL")" == 403 ]] || { echo "FAIL: release delete bypassed CSRF"; exit 1; }
CODE=$(curl -s -b "$COOKIES" -H "X-CSRF-Token: $CSRF" -H 'HX-Request: true' \
    -X DELETE -D "$TMP/delete-headers" -o /dev/null -w '%{http_code}' "$WEB_URL")
[[ "$CODE" == 200 ]] && grep -qi "^HX-Redirect: /projects/$PROJECT_ID/releases" "$TMP/delete-headers" \
    || { echo "FAIL: HTMX release delete did not redirect ($CODE)"; exit 1; }
[[ "$(curl_silent "$WEB_URL")" == 404 ]] || { echo "FAIL: web-deleted release is still readable"; exit 1; }
echo "  Release delete via API and web: OK"

# A6b: Secret variables are never returned in plaintext by ordinary
# API reads (issue #29).
SECRET_E2E_VALUE="e2e-super-secret-$RANDOM"
SECRET_CREATE=$(api_post "{\"name\":\"E2E_SECRET\",\"value\":\"$SECRET_E2E_VALUE\",\"secret\":true}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/variables")
SECRET_VAR_ID=$(echo "$SECRET_CREATE" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$SECRET_VAR_ID" ]] || { echo "FAIL: create secret variable did not return id: $SECRET_CREATE"; exit 1; }
echo "$SECRET_CREATE" | python3 -c '
import json
import sys

body = sys.stdin.read()
data = json.loads(body)
assert data["secret"] == 1, data
assert data["value"] == "", data
assert "e2e-super-secret" not in body, body
'
echo "  Secret create response masked: OK"

api_get "$BASE/api/v1/projects/$API_PROJECT_ID/variables" | python3 -c '
import json
import sys

body = sys.stdin.read()
items = [v for v in json.loads(body)["items"] if v["name"] == "E2E_SECRET"]
assert len(items) == 1, items
assert items[0]["value"] == "", items
assert "e2e-super-secret" not in body, body
'
echo "  Secret list response masked: OK"

api_get "$BASE/api/v1/projects/$API_PROJECT_ID/variables/$SECRET_VAR_ID" | python3 -c '
import json
import sys

body = sys.stdin.read()
data = json.loads(body)
assert data["secret"] == 1 and data["value"] == "", data
assert "e2e-super-secret" not in body, body
'
echo "  Secret get response masked: OK"

SECRET_UPDATE=$(api_put '{"name":"E2E_SECRET","value":"","secret":true}' \
    "$BASE/api/v1/projects/$API_PROJECT_ID/variables/$SECRET_VAR_ID")
echo "$SECRET_UPDATE" | python3 -c '
import json
import sys

body = sys.stdin.read()
data = json.loads(body)
assert data["value"] == "", data
assert "e2e-super-secret" not in body, body
'
echo "  Secret update response masked: OK"

# A variable may be secret with no stored value at all; its snapshot
# must still emit "" and not null. Blank value keeps stored secrets.
SECRET_EMPTY_CREATE=$(api_post '{"name":"E2E_SECRET_EMPTY","secret":true}' \
    "$BASE/api/v1/projects/$API_PROJECT_ID/variables")
SECRET_EMPTY_ID=$(echo "$SECRET_EMPTY_CREATE" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$SECRET_EMPTY_ID" ]] || {
    echo "FAIL: empty secret create got $SECRET_EMPTY_CREATE"; exit 1;
}
SECRET_EMPTY_GET=$(api_get \
    "$BASE/api/v1/projects/$API_PROJECT_ID/variables/$SECRET_EMPTY_ID")
echo "$SECRET_EMPTY_GET" | python3 -c '
import json
import sys

data = json.load(sys.stdin)
assert isinstance(data["value"], str), type(data["value"]).__name__
assert data["value"] == "", data
'
echo "  Valueless secret read masked: OK"

# A non-secret variable with no value must also emit "" (not null) in
# a release snapshot — matching the variables list/get behavior.
PLAIN_EMPTY_CREATE=$(api_post '{"name":"E2E_PLAIN_EMPTY"}' \
    "$BASE/api/v1/projects/$API_PROJECT_ID/variables")
PLAIN_EMPTY_ID=$(echo "$PLAIN_EMPTY_CREATE" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$PLAIN_EMPTY_ID" ]] || {
    echo "FAIL: plain empty create got $PLAIN_EMPTY_CREATE"; exit 1;
}
PLAIN_EMPTY_GET=$(api_get \
    "$BASE/api/v1/projects/$API_PROJECT_ID/variables/$PLAIN_EMPTY_ID")
echo "$PLAIN_EMPTY_GET" | python3 -c '
import json
import sys

data = json.load(sys.stdin)
assert isinstance(data["value"], str), type(data["value"]).__name__
assert data["value"] == "", data
'
echo "  Valueless plain read OK"

# The stored secret survives metadata-only updates and appears in a
# release snapshot response masked.
api_post '{"version":"v-secret"}' "$BASE/api/v1/projects/$API_PROJECT_ID/releases" >/dev/null
api_get "$BASE/api/v1/projects/$API_PROJECT_ID/releases?limit=1000" | python3 -c '
import json
import sys

body = sys.stdin.read()
releases = [r for r in json.loads(body)["items"] if r["version"] == "v-secret"]
assert len(releases) == 1, releases
print(releases[0]["id"])
' >"$TMP/secret-release-id"
SECRET_RELEASE_ID=$(cat "$TMP/secret-release-id")
api_get "$BASE/api/v1/projects/$API_PROJECT_ID/releases/$SECRET_RELEASE_ID" | python3 -c '
import json
import sys

body = sys.stdin.read()
data = json.loads(body)
secret = [v for v in data["variables"] if v["name"] == "E2E_SECRET"]
assert len(secret) == 1, data["variables"]
# The served contract declares value as a nullable string, so a
# masked snapshot must emit "" (not {"String":..,"Valid":..}).
assert isinstance(secret[0]["value"], str), type(secret[0]["value"]).__name__
assert secret[0]["value"] == "", secret
empty = [v for v in data["variables"] if v["name"] == "E2E_SECRET_EMPTY"]
assert len(empty) == 1, data["variables"]
assert isinstance(empty[0]["value"], str), type(empty[0]["value"]).__name__
assert empty[0]["value"] == "", empty
plain_empty = [v for v in data["variables"] if v["name"] == "E2E_PLAIN_EMPTY"]
assert len(plain_empty) == 1, data["variables"]
assert isinstance(plain_empty[0]["value"], str), type(plain_empty[0]["value"]).__name__
assert plain_empty[0]["value"] == "", plain_empty
assert "e2e-super-secret" not in body, body
'
echo "  Release snapshot secret masked: OK"

# A deployment using the masked round-trip still resolves the real
# value: metadata-only update keeps it, the deploy substitutes it into
# the step environment, and the log scrubber redacts it. The secret is
# written in two halves so the scrubber must stitch the literal across
# chunk boundaries; the live ndjson read below catches a regression.
SECRET_MASKED_STEP=$(api_post \
    "{\"name\":\"echo-secret\",\"script_body\":\"echo secret=\u0024E2E_SECRET\",\"container_image\":\"$BASH_IMAGE\",\"variable_names\":[\"E2E_SECRET\"]}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/steps")
SECRET_MASKED_STEP_ID=$(echo "$SECRET_MASKED_STEP" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$SECRET_MASKED_STEP_ID" ]] || { echo "FAIL: masked round-trip step create failed: $SECRET_MASKED_STEP"; exit 1; }
api_post "{\"version\":\"v-secret-deploy\"}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/releases" >/dev/null
SECRET_DEPLOY_RELEASE_ID=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID/releases?limit=1000" | python3 -c '
import json
import sys

body = sys.stdin.read()
releases = [r for r in json.loads(body)["items"] if r["version"] == "v-secret-deploy"]
assert len(releases) == 1, releases
print(releases[0]["id"])
')
api_post "{\"release_id\":$SECRET_DEPLOY_RELEASE_ID,\"environment_id\":$API_ENV_ID}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/deployments" >/dev/null
SECRET_DEPLOY_ID=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID/deployments?limit=1000" | python3 -c '
import json
import sys

body = sys.stdin.read()
deps = [d for d in json.loads(body)["items"] if d["release_id"] == '"$SECRET_DEPLOY_RELEASE_ID"']
assert len(deps) == 1, deps
print(deps[0]["id"])
')
for i in {1..300}; do
    SECRET_DEPLOY_STATUS=$(api_get "$BASE/api/v1/deployments/$SECRET_DEPLOY_ID/status" \
        | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
    [[ "$SECRET_DEPLOY_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.1
done
[[ "$SECRET_DEPLOY_STATUS" == "succeeded" ]] || {
    echo "FAIL: secret round-trip deployment status=$SECRET_DEPLOY_STATUS"; exit 1;
}
SECRET_DEPLOY_LOGS=$(api_get "$BASE/api/v1/deployments/$SECRET_DEPLOY_ID/logs")
echo "$SECRET_DEPLOY_LOGS" | python3 -c '
import json
import sys

body = sys.stdin.read()
lines = "\n".join(item["line"] for item in json.loads(body))
assert "secret=[REDACTED]" in lines, lines
assert "e2e-super-secret" not in body, body
'
echo "  Deploy resolves stored secret despite masked reads: OK"

# A6c: the same secret deployment re-read through the ndjson stream
# endpoint after it completes: redaction that persists in the row
# store must reach the streaming wire too.
SECRET_STREAM_TMP=$(mktemp)
# The stream stays attached after the replay; a bounded 20s window
# costs the suite once per run but keeps the helper simple (the
# awk-based early-exit variant hung in practice).
timeout 20 curl -s -N -H "Authorization: Bearer $API_TOKEN"     "$BASE/api/v1/deployments/$SECRET_DEPLOY_ID/logs/stream?format=ndjson"     >"$SECRET_STREAM_TMP" 2>/dev/null || true
if grep -q "$SECRET_E2E_VALUE" "$SECRET_STREAM_TMP"; then
    echo "FAIL: ndjson stream persisted the secret value:" >&2
    grep "$SECRET_E2E_VALUE" "$SECRET_STREAM_TMP" | head -2 >&2
    rm -f "$SECRET_STREAM_TMP"
    exit 1
fi
LIVE_I=$(grep -c "secret=\[REDACTED\]" "$SECRET_STREAM_TMP" 2>/dev/null) || true
[[ "$LIVE_I" -ge 1 ]] || {
    echo "FAIL: ndjson stream never carried the scrubbed line:" >&2
    head -3 "$SECRET_STREAM_TMP" >&2
    rm -f "$SECRET_STREAM_TMP"
    exit 1
}
rm -f "$SECRET_STREAM_TMP"
echo "  Log streaming redacts stored secret rows: OK"

# A7: Deployment create + status + cancel.
API_DEP=$(api_post "{\"release_id\":$API_RELEASE_ID,\"environment_id\":$API_ENV_ID}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/deployments")
API_DEP_ID=$(echo "$API_DEP" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
[[ -n "$API_DEP_ID" ]] || { echo "FAIL: create deployment did not return id: $API_DEP"; exit 1; }
for i in {1..50}; do
    API_DEP_STATUS=$(api_get "$BASE/api/v1/deployments/$API_DEP_ID/status" | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
    [[ "$API_DEP_STATUS" == "running" ]] && break
    sleep 0.1
done
[[ "$API_DEP_STATUS" == "running" ]] || { echo "FAIL: deployment did not reach running, status=$API_DEP_STATUS"; exit 1; }
echo "  Deployment reached running: OK ($API_DEP_ID)"

API_CANCEL_BODY="$TMP/api-cancel.json"
CODE=$(curl -s -H "Authorization: Bearer $API_TOKEN" -X POST \
    -o "$API_CANCEL_BODY" -w "%{http_code}" \
    "$BASE/api/v1/deployments/$API_DEP_ID/cancel")
[[ "$CODE" == "200" ]] || { echo "FAIL: cancel deployment got $CODE, want 200"; exit 1; }
API_CANCEL_STATUS=$(python3 -c \
    "import sys,json; print(json.load(sys.stdin)['status'])" <"$API_CANCEL_BODY")
[[ "$API_CANCEL_STATUS" == "running" ]] || {
    echo "FAIL: cancel acknowledgement status=$API_CANCEL_STATUS, want running"; exit 1;
}
for i in {1..200}; do
    API_DEP_STATUS=$(api_get "$BASE/api/v1/deployments/$API_DEP_ID/status" | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
    [[ "$API_DEP_STATUS" == "cancelled" ]] && break
    sleep 0.1
done
[[ "$API_DEP_STATUS" == "cancelled" ]] || { echo "FAIL: deployment did not cancel, status=$API_DEP_STATUS"; exit 1; }
echo "  Deployment cancelled: OK"

# A8: Log streaming (ndjson). Use a fresh step + release + deployment so we can
# read at least one line from the live stream and then cancel it.
API_LOG_STEP=$(api_post "{\"name\":\"streamer\",\"script_body\":\"for i in 1 2 3; do echo api-line-\$i; sleep 0.1; done\",\"container_image\":\"$BASH_IMAGE\"}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/steps")
API_LOG_RELEASE=$(api_post '{"version":"v2"}' "$BASE/api/v1/projects/$API_PROJECT_ID/releases")
API_LOG_RELEASE_ID=$(echo "$API_LOG_RELEASE" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
API_LOG_DEP=$(api_post "{\"release_id\":$API_LOG_RELEASE_ID,\"environment_id\":$API_ENV_ID}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/deployments")
API_LOG_DEP_ID=$(echo "$API_LOG_DEP" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")

for i in {1..300}; do
    API_LOG_STATUS=$(api_get "$BASE/api/v1/deployments/$API_LOG_DEP_ID/status" | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
    if [[ "$API_LOG_STATUS" =~ ^(failed|succeeded|cancelled)$ ]]; then break; fi
    sleep 0.1
done
[[ "$API_LOG_STATUS" =~ ^(failed|succeeded|cancelled)$ ]] || {
    echo "FAIL: log deployment did not finish, status=$API_LOG_STATUS"
    exit 1
}

# Read the ndjson stream. The stream replays historical logs first, so we should
# get a line almost immediately; cap the connection at 5s to avoid hanging.
LOGS=$(curl -s -m 5 -H "Authorization: Bearer $API_TOKEN" \
    "$BASE/api/v1/deployments/$API_LOG_DEP_ID/logs/stream?format=ndjson") || true
LOG_LINE=$(echo "$LOGS" | head -1)
echo "$LOG_LINE" | python3 -c "import sys,json; d=json.load(sys.stdin); assert 'line' in d; print('ndjson line OK')"
echo "  Log streaming (ndjson): OK"

assert_log_stream "/deployments/$API_LOG_DEP_ID/logs/stream" 'data:' 'text/event-stream'
assert_log_stream "/api/v1/deployments/$API_LOG_DEP_ID/logs/stream" 'data:' 'text/event-stream'
assert_log_stream "/api/v1/deployments/$API_LOG_DEP_ID/logs/stream?format=ndjson" '"line":' 'application/x-ndjson'
assert_log_stream "/api/v1/deployments/$API_LOG_DEP_ID/events" 'data:' 'text/event-stream'
echo "  Web/API deployment streams: OK"

# A9: Failure paths.
# 401 without token.
CODE=$(api_post_noauth '{"name":"noauth"}' "$BASE/api/v1/projects")
[[ "$CODE" == "401" ]] || { echo "FAIL: no-auth create project got $CODE, want 401"; exit 1; }
echo "  401 without token: OK"

# 404 missing project.
CODE=$(api_get_code "$BASE/api/v1/projects/9999")
[[ "$CODE" == "404" ]] || { echo "FAIL: missing project got $CODE, want 404"; exit 1; }
echo "  404 missing project: OK"

# 403 viewer write block.
VIEWER_EMAIL="e2e-viewer@test.local"
VIEWER_PASS="e2e-viewer-pass-1234"
VIEWER_CREATE=$(api_post "{\"email\":\"$VIEWER_EMAIL\",\"name\":\"E2E Viewer\",\"role\":\"deployer\",\"password\":\"$VIEWER_PASS\"}" \
    "$BASE/api/v1/admin/users")
VIEWER_USER_ID=$(echo "$VIEWER_CREATE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))")
[[ -n "$VIEWER_USER_ID" ]] || { echo "FAIL: viewer user create did not return id: $VIEWER_CREATE"; exit 1; }
VIEWER_LOGIN="$TMP/viewer-cookies"
CODE=$(curl -s -c "$VIEWER_LOGIN" -o /dev/null -w "%{http_code}" \
    -X POST -d "email=$VIEWER_EMAIL&password=$VIEWER_PASS" "$BASE/login")
[[ "$CODE" == "303" ]] || { echo "FAIL: viewer login got $CODE, want 303"; exit 1; }
VIEWER_CSRF=$(csrf_from_cookies "$VIEWER_LOGIN")
[[ -n "$VIEWER_CSRF" ]] || { echo "FAIL: no CSRF token for viewer"; exit 1; }
VIEWER_TOKEN=$(mint_web_token "$VIEWER_LOGIN" e2e-viewer "$VIEWER_CSRF") \
    || { echo "FAIL: could not mint viewer token"; exit 1; }
[[ -n "$VIEWER_TOKEN" ]] || { echo "FAIL: could not mint viewer token"; exit 1; }
VIEWER_UPDATE=$(curl -s -H "Authorization: Bearer $API_TOKEN" \
    -H "Content-Type: application/json" -X PUT \
    -d '{"name":"E2E Viewer","role":"viewer"}' \
    "$BASE/api/v1/admin/users/$VIEWER_USER_ID")
VIEWER_ROLE=$(echo "$VIEWER_UPDATE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('role',''))")
[[ "$VIEWER_ROLE" == "viewer" ]] || { echo "FAIL: viewer role = $VIEWER_ROLE, want viewer"; exit 1; }
CODE=$(curl -s -H "Authorization: Bearer $VIEWER_TOKEN" -H "Content-Type: application/json" \
    -X POST -d '{"name":"viewer-proj"}' -o /dev/null -w "%{http_code}" "$BASE/api/v1/projects")
[[ "$CODE" == "403" ]] || { echo "FAIL: viewer create project got $CODE, want 403"; exit 1; }
echo "  403 viewer write block: OK"

# A10: Swagger endpoints.
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/swagger/")
[[ "$CODE" == "200" ]] || { echo "FAIL: swagger redirect got $CODE, want 200"; exit 1; }
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/swagger/index.html")
[[ "$CODE" == "200" ]] || { echo "FAIL: swagger UI got $CODE, want 200"; exit 1; }
SWAGGER=$(curl -s "$BASE/api/swagger/spec")
echo "$SWAGGER" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['swagger']=='2.0'; print('swagger spec OK')"
echo "  Swagger UI + spec: OK"

CODE=$(curl -s -H "Authorization: Bearer $API_TOKEN" -o /dev/null \
    -w "%{http_code}" -X DELETE "$BASE/api/v1/environments/$API_ENV_ID")
[[ "$CODE" == "204" ]] || { echo "FAIL: API delete executed environment got $CODE"; exit 1; }

echo "=== APPLICATION E2E CHECKS PASSED ==="

echo "=== MFA HTTP contracts ==="

# The initial login above proves the unenrolled contract. Enrolling this admin
# must invalidate that browser session, and the second factor must create a new
# one. Values below are deliberately never printed.
CODE=$(curl_silent -X POST -d "password=$ADMIN_PASS&csrf_token=$CSRF" \
    "$BASE/settings/security/reauth")
[[ "$CODE" == "303" ]] || { echo "FAIL: MFA reauthentication got $CODE"; exit 1; }
CSRF=$(csrf_from_cookies "$COOKIES")
TOTP_PAGE="$TMP/totp-enrollment.html"
CODE=$(curl -s -b "$COOKIES" -o "$TOTP_PAGE" -w "%{http_code}" \
    -X POST -d "csrf_token=$CSRF" "$BASE/settings/security/totp/begin")
[[ "$CODE" == "200" ]] || { echo "FAIL: TOTP enrollment begin got $CODE"; exit 1; }
TOTP_SEED=$(grep -oP 'id="totp-manual-key"[^>]*>\K[^<]+' "$TOTP_PAGE")
TOTP_CHALLENGE=$(grep -oP 'name="challenge_token" value="\K[^"]+' "$TOTP_PAGE" | head -1)
TOTP_CHALLENGE_CSRF=$(grep -oP 'name="challenge_csrf" value="\K[^"]+' "$TOTP_PAGE" | head -1)
[[ -n "$TOTP_SEED" && -n "$TOTP_CHALLENGE" && -n "$TOTP_CHALLENGE_CSRF" ]] || {
    echo "FAIL: TOTP enrollment response omitted required state"; exit 1;
}
totp_code() {
    python3 -c '
import base64
import hashlib
import hmac
import struct
import sys
import time

seed = sys.argv[1]
counter = int(time.time()) // 30
digest = hmac.new(base64.b32decode(seed), struct.pack(">Q", counter), hashlib.sha1).digest()
offset = digest[-1] & 15
value = (struct.unpack(">I", digest[offset:offset + 4])[0] & 0x7fffffff) % 1000000
print(f"{value:06d}")
' "$1"
}
TOTP_CODE=$(totp_code "$TOTP_SEED")
RECOVERY_PAGE="$TMP/recovery-codes.html"
CODE=$(curl -s -b "$COOKIES" -c "$COOKIES" -o "$RECOVERY_PAGE" -w "%{http_code}" \
    -X POST -d "csrf_token=$CSRF&challenge_token=$TOTP_CHALLENGE&challenge_csrf=$TOTP_CHALLENGE_CSRF&code=$TOTP_CODE" \
    "$BASE/settings/security/totp/confirm")
[[ "$CODE" == "200" ]] || { echo "FAIL: TOTP enrollment confirm got $CODE"; exit 1; }
RECOVERY_CODE_COUNT=$(grep -o 'class="recovery-code' "$RECOVERY_PAGE" | wc -l)
[[ "$RECOVERY_CODE_COUNT" == "10" ]] || { echo "FAIL: recovery code count got $RECOVERY_CODE_COUNT, want 10"; exit 1; }
RECOVERY_CODE=$(grep -oP 'class="recovery-code[^>]*>\K[^<]+' "$RECOVERY_PAGE" | head -1)
[[ -n "$RECOVERY_CODE" ]] || { echo "FAIL: recovery verification omitted codes"; exit 1; }
CODE=$(curl -s -b "$COOKIES" -c "$COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST "$BASE/settings/security/recovery/continue")
[[ "$CODE" == "303" ]] || { echo "FAIL: recovery continue got $CODE, want 303"; exit 1; }

MFA_COOKIES="$TMP/mfa-cookies"
CODE=$(curl -s -c "$MFA_COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "email=$ADMIN_EMAIL&password=$ADMIN_PASS" "$BASE/login")
[[ "$CODE" == "303" ]] || { echo "FAIL: enrolled password login got $CODE"; exit 1; }
LOCATION=$(curl -s -b "$MFA_COOKIES" -D - -o /dev/null "$BASE/login/mfa" | awk '/^Location:/ {print $2}' | tr -d '\r')
[[ -z "$LOCATION" ]] || { echo "FAIL: pending MFA unexpectedly redirected"; exit 1; }
CODE=$(curl -s -b "$MFA_COOKIES" -o /dev/null -w "%{http_code}" "$BASE/")
[[ "$CODE" == "303" ]] || { echo "FAIL: enrolled password created a browser session"; exit 1; }
MFA_PAGE="$TMP/mfa-login.html"
curl -s -b "$MFA_COOKIES" -o "$MFA_PAGE" "$BASE/login/mfa"
MFA_CSRF=$(grep -oP 'name="csrf_token" value="\K[^"]+' "$MFA_PAGE" | head -1)
[[ -n "$MFA_CSRF" ]] || { echo "FAIL: MFA page omitted CSRF state"; exit 1; }
sleep $((31 - $(date +%s) % 30))
TOTP_CODE=$(totp_code "$TOTP_SEED")
CODE=$(curl -s -b "$MFA_COOKIES" -c "$MFA_COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "csrf_token=$MFA_CSRF&code=$TOTP_CODE" "$BASE/login/mfa/totp")
[[ "$CODE" == "303" ]] || { echo "FAIL: TOTP login completion got $CODE"; exit 1; }
grep -q $'\tsession\t' "$MFA_COOKIES" || { echo "FAIL: TOTP did not issue a browser session"; exit 1; }

MFA_LOGIN_PAGE="$TMP/mfa-login-recovery.html"
curl -s -b "$MFA_COOKIES" -c "$MFA_COOKIES" -o /dev/null -X POST \
    -d "csrf_token=$(csrf_from_cookies "$MFA_COOKIES")" "$BASE/logout"
CODE=$(curl -s -c "$MFA_COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "email=$ADMIN_EMAIL&password=$ADMIN_PASS" "$BASE/login")
[[ "$CODE" == "303" ]] || { echo "FAIL: recovery password login got $CODE"; exit 1; }
curl -s -b "$MFA_COOKIES" -o "$MFA_LOGIN_PAGE" "$BASE/login/mfa"
MFA_CSRF=$(grep -oP 'name="csrf_token" value="\K[^"]+' "$MFA_LOGIN_PAGE" | head -1)
CODE=$(curl -s -b "$MFA_COOKIES" -c "$MFA_COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "csrf_token=$MFA_CSRF&code=$RECOVERY_CODE" "$BASE/login/mfa/recovery")
[[ "$CODE" == "303" ]] || { echo "FAIL: recovery login completion got $CODE"; exit 1; }

curl -s -b "$MFA_COOKIES" -c "$MFA_COOKIES" -o /dev/null -X POST \
    -d "csrf_token=$(csrf_from_cookies "$MFA_COOKIES")" "$BASE/logout"
curl -s -c "$MFA_COOKIES" -o /dev/null -X POST \
    -d "email=$ADMIN_EMAIL&password=$ADMIN_PASS" "$BASE/login"
curl -s -b "$MFA_COOKIES" -o "$MFA_LOGIN_PAGE" "$BASE/login/mfa"
MFA_CSRF=$(grep -oP 'name="csrf_token" value="\K[^"]+' "$MFA_LOGIN_PAGE" | head -1)
CODE=$(curl -s -b "$MFA_COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "csrf_token=$MFA_CSRF&code=$RECOVERY_CODE" "$BASE/login/mfa/recovery")
[[ "$CODE" == "422" ]] || { echo "FAIL: reused recovery code got $CODE"; exit 1; }
echo "  Unenrolled, pending-password, TOTP, recovery, and recovery-replay contracts: OK"
echo "=== ALL E2E CHECKS PASSED ==="
