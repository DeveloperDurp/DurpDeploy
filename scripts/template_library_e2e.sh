#!/usr/bin/env bash
# Sourced by the running-server suite. Retain copies for manual inspection.

template_library_e2e() {
    local fixture_agent_id="e2e-library-$E2E_RUN_ID"
    local agent_label="e2e-library-$E2E_RUN_ID"
    local project_id template_preview apply_request slug apply_result http_status
    local project_api_url template_api_url

    echo '=== Curated step and workflow library ==='
    # An offline, unpaired fixture supplies a label for configuration-only tests.
    # It has no identity and cannot execute work. Real deployment needs a paired host.
    db_query "INSERT INTO agents (id,name,endpoint) VALUES ('$fixture_agent_id','Library configuration fixture','https://library-fixture.invalid');" >/dev/null
    db_query "INSERT INTO agent_labels (agent_id,label) VALUES ('$fixture_agent_id','$agent_label');" >/dev/null

    api_get "$BASE/api/v1/template-library" | python3 -c '
import json
import sys

library = json.load(sys.stdin)
assert library["total"] == 19
assert sum(entry["kind"] == "step" for entry in library["items"]) == 12
assert sum(entry["kind"] == "workflow" for entry in library["items"]) == 7
'
    local slugs
    slugs=$(api_get "$BASE/api/v1/template-library" | python3 -c \
        'import json,sys; print(" ".join(entry["slug"] for entry in json.load(sys.stdin)["items"]))')
    for slug in $slugs; do
        project_id=$(api_post "{\"name\":\"e2e-library-$slug-$E2E_RUN_ID\"}" "$BASE/api/v1/projects" |
            python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
        project_api_url="$BASE/api/v1/projects/$project_id"
        template_api_url="$project_api_url/template-library/$slug"
        template_preview=$(api_get "$template_api_url")
        apply_request=$(printf '%s' "$template_preview" | python3 -c '
import json
import sys

template = json.load(sys.stdin)
values = {
    "ARTIFACT_URL": "https://artifacts.example.test/app",
    "ARTIFACT_TOKEN": "retained-fixture-secret",
    "FILE_SOURCE": "",
    "INSTALL_PATH": "/opt/example/app",
    "SERVICE_NAME": "example.service",
    "HEALTH_URL": "http://127.0.0.1:8080/health",
    "COMPOSE_FILE": "/opt/example/compose.yml",
    "COMPOSE_PROJECT": "example",
    "APP_IMAGE": "example/app:1.0",
    "TF_CONFIG_DIR": "/opt/example/terraform",
    "TF_CONTAINER_IMAGE": "example/terraform-toolchain:reviewed",
    "TF_HTTP_USERNAME": "fixture",
    "TF_HTTP_PASSWORD": "retained-fixture-secret",
}
assert template["dependencies"] and template["permissions"]
print(json.dumps({
    "version": template["version"],
    "reviewed": True,
    "agent_selectors": [sys.argv[1]] if any(
        step["execution_target"] == "agent" for step in template["snapshot"]["steps"]
    ) else [],
    "variable_values": {
        name: values[name] for name in template["configuration"]
    },
}))
' "$agent_label")
        apply_result=$(api_post "$apply_request" "$template_api_url/apply")
        printf '%s' "$apply_result" | python3 -c '
import json
import sys

result = json.load(sys.stdin)
assert result["steps_created"] > 0
'
        api_get "$project_api_url/deployments" | python3 -c '
import json
import sys

deployments = json.load(sys.stdin)
assert deployments["total"] == 0
'
        http_status=$(api_post_code "$apply_request" "$template_api_url/apply")
        if [[ "$http_status" != 409 ]]; then
            echo "FAIL: library collision returned $http_status"
            return 1
        fi
        printf '  Editable %s copy: %s/projects/%s\n' "$slug" "$BASE" "$project_id"
        printf '  Review/configuration: %s/projects/%s/template-library/%s\n' "$BASE" "$project_id" "$slug"
    done
    printf '  Offline label: %s. Replace it with paired host labels before deployment.\n' "$agent_label"
}

template_library_e2e
