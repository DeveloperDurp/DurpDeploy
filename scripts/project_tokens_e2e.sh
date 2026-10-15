#!/usr/bin/env bash
# Sourced by running_features_e2e.sh. Retain projects and token metadata.

project_token_code() {
    local token=$1 method=$2 path=$3 payload=${4:-}
    curl -sS -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
        -X "$method" -d "$payload" -o /dev/null -w '%{http_code}' "$BASE/api/v1$path"
}

project_tokens_e2e() {
    local first second foreign role created scoped token_id expected code
    local revoke_id revoke_token
    echo '=== Project-scoped API tokens ==='
    first=$(api_post "{\"name\":\"e2e-token-a-$E2E_RUN_ID\"}" "$BASE/api/v1/projects" |
        python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    second=$(api_post "{\"name\":\"e2e-token-b-$E2E_RUN_ID\"}" "$BASE/api/v1/projects" |
        python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    foreign=$(api_post "{\"name\":\"e2e-token-foreign-$E2E_RUN_ID\"}" "$BASE/api/v1/projects" |
        python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    for role in viewer deployer admin; do
        created=$(api_post "{\"name\":\"e2e-project-$role-$E2E_RUN_ID\",\"scope\":\"scoped\",\"role\":\"$role\",\"project_ids\":[$first,$second]}" "$BASE/api/v1/tokens")
        scoped=$(printf '%s' "$created" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
        token_id=$(printf '%s' "$created" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token_id"])')
        curl -fsS -H "Authorization: Bearer $scoped" "$BASE/api/v1/projects?limit=1" |
            python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["total"] == 2 and len(d["items"]) == 1'
        for path in "/projects/$foreign" /admin/stats /tokens /templates; do
            code=$(project_token_code "$scoped" GET "$path")
            [[ "$code" == 403 ]] || { echo "FAIL: $role GET $path returned $code"; return 1; }
        done
        code=$(project_token_code "$scoped" POST /tokens '{"name":"escape"}')
        [[ "$code" == 403 ]] || { echo "FAIL: scoped token can mint another token"; return 1; }
        expected=201
        [[ "$role" != viewer ]] || expected=403
        code=$(project_token_code "$scoped" POST "/projects/$first/variables" \
            "{\"name\":\"TOKEN_$role\",\"value\":\"retained scope example\"}")
        [[ "$code" == "$expected" ]] || { echo "FAIL: $role write returned $code"; return 1; }
        if [[ "$role" == viewer ]]; then
            code=$(project_token_code "$API_TOKEN" PATCH "/tokens/$token_id" \
                "{\"scope\":\"scoped\",\"role\":\"viewer\",\"project_ids\":[$second]}")
            [[ "$code" == 200 ]] || { echo "FAIL: scope update returned $code"; return 1; }
            code=$(project_token_code "$scoped" GET "/projects/$first")
            [[ "$code" == 403 ]] || { echo "FAIL: removed token project remains accessible"; return 1; }
            code=$(project_token_code "$API_TOKEN" PATCH "/tokens/$token_id" \
                "{\"scope\":\"scoped\",\"role\":\"viewer\",\"project_ids\":[$first,$second]}")
            [[ "$code" == 200 ]] || return 1
        fi
    done
    code=$(project_token_code "$API_TOKEN" POST /tokens \
        '{"name":"empty-scoped","scope":"scoped","role":"viewer","project_ids":[]}')
    [[ "$code" == 422 ]] || { echo "FAIL: empty scoped selection returned $code"; return 1; }
    created=$(api_post "{\"name\":\"e2e-revoked-project-$E2E_RUN_ID\",\"scope\":\"scoped\",\"role\":\"viewer\",\"project_ids\":[$first]}" "$BASE/api/v1/tokens")
    revoke_id=$(printf '%s' "$created" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token_id"])')
    revoke_token=$(printf '%s' "$created" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    code=$(project_token_code "$API_TOKEN" DELETE "/tokens/$revoke_id")
    [[ "$code" == 204 ]] || return 1
    code=$(project_token_code "$revoke_token" GET "/projects/$first")
    [[ "$code" == 401 ]] || { echo 'FAIL: revoked scoped token remains valid'; return 1; }
    PROJECT_TOKEN_BROWSER_IDS="$first,$second"
    echo "  Selected projects: $BASE/projects/$first and $BASE/projects/$second"
    echo "  Denied project: $BASE/projects/$foreign"
    echo "  Retained role and scope metadata: $BASE/settings/tokens"
}

project_tokens_e2e
