#!/usr/bin/env bash
# Sourced by running_features_e2e.sh. Keep completed histories and repeat fixtures.

echo "=== Runbook deletion and retained history ==="
for RB_DELETE_TRANSPORT in api web; do
    RB_DELETE_CREATED=$(api_post "{\"name\":\"delete-$RB_DELETE_TRANSPORT-$E2E_RUN_ID\",\"steps\":[{\"name\":\"inspect\",\"script_body\":\"printf runbook-e2e-ok\",\"container_image\":\"$BASH_IMAGE\"}]}" \
        "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks")
    RB_DELETE_ID=$(echo "$RB_DELETE_CREATED" | python3 -c 'import json,sys; print(json.load(sys.stdin)["runbook"]["id"])')
    RB_DELETE_PATH="/projects/$API_PROJECT_ID/runbooks/$RB_DELETE_ID"
    RB_DELETE_EXECUTION=$(api_post "{\"environment_id\":$API_ENV_ID}" \
        "$BASE/api/v1$RB_DELETE_PATH/executions")
    RB_DELETE_EXECUTION_ID=$(echo "$RB_DELETE_EXECUTION" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    for i in {1..200}; do
        RB_DELETE_STATUS=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RB_DELETE_EXECUTION_ID" \
            | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
        [[ "$RB_DELETE_STATUS" =~ ^(succeeded|failed|cancelled|cleanup_unconfirmed)$ ]] && break
        sleep 0.1
    done
    [[ "$RB_DELETE_STATUS" == succeeded ]] || { echo "FAIL: deletion fixture execution=$RB_DELETE_STATUS"; exit 1; }
    RB_DELETE_HISTORY="/projects/$API_PROJECT_ID/runbooks/executions/$RB_DELETE_EXECUTION_ID"
    api_post "{\"environment_id\":$API_ENV_ID,\"cron\":\"0 3 * * *\"}" \
        "$BASE/api/v1$RB_DELETE_PATH/schedules" >/dev/null
    if [[ "$RB_DELETE_TRANSPORT" == web ]]; then
        RUNBOOK_DELETE_BROWSER_PATH=$RB_DELETE_PATH
        RUNBOOK_DELETE_BROWSER_HISTORY=$RB_DELETE_HISTORY
        continue
    fi
    CODE=$(api_get_code -X DELETE "$BASE/api/v1$RB_DELETE_PATH")
    [[ "$CODE" == 204 ]] || { echo "FAIL: API runbook deletion=$CODE"; exit 1; }
    CODE=$(api_get_code "$BASE/api/v1$RB_DELETE_PATH")
    [[ "$CODE" == 404 ]] || { echo "FAIL: deleted runbook remains active=$CODE"; exit 1; }
    CODE=$(api_post_code '{}' "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RB_DELETE_EXECUTION_ID/retry")
    [[ "$CODE" == 404 ]] || { echo "FAIL: deleted runbook retry=$CODE"; exit 1; }
    RB_DELETE_LOGS=$(api_get "$BASE/api/v1/projects/$API_PROJECT_ID/runbook-executions/$RB_DELETE_EXECUTION_ID/logs")
    [[ "$RB_DELETE_LOGS" == *runbook-e2e-ok* ]] || { echo "FAIL: deletion lost logs"; exit 1; }
    echo "  API deletion history: $BASE$RB_DELETE_HISTORY"
done
echo "  Repeat definition: $BASE/projects/$API_PROJECT_ID/runbooks/$RUNBOOK_ID?version_id=$RUNBOOK_VERSION"
