#!/usr/bin/env bash
# Sourced by the running-server suite. Keep all role examples and history.

permission_roles_e2e() {
    local project release role user email bearer created code deployment
    local jar csrf password=role-example-pass-1234
    local json_id='import json,sys; print(json.load(sys.stdin)["id"])'
    project=$(api_post "{\"name\":\"permission-roles-$E2E_RUN_ID\"}" "$BASE/api/v1/projects" | python3 -c "$json_id")
    api_post "{\"name\":\"Approved command\",\"script_body\":\"printf permission-roles-ok\",\"container_image\":\"$BASH_IMAGE\"}" \
        "$BASE/api/v1/projects/$project/steps" >/dev/null
    release=$(api_post '{"version":"approved-role-example"}' "$BASE/api/v1/projects/$project/releases" | python3 -c "$json_id")
    for role in viewer deployer maintainer admin; do
        email="e2e-role-$role-$E2E_RUN_ID@test.local"
        user=$(api_post "{\"email\":\"$email\",\"name\":\"Role example $role\",\"role\":\"maintainer\",\"password\":\"$password\"}" \
            "$BASE/api/v1/admin/users" | python3 -c "$json_id")
        api_post "{\"user_id\":$user,\"role\":\"$role\"}" "$BASE/api/v1/projects/$project/members" >/dev/null
        jar="$TMP/permission-$role-cookies"
        code=$(curl -sS -c "$jar" -o /dev/null -w '%{http_code}' -X POST \
            --data-urlencode "email=$email" --data-urlencode "password=$password" "$BASE/login")
        [[ "$code" == 303 ]] || return 1
        csrf=$(csrf_from_cookies "$jar")
        bearer=$(mint_web_token "$jar" "role-example-$role" "$csrf")
        api_put "{\"name\":\"Role example $role\",\"role\":\"$role\"}" "$BASE/api/v1/admin/users/$user" >/dev/null
        code=$(project_token_code "$bearer" POST "/projects/$project/variables" \
            "{\"name\":\"ROLE_$role\",\"value\":\"retained example\"}")
        if [[ "$role" == viewer || "$role" == deployer ]]; then
            [[ "$code" == 403 ]] || return 1
        else
            [[ "$code" == 201 ]] || return 1
        fi
        if [[ "$role" == viewer ]]; then
            code=$(project_token_code "$bearer" POST "/projects/$project/deployments" \
                "{\"release_id\":$release,\"environment_id\":$API_ENV_ID}")
            [[ "$code" == 403 ]] || return 1
        else
            created=$(curl -fsS -H "Authorization: Bearer $bearer" -H 'Content-Type: application/json' \
                -d "{\"release_id\":$release,\"environment_id\":$API_ENV_ID}" "$BASE/api/v1/projects/$project/deployments")
            deployment=$(printf '%s' "$created" | python3 -c "$json_id")
            wait_running_deployment "$deployment" succeeded
        fi
        echo "  $role login: $email / $password"
    done
    echo "  Permission role project: $BASE/projects/$project"
    echo "  Existing release: $BASE/projects/$project/releases/$release"
    echo '  No schedules were created.'
}

echo '=== Four permission levels ==='
permission_roles_e2e
