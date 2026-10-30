#!/usr/bin/env bash
# Sourced by the running suite. Keep authoring examples for manual inspection.
step_variable_id() {
    python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'
}
echo '=== Step variable viewer ==='
VIEWER_LC_ID=$(api_post "{\"name\":\"variable-viewer-$E2E_RUN_ID\"}" \
    "$BASE/api/v1/lifecycles" | step_variable_id)
api_post "{\"environment_id\":$API_ENV_ID}" \
    "$BASE/api/v1/lifecycles/$VIEWER_LC_ID/stages" >/dev/null
api_post '{"name":"REGION","value":"lifecycle-environment"}' \
    "$BASE/api/v1/lifecycles/$VIEWER_LC_ID/variables" >/dev/null
api_post "{\"name\":\"REGION\",\"value\":\"selected-environment\",\"environment_id\":$API_ENV_ID}" \
    "$BASE/api/v1/lifecycles/$VIEWER_LC_ID/variables" >/dev/null
api_post '{"name":"SECRET","value":"viewer-retained-secret","secret":true}' \
    "$BASE/api/v1/lifecycles/$VIEWER_LC_ID/variables" >/dev/null
VIEWER_PID=$(api_post "{\"name\":\"variable-viewer-$E2E_RUN_ID\",\"lifecycle_id\":$VIEWER_LC_ID}" \
    "$BASE/api/v1/projects" | step_variable_id)
api_post '{"name":"REGION","value":"project-default"}' \
    "$BASE/api/v1/projects/$VIEWER_PID/variables" >/dev/null
api_post "{\"name\":\"Inspect variables\",\"script_body\":\"echo authoring-reference\",\"container_image\":\"$BASH_IMAGE\",\"variable_names\":[\"REGION\"]}" \
    "$BASE/api/v1/projects/$VIEWER_PID/steps" >/dev/null
VIEWER_CATALOG=$(api_get "$BASE/api/v1/projects/$VIEWER_PID/variables/available?environment_id=$API_ENV_ID&interpreter=python3&variable_names=REGION")
printf '%s' "$VIEWER_CATALOG" | python3 -c '
import json,sys
c=json.load(sys.stdin)
region=next(v for v in c["configured"] if v["name"]=="REGION" and v["effective"])
assert region["value"]=="selected-environment" and region["source"]=="lifecycle"
secret=next(v for v in c["configured"] if v["name"]=="SECRET")
assert secret["secret"] and secret["value"] is None and secret["status"]=="excluded"
assert region["reference"]=="os.environ.get(\"REGION\")"
assert len(c["predefined"])==5
for v in c["predefined"]:
    expected = "available" if v["name"] in {"DURPDEPLOY_ENVIRONMENT_NAME", "DURPDEPLOY_ENVIRONMENT_ID"} else "conditional"
    assert v["status"]==expected and v["value"] is None and not v["secret"]
assert "viewer-retained-secret" not in json.dumps(c)
'
VIEWER_PAGE=$(curl_body "$BASE/projects/$VIEWER_PID/steps-page")
[[ "$VIEWER_PAGE" == *data-show-step-variables* && "$VIEWER_PAGE" == *data-variable-dialog* ]] || {
    echo 'FAIL: step variable viewer is missing'; exit 1;
}
printf '  Retained variable viewer: %s/projects/%s/steps-page\n' "$BASE" "$VIEWER_PID"
printf '  Retained variable lifecycle: %s/lifecycles/%s\n' "$BASE" "$VIEWER_LC_ID"

echo '=== Deployment environment identity ==='
IDENTITY_PID=$(api_post "{\"name\":\"environment-identity-$E2E_RUN_ID\"}" \
    "$BASE/api/v1/projects" | step_variable_id)
api_post '{"name":"SELECTED","value":"kept"}' "$BASE/api/v1/projects/$IDENTITY_PID/variables" >/dev/null
IDENTITY_STEP=$(python3 -c 'import json,sys
print(json.dumps({"name":"Print accepted identity", "container_image":sys.argv[1], "variable_names":["SELECTED"], "max_retries":1,
"script_body":"set -eu\ntest \"$SELECTED\" = kept\nprintf '\''identity=%s|%s\\n'\'' \"$DURPDEPLOY_ENVIRONMENT_NAME\" \"$DURPDEPLOY_ENVIRONMENT_ID\"\nif [ ! -e \"$DURPDEPLOY_STAGE_DIR/once\" ]; then touch \"$DURPDEPLOY_STAGE_DIR/once\"; exit 1; fi"}))' "$BASH_IMAGE")
api_post "$IDENTITY_STEP" "$BASE/api/v1/projects/$IDENTITY_PID/steps" >/dev/null
IDENTITY_RELEASE=$(api_post '{"version":"identity-v1"}' "$BASE/api/v1/projects/$IDENTITY_PID/releases" | step_variable_id)
IDENTITY_ENV_IDS=()
for IDENTITY_NAME in "identity-dev-$E2E_RUN_ID" "identity-prd-$E2E_RUN_ID"; do
    IDENTITY_ENV=$(api_post "{\"name\":\"$IDENTITY_NAME\"}" "$BASE/api/v1/environments" | step_variable_id)
    IDENTITY_ENV_IDS+=("$IDENTITY_ENV")
    IDENTITY_DEP=$(api_post "{\"release_id\":$IDENTITY_RELEASE,\"environment_id\":$IDENTITY_ENV}" \
        "$BASE/api/v1/projects/$IDENTITY_PID/deployments" | step_variable_id)
    wait_running_deployment "$IDENTITY_DEP" succeeded
    api_get "$BASE/api/v1/deployments/$IDENTITY_DEP/logs" | python3 -c 'import json,sys
logs="".join(v["line"] for v in json.load(sys.stdin))
assert logs.count("identity="+sys.argv[1]+"|"+sys.argv[2])==2, logs' "$IDENTITY_NAME" "$IDENTITY_ENV"
    printf '  Retained environment identity deployment: %s/deployments/%s\n' "$BASE" "$IDENTITY_DEP"
done
printf '  Retained environment identity project: %s/projects/%s/steps-page\n' "$BASE" "$IDENTITY_PID"
