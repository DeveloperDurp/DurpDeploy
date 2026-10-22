#!/usr/bin/env bash
# Sourced by the running-server suite. Retain groups and projects for manual checks.

check_project_groups() {
    local group_http_code='%{http_code}'
    local group temporary first second private code listing
    local json_id='import json,sys; print(json.load(sys.stdin)["id"])'
    group=$(api_post "{\"name\":\"Group team $E2E_RUN_ID\"}" "$BASE/api/v1/project-groups" | python3 -c "$json_id")
    temporary=$(api_post "{\"name\":\"Group disposable $E2E_RUN_ID\"}" "$BASE/api/v1/project-groups" | python3 -c "$json_id")
    first=$(api_post "{\"name\":\"Group Alpha $E2E_RUN_ID\"}" "$BASE/api/v1/projects" | python3 -c "$json_id")
    second=$(api_post "{\"name\":\"Group Beta $E2E_RUN_ID\"}" "$BASE/api/v1/projects" | python3 -c "$json_id")
    private=$(api_post "{\"name\":\"Group Private $E2E_RUN_ID\"}" "$BASE/api/v1/projects" | python3 -c "$json_id")
    api_put "{\"group_id\":$group}" "$BASE/api/v1/projects/$first/group" >/dev/null
    api_put "{\"group_id\":$group}" "$BASE/api/v1/projects/$private/group" >/dev/null
    api_post "{\"user_id\":$VIEWER_USER_ID,\"role\":\"viewer\"}" "$BASE/api/v1/projects/$first/members" >/dev/null
    api_post "{\"user_id\":$VIEWER_USER_ID,\"role\":\"viewer\"}" "$BASE/api/v1/projects/$second/members" >/dev/null
    api_put "{\"group_id\":$temporary}" "$BASE/api/v1/projects/$second/group" >/dev/null
    api_get "$BASE/api/v1/projects?group_id=$group&sort=name&limit=1&offset=1" \
        | python3 -c 'import json,sys; p=json.load(sys.stdin); assert p["total"]==2 and len(p["items"])==1 and p["items"][0]["id"]==int(sys.argv[1])' "$private"
    api_get "$BASE/api/v1/projects?q=Group%20Alpha%20$E2E_RUN_ID" \
        | python3 -c 'import json,sys; p=json.load(sys.stdin); assert p["total"]==1 and p["items"][0]["id"]==int(sys.argv[1])' "$first"
    listing=$(curl -fsS -H "Authorization: Bearer $VIEWER_TOKEN" "$BASE/api/v1/project-groups")
    printf '%s' "$listing" | python3 -c 'import json,sys; p=json.load(sys.stdin); g=next(g for g in p["items"] if g["id"]==int(sys.argv[1])); assert g["project_count"]==1' "$group"
    code=$(curl -sS -H "Authorization: Bearer $VIEWER_TOKEN" -H 'Content-Type: application/json' \
        -X PUT -d '{"group_id":null}' -o /dev/null -w "$group_http_code" "$BASE/api/v1/projects/$first/group")
    [[ "$code" == 403 ]] || { echo "FAIL: viewer project move=$code"; return 1; }
    code=$(curl -sS -H "Authorization: Bearer $API_TOKEN" -X DELETE \
        -o /dev/null -w "$group_http_code" "$BASE/api/v1/project-groups/$temporary")
    [[ "$code" == 204 ]] || { echo "FAIL: group deletion=$code"; return 1; }
    api_get "$BASE/api/v1/projects/$second" \
        | python3 -c 'import json,sys; assert json.load(sys.stdin)["group_id"] is None'
    api_put "{\"name\":\"Group renamed $E2E_RUN_ID\"}" "$BASE/api/v1/project-groups/$group" >/dev/null
    code=$(curl -sS -b "$COOKIES" -H "X-CSRF-Token: $CSRF" -X POST \
        -d "group_id=$group" -o /dev/null -w "$group_http_code" "$BASE/projects/$second/group")
    [[ "$code" == 303 ]] || { echo "FAIL: web project move=$code"; return 1; }
    code=$(curl -sS -b "$COOKIES" -H "X-CSRF-Token: $CSRF" -X POST \
        -d 'group_id=' -o /dev/null -w "$group_http_code" "$BASE/projects/$second/group")
    [[ "$code" == 303 ]] || { echo "FAIL: web Ungrouped move=$code"; return 1; }
    # Recreate the disposable group so the deletion scenario remains repeatable.
    api_post "{\"name\":\"Group disposable $E2E_RUN_ID\"}" "$BASE/api/v1/project-groups" >/dev/null
    echo "  Grouped projects: $BASE/projects?group_id=$group"
    echo "  Ungrouped project: $BASE/projects/$second"
    echo "  Group management: $BASE/project-groups"
    echo "  Viewer-visible project: $BASE/projects/$first"
}

echo '=== Project group API and web checks ==='
check_project_groups
