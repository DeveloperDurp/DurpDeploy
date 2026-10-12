#!/usr/bin/env bash
# Sourced by e2e_test.sh; all fixtures use the public API.
deployment_list_e2e() {
    local owned foreign env foreign_env release foreign_release user jar token csrf
    local prefix="list-access-$(date +%s)-$$" password="list-access-password-1234"
    local owned_dep foreign_dep role query body code
    local project_prefix=$prefix
    [[ -z "${E2E_RUN_ID:-}" ]] || project_prefix=list-access
    echo "=== Deployment list authorization ==="
    owned=$(api_post "{\"name\":\"$project_prefix-owned\"}" "$BASE/api/v1/projects" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    foreign=$(api_post "{\"name\":\"$project_prefix-foreign\"}" "$BASE/api/v1/projects" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    env=${ENV_ID:-}
    foreign_env=${LC_TEST_ID:-}
    if [[ -z "$env" ]]; then
        env=$(api_post "{\"name\":\"$prefix-env\"}" "$BASE/api/v1/environments" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    fi
    if [[ -z "$foreign_env" ]]; then
        foreign_env=$(api_post "{\"name\":\"$prefix-foreign-env\"}" "$BASE/api/v1/environments" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    fi
    release=$(api_post '{"version":"1.0"}' "$BASE/api/v1/projects/$owned/releases" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    foreign_release=$(api_post '{"version":"1.0"}' "$BASE/api/v1/projects/$foreign/releases" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    owned_dep=$(api_post "{\"release_id\":$release,\"environment_id\":$env}" "$BASE/api/v1/projects/$owned/deployments" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    foreign_dep=$(api_post "{\"release_id\":$foreign_release,\"environment_id\":$foreign_env}" "$BASE/api/v1/projects/$foreign/deployments" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    user=$(api_post "{\"email\":\"$prefix@test.local\",\"name\":\"List User\",\"password\":\"$password\",\"role\":\"deployer\"}" "$BASE/api/v1/admin/users" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    jar="$TMP/list-access-cookies"
    code=$(curl -sS -c "$jar" -o /dev/null -w '%{http_code}' -X POST \
        --data-urlencode "email=$prefix@test.local" --data-urlencode "password=$password" "$BASE/login")
    [[ "$code" == 303 ]] || { echo "FAIL: list user login got $code"; return 1; }
    csrf=$(csrf_from_cookies "$jar")
    token=$(mint_web_token "$jar" list-access "$csrf")

    # Given no memberships, both surfaces are empty.
    curl -fsS -H "Authorization: Bearer $token" "$BASE/api/v1/deployments" | python3 -c '
import json,sys
result=json.load(sys.stdin)
assert result["total"] == 0 and result["items"] == []
'
    body=$(curl -fsS -b "$jar" "$BASE/deployments")
    [[ "$body" != *"$project_prefix-owned"* && "$body" != *"$project_prefix-foreign"* ]] || {
        echo "FAIL: nonmember web list exposes projects"; return 1;
    }
    api_post "{\"user_id\":$user,\"role\":\"deployer\"}" "$BASE/api/v1/projects/$owned/members" >/dev/null

    for role in deployer viewer; do
        if [[ "$role" == viewer ]]; then
            api_put '{"name":"List User","role":"viewer"}' "$BASE/api/v1/admin/users/$user" >/dev/null
        fi
        # When listing with pagination, totals and IDs contain only owned rows.
        curl -fsS -H "Authorization: Bearer $token" "$BASE/api/v1/deployments?limit=1" | python3 -c '
import json,sys
result=json.load(sys.stdin)
assert result["total"] == 1 and [item["id"] for item in result["items"]] == [int(sys.argv[1])]
' "$owned_dep"
        for query in "project_id=$foreign" "project_id=999999999" "env_id=$foreign_env" "limit=1&offset=1"; do
            curl -fsS -H "Authorization: Bearer $token" "$BASE/api/v1/deployments?$query" | python3 -c '
import json,sys
result=json.load(sys.stdin)
assert result["items"] == []
assert result["total"] == (1 if sys.argv[1].startswith("limit=") else 0)
' "$query"
        done
        # Then the full web page and HTMX rows expose no foreign metadata/counts.
        body=$(curl -fsS -b "$jar" "$BASE/deployments?limit=1")
        [[ "$body" == *"$project_prefix-owned"* && "$body" == *"value=\"$env\""* &&
           "$body" != *"$project_prefix-foreign"* && "$body" != *'Load more'* ]] || {
            echo "FAIL: $role web list/dropdowns leak or omit metadata"; return 1;
        }
        body=$(curl -fsS -b "$jar" -H 'HX-Request: true' "$BASE/deployments?limit=1&offset=1")
        [[ "$body" != *'href="/deployments/'* && "$body" != *'Load more'* ]] || {
            echo "FAIL: $role HTMX pagination includes foreign rows"; return 1;
        }
        body=$(curl -fsS -b "$jar" "$BASE/deployments?project_id=$foreign")
        [[ "$body" != *'href="/deployments/'* && "$body" != *"$project_prefix-foreign"* ]] || {
            echo "FAIL: $role foreign project filter reveals deployments"; return 1;
        }
    done
    # Global admins retain both rows, regardless of membership.
    api_get "$BASE/api/v1/deployments?project_id=$foreign" | python3 -c '
import json,sys
result=json.load(sys.stdin)
assert result["total"] == 1 and result["items"][0]["id"] == int(sys.argv[1])
' "$foreign_dep"
    body=$(curl_body "$BASE/deployments")
    [[ "$body" == *"$project_prefix-owned"* && "$body" == *"$project_prefix-foreign"* ]] || {
        echo "FAIL: admin dropdowns lost global view"; return 1;
    }
    echo "  Nonmember, deployer, viewer, filters, totals, HTMX, and admin: OK"
}
deployment_list_e2e
