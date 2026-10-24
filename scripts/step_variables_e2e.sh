#!/usr/bin/env bash
# Sourced by the running suite. Keep authoring examples for manual inspection.
echo '=== Step variable viewer ==='
VIEWER_LC_ID=$(api_post "{\"name\":\"variable-viewer-$E2E_RUN_ID\"}" \
    "$BASE/api/v1/lifecycles" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
api_post "{\"environment_id\":$API_ENV_ID}" \
    "$BASE/api/v1/lifecycles/$VIEWER_LC_ID/stages" >/dev/null
api_post '{"name":"REGION","value":"lifecycle-environment"}' \
    "$BASE/api/v1/lifecycles/$VIEWER_LC_ID/variables" >/dev/null
api_post "{\"name\":\"REGION\",\"value\":\"selected-environment\",\"environment_id\":$API_ENV_ID}" \
    "$BASE/api/v1/lifecycles/$VIEWER_LC_ID/variables" >/dev/null
api_post '{"name":"SECRET","value":"viewer-retained-secret","secret":true}' \
    "$BASE/api/v1/lifecycles/$VIEWER_LC_ID/variables" >/dev/null
VIEWER_PID=$(api_post "{\"name\":\"variable-viewer-$E2E_RUN_ID\",\"lifecycle_id\":$VIEWER_LC_ID}" \
    "$BASE/api/v1/projects" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
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
assert len(c["predefined"])==3 and all(v["status"]=="conditional" and v["value"] is None for v in c["predefined"])
assert "viewer-retained-secret" not in json.dumps(c)
'
VIEWER_PAGE=$(curl_body "$BASE/projects/$VIEWER_PID/steps-page")
[[ "$VIEWER_PAGE" == *data-show-step-variables* && "$VIEWER_PAGE" == *data-variable-dialog* ]] || {
    echo 'FAIL: step variable viewer is missing'; exit 1;
}
printf '  Retained variable viewer: %s/projects/%s/steps-page\n' "$BASE" "$VIEWER_PID"
printf '  Retained variable lifecycle: %s/lifecycles/%s\n' "$BASE" "$VIEWER_LC_ID"
