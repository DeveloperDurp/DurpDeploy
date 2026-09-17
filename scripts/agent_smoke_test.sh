#!/usr/bin/env bash
set -euo pipefail

BASE="${DURPDEPLOY_BASE_URL:-https://localhost:8443}"
BASE="${BASE%/}"
DB="${DURPDEPLOY_DB:-durpdeploy.db}"
ENVIRONMENT="${DURPDEPLOY_AGENT_TEST_ENVIRONMENT:-dev}"
LABEL="${DURPDEPLOY_AGENT_TEST_LABEL:-test}"
ADMIN_EMAIL="${DURPDEPLOY_AGENT_TEST_EMAIL:-e2e-admin@test.local}"
ADMIN_PASSWORD="${DURPDEPLOY_AGENT_TEST_PASSWORD:-e2e-admin-password-1234}"
TIMEOUT_SECONDS="${DURPDEPLOY_AGENT_TEST_TIMEOUT:-60}"

curl_options=(-sS)
if [[ "$BASE" == https://localhost:* || "$BASE" == https://127.0.0.1:* ]]; then
    curl_options+=(-k)
fi

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

db_query() {
    sqlite3 "$DB" "$1"
}

sql_quote() {
    local value=${1//\'/\'\'}
    printf "'%s'" "$value"
}

http_code() {
    awk '/^HTTP\// { code=$2 } END { print code }'
}

wait_for_deployment() {
    local deployment_id=$1
    local deadline=$((SECONDS + TIMEOUT_SECONDS))
    local status

    while ((SECONDS < deadline)); do
        status=$(db_query "SELECT status FROM deployments WHERE id=$deployment_id;")
        case "$status" in
            succeeded|failed|cancelled)
                printf '%s' "$status"
                return
                ;;
        esac
        sleep 1
    done

    local remote_states
    remote_states=$(db_query \
        "SELECT group_concat(step_index || ':' || state, ',')
         FROM remote_step_runs WHERE deployment_id=$deployment_id;")
    fail "deployment $deployment_id timed out in state ${status:-missing}; remote runs: ${remote_states:-none}"
}

create_release() {
    local version=$1
    local code
    code=$(curl "${curl_options[@]}" -H "Cookie: session=$SESSION" \
        -o /dev/null -w '%{http_code}' -X POST \
        --data-urlencode "version=$version" \
        --data-urlencode "csrf_token=$CSRF" \
        "$BASE/projects/$PROJECT_ID/releases")
    [[ "$code" == 303 ]] || fail "create release $version returned HTTP $code"

    db_query "SELECT id FROM releases
              WHERE project_id=$PROJECT_ID AND version=$(sql_quote "$version");"
}

deploy_release() {
    local release_id=$1
    local headers code location deployment_id
    headers=$(curl "${curl_options[@]}" -D - -o /dev/null \
        -H "Cookie: session=$SESSION" -X POST \
        --data-urlencode "release_id=$release_id" \
        --data-urlencode "environment_id=$ENVIRONMENT_ID" \
        --data-urlencode "csrf_token=$CSRF" \
        "$BASE/projects/$PROJECT_ID/deploy")
    code=$(printf '%s\n' "$headers" | http_code)
    [[ "$code" == 303 ]] || fail "start deployment returned HTTP $code"
    location=$(printf '%s\n' "$headers" |
        sed -n 's/^location: \(.*\)\r$/\1/ip' | tail -1)
    deployment_id=${location##*/}
    [[ "$deployment_id" =~ ^[0-9]+$ ]] ||
        fail "deployment redirect did not contain an ID: ${location:-missing}"
    printf '%s' "$deployment_id"
}

assert_remote_runs() {
    local deployment_id=$1
    local step_index=$2
    local expected_state=$3
    local actual_count wrong_count states

    actual_count=$(db_query \
        "SELECT count(*) FROM remote_step_runs
         WHERE deployment_id=$deployment_id AND step_index=$step_index;")
    wrong_count=$(db_query \
        "SELECT count(*) FROM remote_step_runs
         WHERE deployment_id=$deployment_id AND step_index=$step_index
           AND state <> $(sql_quote "$expected_state");")
    states=$(db_query \
        "SELECT group_concat(agent_id || ':' || state, ',')
         FROM remote_step_runs
         WHERE deployment_id=$deployment_id AND step_index=$step_index;")

    [[ "$actual_count" == "$AGENT_COUNT" ]] ||
        fail "deployment $deployment_id step $step_index reached $actual_count agents, want $AGENT_COUNT ($states)"
    [[ "$wrong_count" == 0 ]] ||
        fail "deployment $deployment_id step $step_index states: $states"
}

assert_log_marker() {
    local deployment_id=$1
    local marker=$2
    local logs
    logs=$(curl "${curl_options[@]}" -H "Cookie: session=$SESSION" \
        "$BASE/deployments/$deployment_id/logs.txt")
    grep -Fq "$marker" <<<"$logs" ||
        fail "deployment $deployment_id logs do not contain $marker"
}

command -v curl >/dev/null || fail 'curl is required'
command -v sqlite3 >/dev/null || fail 'sqlite3 is required'
[[ "$TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ]] ||
    fail 'DURPDEPLOY_AGENT_TEST_TIMEOUT must be a positive integer'
[[ -f "$DB" ]] || fail "SQLite database not found: $DB"
curl "${curl_options[@]}" -f "$BASE/healthz" >/dev/null ||
    fail "DurpDeploy is unavailable at $BASE"

quoted_environment=$(sql_quote "$ENVIRONMENT")
quoted_label=$(sql_quote "$LABEL")
ENVIRONMENT_ID=$(db_query \
    "SELECT id FROM environments WHERE lower(name)=lower($quoted_environment);")
[[ "$ENVIRONMENT_ID" =~ ^[0-9]+$ ]] ||
    fail "environment $ENVIRONMENT was not found"

AGENT_COUNT=$(db_query \
    "SELECT count(DISTINCT a.id)
     FROM agents a
     JOIN agent_pairings p ON p.agent_id=a.id AND p.state='paired'
     JOIN agent_labels l ON l.agent_id=a.id
     JOIN agent_environment_labels e ON e.agent_id=a.id
     WHERE a.status='active' AND a.revoked_at IS NULL
       AND a.last_heartbeat_at >= unixepoch() - 90
       AND lower(l.label)=lower($quoted_label)
       AND e.environment_id=$ENVIRONMENT_ID;")
((AGENT_COUNT > 0)) ||
    fail "no active, paired, recently heartbeating agent matches label $LABEL and environment $ENVIRONMENT"
printf 'Agent preflight: %s matching agent(s) for label=%s environment=%s\n' \
    "$AGENT_COUNT" "$LABEL" "$ENVIRONMENT"

login_headers=$(curl "${curl_options[@]}" -D - -o /dev/null -X POST \
    --data-urlencode "email=$ADMIN_EMAIL" \
    --data-urlencode "password=$ADMIN_PASSWORD" "$BASE/login")
login_code=$(printf '%s\n' "$login_headers" | http_code)
[[ "$login_code" == 303 ]] ||
    fail "login as $ADMIN_EMAIL returned HTTP $login_code"
SESSION=$(printf '%s\n' "$login_headers" |
    sed -n 's/^set-cookie: session=\([^;]*\).*/\1/ip' | tail -1)
[[ -n "$SESSION" ]] || fail 'login response did not set a session cookie'

projects_page=$(curl "${curl_options[@]}" \
    -H "Cookie: session=$SESSION" "$BASE/projects")
CSRF=$(printf '%s\n' "$projects_page" |
    sed -n 's/.*<meta name="csrf-token" content="\([^"]*\)".*/\1/p' |
    head -1)
[[ -n "$CSRF" ]] || fail 'authenticated page did not contain a CSRF token'

run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
project_name="Agent smoke $run_id"
marker="agent-smoke-success-$run_id"
failure_marker="agent-smoke-failure-$run_id"

code=$(curl "${curl_options[@]}" -H "Cookie: session=$SESSION" \
    -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode "name=$project_name" \
    --data-urlencode 'description=Live agent smoke test' \
    --data-urlencode "csrf_token=$CSRF" "$BASE/projects")
[[ "$code" == 303 ]] || fail "create project returned HTTP $code"
PROJECT_ID=$(db_query \
    "SELECT id FROM projects WHERE name=$(sql_quote "$project_name");")
[[ "$PROJECT_ID" =~ ^[0-9]+$ ]] || fail 'created project was not found'
printf 'Test project: %s (ID %s)\n' "$project_name" "$PROJECT_ID"

code=$(curl "${curl_options[@]}" -H "Cookie: session=$SESSION" \
    -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode 'name=remote success' \
    --data-urlencode "script_body=printf '%s\\n' '$marker'" \
    --data-urlencode 'execution_target=agent' \
    --data-urlencode "agent_label=$LABEL" \
    --data-urlencode 'timeout_seconds=30' \
    --data-urlencode 'max_retries=0' \
    --data-urlencode "csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps")
[[ "$code" == 200 ]] || fail "create successful agent step returned HTTP $code"

success_release=$(create_release 'agent-smoke-success')
success_deployment=$(deploy_release "$success_release")
success_status=$(wait_for_deployment "$success_deployment")
[[ "$success_status" == succeeded ]] ||
    fail "deployment $success_deployment ended as $success_status, want succeeded"
assert_remote_runs "$success_deployment" 0 succeeded
assert_log_marker "$success_deployment" "$marker"
printf 'PASS: remote success deployment %s completed on all %s agent(s) and returned logs\n' \
    "$success_deployment" "$AGENT_COUNT"

code=$(curl "${curl_options[@]}" -H "Cookie: session=$SESSION" \
    -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode 'name=remote failure' \
    --data-urlencode "script_body=printf '%s\\n' '$failure_marker'; exit 23" \
    --data-urlencode 'execution_target=agent' \
    --data-urlencode "agent_label=$LABEL" \
    --data-urlencode 'timeout_seconds=30' \
    --data-urlencode 'max_retries=0' \
    --data-urlencode "csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps")
[[ "$code" == 200 ]] || fail "create failing agent step returned HTTP $code"

failure_release=$(create_release 'agent-smoke-failure')
failure_deployment=$(deploy_release "$failure_release")
failure_status=$(wait_for_deployment "$failure_deployment")
[[ "$failure_status" == failed ]] ||
    fail "deployment $failure_deployment ended as $failure_status, want failed"
assert_remote_runs "$failure_deployment" 1 failed
assert_log_marker "$failure_deployment" "$failure_marker"
printf 'PASS: remote failure deployment %s propagated failure and returned logs\n' \
    "$failure_deployment"
printf 'Agent smoke test: PASS\n'
