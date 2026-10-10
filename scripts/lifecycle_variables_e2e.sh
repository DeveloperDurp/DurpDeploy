#!/usr/bin/env bash
# Sourced by the running-server suite; retain every shared-variable example.
echo "=== Shared lifecycle variables ==="
SHARED_LC_ID=$(api_post "{\"name\":\"shared-lifecycle-$E2E_RUN_ID\"}" "$BASE/api/v1/lifecycles" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
api_post "{\"environment_id\":$TEST_ENV_ID}" "$BASE/api/v1/lifecycles/$SHARED_LC_ID/stages" >/dev/null
SHARED_VARIABLE_URL="$BASE/api/v1/lifecycles/$SHARED_LC_ID/variables"
SHARED_REGION_ID=$(api_post '{"name":"REGION","value":"shared-first"}' "$SHARED_VARIABLE_URL" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
api_post '{"name":"TOKEN","value":"shared-secret-sentinel","secret":true}' "$SHARED_VARIABLE_URL" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["value"] == "" and d["secret"] == 1'
SHARED_PROJECT_IDS=()
for SHARED_INDEX in 1 2; do
    SHARED_PID=$(api_post "{\"name\":\"shared-project-$E2E_RUN_ID-$SHARED_INDEX\",\"lifecycle_id\":$SHARED_LC_ID}" "$BASE/api/v1/projects" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    SHARED_PROJECT_IDS+=("$SHARED_PID")
    SHARED_SCRIPT=$(python3 -c 'import json,sys; print(json.dumps({"name":"Check inheritance", "script_body":"test \"$REGION\" = shared-first; test \"$TOKEN\" = shared-secret-sentinel; printf \"%s %s\\n\" \"$REGION\" \"$TOKEN\"", "container_image":sys.argv[1]}))' "$BASH_IMAGE")
    api_post "$SHARED_SCRIPT" "$BASE/api/v1/projects/$SHARED_PID/steps" >/dev/null
    SHARED_RELEASE_ID=$(api_post '{"version":"shared-v1"}' "$BASE/api/v1/projects/$SHARED_PID/releases" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    api_put '{"name":"REGION","value":"shared-updated"}' "$SHARED_VARIABLE_URL/$SHARED_REGION_ID" >/dev/null
    SHARED_DEP_ID=$(api_post "{\"release_id\":$SHARED_RELEASE_ID,\"environment_id\":$TEST_ENV_ID}" "$BASE/api/v1/projects/$SHARED_PID/deployments" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    wait_running_deployment "$SHARED_DEP_ID" succeeded
    SHARED_LOGS=$(api_get "$BASE/api/v1/deployments/$SHARED_DEP_ID/logs")
    [[ "$SHARED_LOGS" == *shared-first* && "$SHARED_LOGS" != *shared-secret-sentinel* ]] || { echo 'FAIL: shared snapshot or redaction'; exit 1; }
    api_get "$BASE/api/v1/projects/$SHARED_PID/variables/inherited" | python3 -c 'import json,sys; v=json.load(sys.stdin); assert len(v)==2; assert next(x for x in v if x["name"]=="TOKEN")["value"]==""'
    SHARED_PAGE=$(curl_body "$BASE/projects/$SHARED_PID/variables")
    [[ "$SHARED_PAGE" == *'Inherited variables'* && "$SHARED_PAGE" != *shared-secret-sentinel* ]] || { echo 'FAIL: inherited web values'; exit 1; }
    api_put '{"name":"REGION","value":"shared-first"}' "$SHARED_VARIABLE_URL/$SHARED_REGION_ID" >/dev/null
    echo "  Shared project: $BASE/projects/$SHARED_PID/variables"
    echo "  Immutable shared release: $BASE/projects/$SHARED_PID/releases/$SHARED_RELEASE_ID"
    echo "  Inherited deployment: $BASE/deployments/$SHARED_DEP_ID"
done
SHARED_OVERRIDE_ID=$(api_post '{"name":"REGION","value":"project-only"}' "$BASE/api/v1/projects/${SHARED_PROJECT_IDS[0]}/variables" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
api_get "$BASE/api/v1/projects/${SHARED_PROJECT_IDS[0]}/variables/inherited" | python3 -c 'import json,sys; v=next(x for x in json.load(sys.stdin) if x["name"]=="REGION"); assert v["override"]["value"]=="project-only"'
api_get "$BASE/api/v1/projects/${SHARED_PROJECT_IDS[1]}/variables/inherited" | python3 -c 'import json,sys; v=next(x for x in json.load(sys.stdin) if x["name"]=="REGION"); assert v["override"] is None'
echo "  Lifecycle editor: $BASE/lifecycles/$SHARED_LC_ID"
echo '  Shared inheritance, project isolation, immutable snapshots and redaction: OK'
