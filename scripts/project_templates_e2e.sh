#!/usr/bin/env bash
# Sourced by the running-server suite. Retain all template examples.

project_templates_e2e() {
    local source template template_id preview input created destination release
    local viewer token code
    local json_id='import json,sys; print(json.load(sys.stdin)["id"])'
    echo '=== Reusable project templates ==='
    source=$(api_post "{\"name\":\"e2e-template-source-$E2E_RUN_ID\"}" "$BASE/api/v1/projects" |
        python3 -c "$json_id")
    api_post "{\"name\":\"Template workflow\",\"script_body\":\"echo original-template\",\"container_image\":\"$BASH_IMAGE\",\"timeout_seconds\":73,\"max_retries\":2,\"variable_names\":[\"TEMPLATE_DEFAULT\"]}" \
        "$BASE/api/v1/projects/$source/steps" >/dev/null
    api_post '{"name":"TEMPLATE_DEFAULT","value":"retained non-secret default"}' \
        "$BASE/api/v1/projects/$source/variables" >/dev/null
    api_post '{"name":"TEMPLATE_SECRET","value":"source-template-secret","secret":true}' \
        "$BASE/api/v1/projects/$source/variables" >/dev/null
    template=$(api_post "{\"name\":\"Reusable project $E2E_RUN_ID\",\"description\":\"Retained project-template example\"}" \
        "$BASE/api/v1/projects/$source/project-templates")
    template_id=$(printf '%s' "$template" | python3 -c "$json_id")
    preview=$(api_get "$BASE/api/v1/projects/$source/project-templates/$template_id")
    input=$(printf '%s' "$preview" | python3 -c '
import json,sys
template=json.load(sys.stdin)
snapshot=template["snapshot"]
assert "source-template-secret" not in json.dumps(template)
assert any(v.get("default")=="retained non-secret default" for v in snapshot["variables"])
print(json.dumps({"name":"e2e-template-copy-"+sys.argv[1],"reviewed":True,
    "variable_values":{v["key"]:"fresh-template-secret" for v in snapshot["variables"] if v["secret"]}}))
' "$E2E_RUN_ID")
    created=$(api_post "$input" "$BASE/api/v1/projects/$source/project-templates/$template_id/projects")
    destination=$(printf '%s' "$created" | python3 -c "$json_id")
    api_get "$BASE/api/v1/projects/$destination/steps" | python3 -c '
import json,sys
steps=json.load(sys.stdin)["items"]
assert len(steps)==1 and steps[0]["script_body"]=="echo original-template"
assert steps[0]["timeout_seconds"]==73 and steps[0]["max_retries"]==2
assert steps[0]["variable_names"]==["TEMPLATE_DEFAULT"]
'
    release=$(api_post '{"version":"template-frozen-v1"}' "$BASE/api/v1/projects/$destination/releases" |
        python3 -c "$json_id")
    code=$(api_post_code '{"name":"incomplete-template-copy","reviewed":true}' \
        "$BASE/api/v1/projects/$source/project-templates/$template_id/projects")
    [[ "$code" == 422 ]] || { echo "FAIL: missing secret returned $code"; return 1; }
    viewer=$(api_post "{\"name\":\"template-viewer-$E2E_RUN_ID\",\"scope\":\"global\",\"role\":\"viewer\"}" "$BASE/api/v1/tokens")
    token=$(printf '%s' "$viewer" | python3 -c "$json_id")
    code=$(curl -sS -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
        -X POST -d "$input" -o /dev/null -w '%{http_code}' \
        "$BASE/api/v1/projects/$source/project-templates/$template_id/projects")
    [[ "$code" == 403 ]] || { echo "FAIL: viewer applied template: $code"; return 1; }
    printf '  Source and Save as Template: %s/projects/%s\n' "$BASE" "$source"
    printf '  Template preview: %s/projects/%s/project-templates/%s\n' "$BASE" "$source" "$template_id"
    printf '  Independent project: %s/projects/%s\n' "$BASE" "$destination"
    printf '  Frozen release: %s/projects/%s/releases/%s\n' "$BASE" "$destination" "$release"
}

project_templates_e2e
