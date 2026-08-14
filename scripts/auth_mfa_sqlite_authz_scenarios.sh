#!/usr/bin/env bash

authz_scenario_init() {
    auth_mfa_fixture_init "$1" "$DURPDEPLOY_BASE_URL" authz_db_query \
        "$DURPDEPLOY_AUTH_MFA_SERVER_LOG"
}

authz_db_query() {
    sqlite3 "$DURPDEPLOY_AUTH_MFA_DB" "$1"
}

authz_json_error() {
    local expected=$1

    auth_mfa_assert_header_contains Content-Type application/json
    auth_mfa_assert_body_contains "JSON error" "\"error\":\"$expected\""
}

authz_create_token() {
    local user_id=$1 label=$2 token hash prefix

    token="ddp_pat_$(openssl rand -hex 32)"
    hash=$(printf '%s' "${token#ddp_pat_}" | sha256sum | cut -d ' ' -f 1)
    prefix=${token:0:12}
    auth_mfa_secret_add "$token"
    authz_db_query "INSERT INTO api_tokens (id, user_id, token_hash, token_prefix, name, scope, created_at) VALUES ('$label', $user_id, '$hash', '$prefix', '$label', 'global', strftime('%s','now'))"
    printf '%s\n' "$token"
}

authz_create_user() {
    local token=$1 label=$2 role=$3
    local password="${label}-password-1234"

    auth_mfa_create_user "$token" "$label" "$role" "${label}-authz.test" "$password"
}

authz_browser_login() {
    local label=$1 jar=$2

    auth_mfa_login "$jar" "$(auth_mfa_user_email "$label")" \
        "$(auth_mfa_user_password "$label")"
    auth_mfa_assert_status 303
}

matrix_csrf_viewer_role_write_rejection() {
    local admin_jar admin_csrf admin_token deployer_jar deployer_csrf viewer_jar
    local viewer_token admin_id deployer_id viewer_id projects_before admin_project_id

    authz_scenario_init csrf-viewer-role-write-rejection
    admin_jar=$(auth_mfa_cookie_jar admin)
    auth_mfa_login "$admin_jar" "$DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL" \
        "$DURPDEPLOY_AUTH_MFA_ADMIN_PASSWORD"
    auth_mfa_assert_status 303
    admin_csrf=$(auth_mfa_csrf_from_cookies "$admin_jar")
    admin_token=$(auth_mfa_mint_api_token "$admin_jar" "$admin_csrf" authz-admin)
    admin_id=$(authz_db_query "SELECT id FROM users WHERE email = '$DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL'")

    authz_create_user "$admin_token" deployer deployer
    authz_create_user "$admin_token" viewer viewer
    deployer_id=$(auth_mfa_user_id deployer)
    viewer_id=$(auth_mfa_user_id viewer)
    viewer_token=$(authz_create_token "$viewer_id" authz-viewer-token)

    deployer_jar=$(auth_mfa_cookie_jar deployer)
    authz_browser_login deployer "$deployer_jar"
    deployer_csrf=$(auth_mfa_csrf_from_cookies "$deployer_jar")
    auth_mfa_form_request "$deployer_jar" POST /projects \
        "name=deployer-html&csrf_token=$(auth_mfa_urlencode "$deployer_csrf")"
    auth_mfa_assert_status 303
    auth_mfa_assert_audit_count create_project 1 "$deployer_id"

    auth_mfa_api_request "$admin_token" POST /api/v1/projects '{"name":"admin-json"}'
    auth_mfa_assert_status 201
    auth_mfa_assert_audit_count create_project 1 "$admin_id"
    admin_project_id=$(authz_db_query "SELECT id FROM projects WHERE name = 'admin-json'")

    viewer_jar=$(auth_mfa_cookie_jar viewer)
    authz_browser_login viewer "$viewer_jar"
    projects_before=$(authz_db_query 'SELECT COUNT(*) FROM projects')
    auth_mfa_form_request "$viewer_jar" POST /projects 'name=viewer-html'
    auth_mfa_assert_status 403
    auth_mfa_assert_header_contains Content-Type text/html
    auth_mfa_assert_body_contains "styled viewer 403" '<h1>Forbidden</h1>'
    auth_mfa_assert_body_contains "viewer 403 message" 'Viewers cannot perform write operations'
    auth_mfa_assert_body_contains "viewer back link" 'javascript:history.back()'

    auth_mfa_http_request "$viewer_jar" POST /projects '{"name":"viewer-htmx"}' \
        application/json 'HX-Request: true'
    auth_mfa_assert_status 200
    auth_mfa_assert_header_contains HX-Trigger makeToast
    auth_mfa_assert_header_contains HX-Trigger 'Viewers cannot perform write operations'

    auth_mfa_http_request "$viewer_jar" PUT /projects/1 '{"name":"viewer-put"}' \
        application/json 'HX-Request: true'
    auth_mfa_assert_status 200
    auth_mfa_assert_header_contains HX-Trigger makeToast
    auth_mfa_http_request "$viewer_jar" PATCH /lifecycles/1/stages/1 '{"requires_approval":true}' \
        application/json 'HX-Request: true'
    auth_mfa_assert_status 200
    auth_mfa_assert_header_contains HX-Trigger makeToast
    auth_mfa_http_request "$viewer_jar" DELETE /projects/1 '' '' 'HX-Request: true'
    auth_mfa_assert_status 200
    auth_mfa_assert_header_contains HX-Trigger makeToast
    auth_mfa_assert_db_count "viewer writes leave projects unchanged" "$projects_before" \
        'SELECT COUNT(*) FROM projects'
    auth_mfa_assert_audit_count create_project 0 "$viewer_id"

    auth_mfa_http_request "$deployer_jar" GET "/projects/$admin_project_id"
    auth_mfa_assert_status 403
    auth_mfa_assert_body_contains "non-member project denial" "You don't have access to this"
    auth_mfa_http_request "$deployer_jar" GET "/projects/$admin_project_id" '' '' \
        'HX-Request: true'
    auth_mfa_assert_status 200
    auth_mfa_assert_header_contains HX-Trigger "You don't have access to this"

    auth_mfa_api_request "$viewer_token" POST /api/v1/projects '{"name":"viewer-api"}'
    auth_mfa_assert_status 403
    authz_json_error 'viewers cannot perform write operations'
    auth_mfa_assert_db_count "API viewer write leaves projects unchanged" "$projects_before" \
        'SELECT COUNT(*) FROM projects'
    auth_mfa_assert_audit_count create_project 0 "$viewer_id"
}

