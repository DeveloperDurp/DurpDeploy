#!/usr/bin/env bash
# Sourced by e2e_db_test.sh. All writes target this run's own fixtures.

source "$SCRIPT_DIR/request_body_e2e.sh"
source "$SCRIPT_DIR/deployment_list_e2e.sh"

echo "=== Dashboard and main-page contracts ==="
ACTIVITY=$(api_get "$BASE/api/v1/deployments/activity")
echo "$ACTIVITY" | python3 -c '
import datetime,json,sys
days=json.load(sys.stdin)
today=datetime.datetime.now(datetime.timezone.utc).date()
assert len(days) == 14
for i,day in enumerate(days):
    assert day["date"] == str(today-datetime.timedelta(days=13-i))
    assert all(isinstance(n,int) and n >= 0 for n in day["counts"].values())
'
WEB_ACTIVITY=$(curl_body "$BASE/dashboard/activity")
echo "$WEB_ACTIVITY" | python3 -c 'import json,sys; assert len(json.load(sys.stdin)) == 14'
for MAIN_PATH in / /projects /environments /lifecycles /deployments /templates \
    /settings/tokens /settings/security /admin/users /admin/agents /admin/audit \
    "/projects/$API_PROJECT_ID" "/projects/$API_PROJECT_ID/steps-page" \
    "/projects/$API_PROJECT_ID/variables" "/projects/$API_PROJECT_ID/releases" \
    "/projects/$API_PROJECT_ID/runbooks" "/projects/$API_PROJECT_ID/notifications" \
    "/projects/$API_PROJECT_ID/package-repository" "/projects/$API_PROJECT_ID/schedules"; do
    CODE=$(curl_silent "$BASE$MAIN_PATH")
    [[ "$CODE" == 200 ]] || { echo "FAIL: main page $MAIN_PATH got $CODE"; exit 1; }
    MAIN_FRAGMENT=$(curl_body -H 'HX-Request: true' -H 'HX-Boosted: true' "$BASE$MAIN_PATH")
    [[ "$MAIN_FRAGMENT" == *'id="page-content"'* ]] || { echo "FAIL: boosted page $MAIN_PATH has no content target"; exit 1; }
done
echo "  Main pages, boosted navigation, and dashboard API: OK"

echo "=== Runbook versions, retry, and schedules ==="
RUNBOOK_UPDATED=$(api_put "{\"steps\":[{\"name\":\"inspect\",\"script_body\":\"printf runbook-e2e-v2\",\"interpreter\":\"bash\",\"container_image\":\"$BASH_IMAGE\"}]}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID")
RUNBOOK_V2=$(echo "$RUNBOOK_UPDATED" | python3 -c 'import sys,json; print(json.load(sys.stdin)["version"]["id"])')
[[ "$RUNBOOK_V2" != "$RUNBOOK_VERSION" ]] || { echo "FAIL: runbook version did not advance"; exit 1; }
api_get "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID/versions/$RUNBOOK_VERSION" \
    | python3 -c 'import sys,json; assert json.load(sys.stdin)["steps"][0]["script_body"] == "printf runbook-e2e-ok"'
RUNBOOK_RETRY=$(api_post '{}' "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RUNBOOK_EXECUTION_ID/retry")
RUNBOOK_RETRY_ID=$(echo "$RUNBOOK_RETRY" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
for i in {1..200}; do
    RUNBOOK_STATUS=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RUNBOOK_RETRY_ID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])')
    [[ "$RUNBOOK_STATUS" =~ ^(succeeded|failed|cancelled|cleanup_unconfirmed)$ ]] && break
    sleep 0.1
done
[[ "$RUNBOOK_STATUS" == succeeded ]] || { echo "FAIL: runbook retry=$RUNBOOK_STATUS"; exit 1; }
RUNBOOK_LOGS=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RUNBOOK_RETRY_ID/logs")
[[ "$RUNBOOK_LOGS" == *runbook-e2e-ok* && "$RUNBOOK_LOGS" != *runbook-e2e-v2* ]] || { echo "FAIL: retry changed pinned runbook version"; exit 1; }
RUNBOOK_SCHEDULE=$(api_post "{\"environment_id\":$API_ENV_ID,\"version_id\":$RUNBOOK_VERSION,\"cron\":\"0 3 * * *\"}" \
    "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID/schedules")
