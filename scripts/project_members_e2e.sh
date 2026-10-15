#!/usr/bin/env bash
# Sourced by the running-server suite. Retain all accounts and memberships.

check_project_member_roles() {
    local project other target manager manager_email member_password
    local jar csrf token path original code role
    local json_id='import json,sys; print(json.load(sys.stdin)["id"])'
    local json_header='Content-Type: application/json'
    local deployer_role='{"role":"deployer"}'
    local http_status='%{http_code}'
    manager_email="e2e-member-admin-$E2E_RUN_ID@test.local"
    member_password=e2e-members-pass-1234
    project=$(api_post "{\"name\":\"member-roles-$E2E_RUN_ID\"}" "$BASE/api/v1/projects" | python3 -c "$json_id")
    other=$(api_post "{\"name\":\"member-roles-other-$E2E_RUN_ID\"}" "$BASE/api/v1/projects" | python3 -c "$json_id")
    target=$(api_post "{\"email\":\"e2e-member-target-$E2E_RUN_ID@test.local\",\"name\":\"Role target\",\"role\":\"deployer\",\"password\":\"$member_password\"}" \
        "$BASE/api/v1/admin/users" | python3 -c "$json_id")
    manager=$(api_post "{\"email\":\"$manager_email\",\"name\":\"Project member administrator\",\"role\":\"deployer\",\"password\":\"$member_password\"}" \
        "$BASE/api/v1/admin/users" | python3 -c "$json_id")
    api_post "{\"user_id\":$target,\"role\":\"deployer\"}" "$BASE/api/v1/projects/$project/members" >/dev/null
    api_post "{\"user_id\":$target,\"role\":\"admin\"}" "$BASE/api/v1/projects/$other/members" >/dev/null
    api_post "{\"user_id\":$manager,\"role\":\"admin\"}" "$BASE/api/v1/projects/$project/members" >/dev/null
    api_post "{\"user_id\":$VIEWER_USER_ID,\"role\":\"admin\"}" "$BASE/api/v1/projects/$project/members" >/dev/null
    path="/projects/$project/members/$target"
    original=$(api_get "$BASE/api/v1/projects/$project/members" | python3 -c 'import json,sys; print(next(m["created_at"] for m in json.load(sys.stdin)["items"] if m["user_id"]==int(sys.argv[1])))' "$target")
    api_put '{"role":"admin"}' "$BASE/api/v1$path" | python3 -c 'import json,sys; assert json.load(sys.stdin)["role"]=="admin"'

    jar="$TMP/member-manager-cookies"
    code=$(curl -sS -c "$jar" -o /dev/null -w "$http_status" -X POST \
        -d "email=$manager_email&password=$member_password" "$BASE/login")
    [[ "$code" == 303 ]] || { echo "FAIL: member administrator login=$code"; return 1; }
    csrf=$(csrf_from_cookies "$jar")
    token=$(mint_web_token "$jar" member-role-check "$csrf")
    [[ -n "$csrf" && -n "$token" ]] || { echo 'FAIL: member administrator credentials'; return 1; }
    for role in deployer admin; do
        curl -fsS -H "Authorization: Bearer $token" -H "$json_header" \
            -X PUT -d "{\"role\":\"$role\"}" "$BASE/api/v1$path" \
            | python3 -c 'import json,sys; assert json.load(sys.stdin)["role"]==sys.argv[1]' "$role"
    done
    for role in deployer admin; do
        code=$(curl -sS -b "$jar" -H "X-CSRF-Token: $csrf" -H 'HX-Request: true' \
            -H 'X-Form-Dialog: true' -X PUT -d "role=$role" \
            -D "$TMP/member-role-headers" -o "$TMP/member-role-body" -w "$http_status" "$BASE$path")
        [[ "$code" == 200 ]] || { echo "FAIL: member role web save=$code"; return 1; }
        grep -q 'Member role updated' "$TMP/member-role-headers"
        grep -q 'id="project-members"' "$TMP/member-role-body"
        api_get "$BASE/api/v1/projects/$project/members" \
            | python3 -c 'import json,sys; m=next(m for m in json.load(sys.stdin)["items"] if m["user_id"]==int(sys.argv[1])); assert m["role"]==sys.argv[2] and str(m["created_at"])==sys.argv[3]' "$target" "$role" "$original"
    done
    code=$(curl -sS -b "$jar" -H "X-CSRF-Token: $csrf" -X PUT -d 'role=viewer' \
        -o /dev/null -w "$http_status" "$BASE$path")
    [[ "$code" == 422 ]] || { echo "FAIL: invalid member role=$code"; return 1; }
    code=$(curl -sS -H "Authorization: Bearer $token" -H "$json_header" \
        -X PUT -d "$deployer_role" -o /dev/null -w "$http_status" \
        "$BASE/api/v1/projects/$other/members/$target")
    [[ "$code" == 403 ]] || { echo "FAIL: cross-project role update=$code"; return 1; }
    code=$(curl -sS -H "Authorization: Bearer $VIEWER_TOKEN" -H "$json_header" \
        -X PUT -d "$deployer_role" -o /dev/null -w "$http_status" "$BASE/api/v1$path")
    [[ "$code" == 403 ]] || { echo "FAIL: viewer role update=$code"; return 1; }
    # Self-demotion must remove member-management authority immediately.
    curl -fsS -H "Authorization: Bearer $token" -H "$json_header" \
        -X PUT -d "$deployer_role" "$BASE/api/v1/projects/$project/members/$manager" >/dev/null
    code=$(curl -sS -H "Authorization: Bearer $token" -H "$json_header" \
        -X PUT -d "$deployer_role" -o /dev/null -w "$http_status" "$BASE/api/v1$path")
    [[ "$code" == 403 ]] || { echo "FAIL: demoted administrator API update=$code"; return 1; }
    code=$(curl -sS -b "$jar" -H "X-CSRF-Token: $csrf" -X PUT -d 'role=deployer' \
        -o /dev/null -w "$http_status" "$BASE$path")
    [[ "$code" == 403 ]] || { echo "FAIL: demoted administrator web update=$code"; return 1; }
    api_get "$BASE/api/v1/projects/$other/members" \
        | python3 -c 'import json,sys; assert next(m["role"] for m in json.load(sys.stdin)["items"] if m["user_id"]==int(sys.argv[1]))=="admin"' "$target"
    api_get "$BASE/api/v1/admin/users/$target" \
        | python3 -c 'import json,sys; assert json.load(sys.stdin)["role"]=="deployer"'
    # Restore the retained account for manual checks.
    api_put '{"role":"admin"}' "$BASE/api/v1/projects/$project/members/$manager" >/dev/null
    echo "  Member role editor: $BASE/projects/$project/edit"
    echo "  Unchanged second project: $BASE/projects/$other/edit"
    echo "  Project administrator: $manager_email / $member_password"
}

echo '=== Project member role API and web checks ==='
check_project_member_roles