matrix_api_token_machine_credential_separation() {
    local admin_jar admin_csrf token sessions_before tokens_before admin_id

    authz_scenario_init api-token-machine-credential-separation
    admin_jar=$(auth_mfa_cookie_jar admin)
    auth_mfa_login "$admin_jar" "$DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL" \
        "$DURPDEPLOY_AUTH_MFA_ADMIN_PASSWORD"
    auth_mfa_assert_status 303
    admin_id=$(authz_db_query "SELECT id FROM users WHERE email = '$DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL'")
    admin_csrf=$(auth_mfa_csrf_from_cookies "$admin_jar")
    token=$(auth_mfa_mint_api_token "$admin_jar" "$admin_csrf" authz-machine)
    sessions_before=$(authz_db_query "SELECT COUNT(*) FROM sessions WHERE user_id = $admin_id")
    tokens_before=$(authz_db_query "SELECT COUNT(*) FROM api_tokens WHERE user_id = $admin_id AND revoked_at IS NULL")

    auth_mfa_api_request "$token" GET /api/v1/users/me
    auth_mfa_assert_status 200
    auth_mfa_assert_header_contains Content-Type application/json
    auth_mfa_assert_body_contains "bearer identity" "\"id\":$admin_id"
    auth_mfa_assert_db_count "bearer call creates no browser session" "$sessions_before" \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $admin_id"
    auth_mfa_assert_db_count "bearer call preserves token" "$tokens_before" \
        "SELECT COUNT(*) FROM api_tokens WHERE user_id = $admin_id AND revoked_at IS NULL"

    auth_mfa_http_request - GET /api/v1/users/me
    auth_mfa_assert_status 401
    authz_json_error unauthenticated
    auth_mfa_http_request - GET /api/v1/users/me '' '' 'Authorization: Token nope'
    auth_mfa_assert_status 401
    authz_json_error 'invalid authorization format'
    auth_mfa_http_request - GET /api/v1/users/me '' '' 'Authorization: Bearer ddp_pat_bad'
    auth_mfa_assert_status 401
    authz_json_error 'invalid or revoked token'
    auth_mfa_http_request - GET /projects '' '' "Authorization: Bearer $token"
    auth_mfa_assert_status 303
    auth_mfa_assert_header_contains Location /login

    authz_db_query "UPDATE api_tokens SET revoked_at = strftime('%s','now') WHERE token_prefix = '${token:0:12}'"
    auth_mfa_api_request "$token" GET /api/v1/users/me
    auth_mfa_assert_status 401
    authz_json_error 'invalid or revoked token'
    auth_mfa_assert_db_count "rejected API calls create no browser session" "$sessions_before" \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $admin_id"
}
