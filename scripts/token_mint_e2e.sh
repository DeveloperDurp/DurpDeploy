#!/usr/bin/env bash
# Sourced by running_features_e2e.sh. Keep this run's accounts and token metadata.

token_mint_ceiling_e2e() (
    set -euo pipefail
    local email="e2e-mint-$E2E_RUN_ID@test.local" password cookie body csrf
    local user first second owner location role code before after
    password=$(openssl rand -hex 20)
    cookie=$(mktemp)
    body=$(mktemp)
    trap 'rm -f "$cookie" "$body"' EXIT
    user=$(api_post "{\"email\":\"$email\",\"name\":\"Token mint review\",\"password\":\"$password\",\"role\":\"maintainer\"}" "$BASE/api/v1/admin/users" |
        python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    first=$(api_post "{\"name\":\"e2e-mint-admin-$E2E_RUN_ID\"}" "$BASE/api/v1/projects" |
        python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    second=$(api_post "{\"name\":\"e2e-mint-viewer-$E2E_RUN_ID\"}" "$BASE/api/v1/projects" |
        python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    api_post "{\"user_id\":$user,\"role\":\"admin\"}" "$BASE/api/v1/projects/$first/members" >/dev/null
    api_post "{\"user_id\":$user,\"role\":\"viewer\"}" "$BASE/api/v1/projects/$second/members" >/dev/null
    code=$(curl -sS -b "$cookie" -c "$cookie" -o "$body" -w '%{http_code}' \
        --data-urlencode "email=$email" --data-urlencode "password=$password" \
        "$BASE/login")
    [[ "$code" == 303 ]] || { echo "FAIL: mint owner login=$code"; exit 1; }
    curl -fsS -b "$cookie" "$BASE/settings/tokens" >"$body"
    csrf=$(python3 -c 'import re,sys; print(re.search(r"name=\"csrf_token\" value=\"([^\"]+)\"",open(sys.argv[1]).read())[1])' "$body")
    location=$(curl -sS -b "$cookie" -D - -o /dev/null \
        --data-urlencode 'name=retained-mint-owner' --data-urlencode "csrf_token=$csrf" \
        "$BASE/settings/tokens" | awk 'tolower($1)=="location:" {gsub("\r", "", $2); print $2}')
    [[ "$location" == /settings/tokens\?flash=* ]] || exit 1
    owner=$(curl -fsS -b "$cookie" "$BASE$location" |
        python3 -c 'import re,sys; print(re.search(r"ddp_pat_[0-9a-f]{64}",sys.stdin.read())[0])')
    before=$(curl -fsS -H "Authorization: Bearer $owner" "$BASE/api/v1/tokens" |
        python3 -c 'import json,sys; print(json.load(sys.stdin)["total"])')
    for role in admin maintainer deployer viewer; do
        code=$(curl -sS -H "Authorization: Bearer $owner" -H 'Content-Type: application/json' \
            -o "$body" -w '%{http_code}' -d "{\"name\":\"retained-global-$role\",\"scope\":\"global\",\"role\":\"$role\"}" "$BASE/api/v1/tokens")
        if [[ "$role" == admin ]]; then
            [[ "$code" == 422 ]] || exit 1
            python3 -c 'import json,sys; assert json.load(open(sys.argv[1]))["error"]=="token role exceeds owner permissions"' "$body"
        else
            [[ "$code" == 201 ]] || exit 1
        fi
        echo "  API global $role: $code"
        code=$(curl -sS -b "$cookie" -o "$body" -w '%{http_code}' \
            --data-urlencode "name=retained-web-$role" --data-urlencode 'scope=global' \
            --data-urlencode "role=$role" --data-urlencode "csrf_token=$csrf" "$BASE/settings/tokens")
        if [[ "$role" == admin ]]; then
            [[ "$code" == 422 ]] || exit 1
            grep -q 'Token role exceeds owner permissions' "$body"
        else
            [[ "$code" == 303 ]] || exit 1
        fi
        echo "  Web global $role: $code"
    done
    for projects in "$first" "$first,$second"; do
        code=$(curl -sS -H "Authorization: Bearer $owner" -H 'Content-Type: application/json' \
            -o "$body" -w '%{http_code}' -d "{\"name\":\"retained-delegation\",\"scope\":\"scoped\",\"role\":\"admin\",\"project_ids\":[$projects]}" "$BASE/api/v1/tokens")
        if [[ "$projects" == "$first" ]]; then [[ "$code" == 201 ]]; else [[ "$code" == 422 ]]; fi
        echo "  API scoped admin projects=[$projects]: $code"
    done
    after=$(curl -fsS -H "Authorization: Bearer $owner" "$BASE/api/v1/tokens" |
        python3 -c 'import json,sys; print(json.load(sys.stdin)["total"])')
    [[ "$after" == "$((before+7))" ]] || { echo 'FAIL: rejected mint stored a token'; exit 1; }
    if [[ ${DURPDEPLOY_MINT_BROWSER:-1} == 1 ]]; then
        local session
        session=$(awk '$6=="session" {print $7}' "$cookie")
        DURPDEPLOY_LIVE_BASE="$BASE" DURPDEPLOY_LIVE_MINT_SESSION="$session" \
            bash scripts/go_test.sh -tags='e2e packagebrowser' -count=1 \
            -run '^TestTokenMintCeilingBrowserE2E$' ./internal/handler/api
    fi
    echo "  Retained mint owner: $email"
    echo "  Project Admin: $BASE/projects/$first"
    echo "  Project Viewer: $BASE/projects/$second"
    echo "  Token metadata: $BASE/settings/tokens"
    echo '  Use administrator password reset to sign in as the retained owner.'
)

token_mint_ceiling_e2e