RUNBOOK_SCHEDULE_ID=$(echo "$RUNBOOK_SCHEDULE" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
CODE=$(api_post_code '{}' "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID/schedules/$RUNBOOK_SCHEDULE_ID/disable")
[[ "$CODE" == 200 ]] || { echo "FAIL: disable runbook schedule=$CODE"; exit 1; }
echo "  Immutable versions, pinned retry, and schedule disable: OK"

echo "=== Verification and rollback ==="
VERIFY_PROJECT=$(api_post "{\"name\":\"e2e-verify-$E2E_RUN_ID\"}" "$BASE/api/v1/projects")
VERIFY_PROJECT_ID=$(echo "$VERIFY_PROJECT" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
VERIFY_ENV=$(api_post "{\"name\":\"e2e-verify-env-$E2E_RUN_ID\",\"verification_type\":\"bash\",\"verification_target\":\"printf verification-e2e-ok\",\"verification_timeout_seconds\":5}" "$BASE/api/v1/environments")
VERIFY_ENV_ID=$(echo "$VERIFY_ENV" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
VERIFY_STEP=$(api_post "{\"name\":\"verify-step\",\"script_body\":\"printf release-v1\",\"container_image\":\"$BASH_IMAGE\"}" "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/steps")
VERIFY_STEP_ID=$(echo "$VERIFY_STEP" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

wait_running_deployment() {
    local id=$1 wanted=$2 status
    for i in {1..200}; do
        status=$(api_get "$BASE/api/v1/deployments/$id/status" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])')
        [[ "$status" =~ ^(succeeded|failed|cancelled|cleanup_unconfirmed)$ ]] && break
        sleep 0.1
    done
    [[ "$status" == "$wanted" ]] || { echo "FAIL: deployment $id=$status, want $wanted"; exit 1; }
}
VERIFY_RELEASE=$(api_post '{"version":"verified-v1"}' "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/releases")
VERIFY_RELEASE_ID=$(echo "$VERIFY_RELEASE" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
VERIFY_DEP=$(api_post "{\"release_id\":$VERIFY_RELEASE_ID,\"environment_id\":$VERIFY_ENV_ID}" "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/deployments")
VERIFY_GOOD_ID=$(echo "$VERIFY_DEP" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
wait_running_deployment "$VERIFY_GOOD_ID" succeeded
api_get "$BASE/api/v1/deployments/$VERIFY_GOOD_ID/verification" | python3 -c 'import sys,json; assert json.load(sys.stdin)["status"] == "succeeded"'
VERIFY_LOGS=$(api_get "$BASE/api/v1/deployments/$VERIFY_GOOD_ID/logs")
[[ "$VERIFY_LOGS" == *verification-e2e-ok* ]] || { echo "FAIL: verification output missing"; exit 1; }
api_put "{\"name\":\"verify-step\",\"script_body\":\"exit 23\",\"container_image\":\"$BASH_IMAGE\"}" "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/steps/$VERIFY_STEP_ID" >/dev/null
VERIFY_RELEASE=$(api_post '{"version":"failed-v2"}' "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/releases")
VERIFY_RELEASE_ID=$(echo "$VERIFY_RELEASE" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
VERIFY_DEP=$(api_post "{\"release_id\":$VERIFY_RELEASE_ID,\"environment_id\":$VERIFY_ENV_ID}" "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/deployments")
VERIFY_FAILED_ID=$(echo "$VERIFY_DEP" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
wait_running_deployment "$VERIFY_FAILED_ID" failed
api_get "$BASE/api/v1/deployments/$VERIFY_FAILED_ID/rollback" | python3 -c 'import sys,json; assert json.load(sys.stdin)["target_deployment_id"] == int(sys.argv[1])' "$VERIFY_GOOD_ID"
ROLLBACK=$(api_post "{\"target_deployment_id\":$VERIFY_GOOD_ID}" "$BASE/api/v1/deployments/$VERIFY_FAILED_ID/rollback")
ROLLBACK_ID=$(echo "$ROLLBACK" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
wait_running_deployment "$ROLLBACK_ID" succeeded
ROLLBACK_LOGS=$(api_get "$BASE/api/v1/deployments/$ROLLBACK_ID/logs")
[[ "$ROLLBACK_LOGS" == *release-v1* && "$ROLLBACK_LOGS" == *verification-e2e-ok* ]] || { echo "FAIL: rollback lost snapshot or verification"; exit 1; }
echo "  Verification gates success; rollback uses the old snapshot: OK"

echo "=== Project notifications and secret masking ==="
api_put '{"slack_webhook_url":"","notify_emails":"","gotify_url":"","gotify_token":"","discord_webhook_url":""}' \
    "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/notifications" \
    | python3 -c 'import sys,json; assert json.load(sys.stdin)["notify_emails"] == ""'
SECRET_CREATED=$(api_post '{"name":"E2E_MASKED","value":"e2e-masked-value","secret":true}' \
    "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/variables")
SECRET_ID=$(echo "$SECRET_CREATED" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["value"] != "e2e-masked-value"; print(d["id"])')
api_get "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/variables/$SECRET_ID" \
    | python3 -c 'import sys,json; assert json.load(sys.stdin)["value"] != "e2e-masked-value"'
SECRET_STEP_BODY=$(python3 -c 'import json,sys; print(json.dumps({"name":"verify-step","script_body":"printf \"%s\" \"$E2E_MASKED\"","container_image":sys.argv[1]}))' "$BASH_IMAGE")
api_put "$SECRET_STEP_BODY" \
    "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/steps/$VERIFY_STEP_ID" >/dev/null
SECRET_RELEASE=$(api_post '{"version":"masked-v3"}' "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/releases")
SECRET_RELEASE_ID=$(echo "$SECRET_RELEASE" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
SECRET_DEP=$(api_post "{\"release_id\":$SECRET_RELEASE_ID,\"environment_id\":$VERIFY_ENV_ID}" "$BASE/api/v1/projects/$VERIFY_PROJECT_ID/deployments")
SECRET_DEP_ID=$(echo "$SECRET_DEP" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
wait_running_deployment "$SECRET_DEP_ID" succeeded
SECRET_LOGS=$(api_get "$BASE/api/v1/deployments/$SECRET_DEP_ID/logs")
[[ "$SECRET_LOGS" == *'[REDACTED]'* && "$SECRET_LOGS" != *e2e-masked-value* ]] || { echo "FAIL: secret value leaked or step did not print it"; exit 1; }
echo "  Test-project notification settings and API/log secret masking: OK"

echo "=== Running-server browser checks ==="
DURPDEPLOY_LIVE_BASE="$BASE" \
DURPDEPLOY_LIVE_SESSION="$SESSION_ID" \
DURPDEPLOY_LIVE_API_TOKEN="$API_TOKEN" \
DURPDEPLOY_LIVE_RUNBOOK_EDITOR="/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID/edit" \
    go test -tags=e2e,packagebrowser -count=1 -timeout=5m \
    -run '^TestRunningServerBrowserE2E$' ./internal/handler/api
