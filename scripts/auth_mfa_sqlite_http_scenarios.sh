#!/usr/bin/env bash

matrix_scenario_init() {
    auth_mfa_fixture_init "$1" "$DURPDEPLOY_BASE_URL" matrix_db_query \
        "$DURPDEPLOY_AUTH_MFA_SERVER_LOG"
}

matrix_input_value() {
    local name=$1

    python3 - "$AUTH_MFA_HTTP_BODY" "$name" <<'PY'
import re
import sys
from pathlib import Path

body = Path(sys.argv[1]).read_text("utf-8", "replace")
name = re.escape(sys.argv[2])
match = re.search(rf'name="{name}" value="([^"]+)"', body)
if match:
    print(match.group(1))
    raise SystemExit(0)
raise SystemExit(1)
PY
}

matrix_totp_code() {
    python3 - "$1" <<'PY'
import base64
import hashlib
import hmac
import struct
import sys
import time

seed = sys.argv[1]
counter = int(time.time()) // 30
digest = hmac.new(base64.b32decode(seed), struct.pack(">Q", counter), hashlib.sha1).digest()
offset = digest[-1] & 15
value = (struct.unpack(">I", digest[offset:offset + 4])[0] & 0x7fffffff) % 1000000
print(f"{value:06d}")
PY
}

matrix_next_totp_code() {
    local seconds

    seconds=$((30 - $(date +%s) % 30))
    sleep "$seconds"
    matrix_totp_code "$1"
}

matrix_assert_no_final_session() {
    auth_mfa_assert_db_count "no final browser session" 0 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID"
}

matrix_assert_pending_challenge() {
    auth_mfa_assert_db_count "one pending login challenge" 1 \
        "SELECT COUNT(*) FROM mfa_challenges WHERE user_id = $MATRIX_USER_ID AND purpose = 'login_mfa'"
}

matrix_pending_csrf() {
    auth_mfa_http_request "$MATRIX_USER_JAR" GET /login/mfa
    auth_mfa_assert_status 200
    matrix_input_value csrf_token
}

matrix_factor_request() {
    local path=$1 code=$2 csrf

    csrf=$(python3 - "$MATRIX_USER_JAR" <<'PY'
import sys
from pathlib import Path

for line in Path(sys.argv[1]).read_text("utf-8", "replace").splitlines():
    fields = line.split("\t")
    if len(fields) == 7 and fields[5] == "mfa_csrf":
        print(fields[6])
        raise SystemExit(0)
raise SystemExit(1)
PY
) || auth_mfa_fail "pending MFA cookies omitted CSRF token"
    auth_mfa_secret_add "$csrf"
    auth_mfa_form_request "$MATRIX_USER_JAR" POST "$path" \
        "csrf_token=$(auth_mfa_urlencode "$csrf")&code=$(auth_mfa_urlencode "$code")"
}

matrix_pending_cookie_value() {
    local jar=$1

    python3 - "$jar" <<'PY'
import sys
from pathlib import Path

for line in Path(sys.argv[1]).read_text("utf-8", "replace").splitlines():
    fields = line.split("\t")
    if len(fields) == 7 and fields[5] == "mfa_csrf":
        print(fields[6])
        raise SystemExit(0)
raise SystemExit(1)
PY
}

matrix_concurrent_factor_requests() {
    local path=$1 code=$2 first_jar second_jar first_csrf second_csrf
    local first_status second_status winners

    first_jar="$AUTH_MFA_FIXTURE_TMP/concurrent-first.cookies"
    second_jar="$AUTH_MFA_FIXTURE_TMP/concurrent-second.cookies"
    cp "$MATRIX_USER_JAR" "$first_jar"
    cp "$MATRIX_USER_JAR" "$second_jar"
    first_csrf=$(matrix_pending_cookie_value "$first_jar") || auth_mfa_fail "first pending CSRF is missing"
    second_csrf=$(matrix_pending_cookie_value "$second_jar") || auth_mfa_fail "second pending CSRF is missing"
    auth_mfa_secret_add "$first_csrf"
    auth_mfa_secret_add "$second_csrf"
    curl -sS -b "$first_jar" -c "$first_jar" -o "$AUTH_MFA_FIXTURE_TMP/concurrent-first.body" \
        -w '%{http_code}' -X POST -H 'Content-Type: application/x-www-form-urlencoded' \
        --data-raw "csrf_token=$(auth_mfa_urlencode "$first_csrf")&code=$(auth_mfa_urlencode "$code")" \
        "$DURPDEPLOY_BASE_URL$path" >"$AUTH_MFA_FIXTURE_TMP/concurrent-first.status" &
    local first_pid=$!
    curl -sS -b "$second_jar" -c "$second_jar" -o "$AUTH_MFA_FIXTURE_TMP/concurrent-second.body" \
        -w '%{http_code}' -X POST -H 'Content-Type: application/x-www-form-urlencoded' \
        --data-raw "csrf_token=$(auth_mfa_urlencode "$second_csrf")&code=$(auth_mfa_urlencode "$code")" \
        "$DURPDEPLOY_BASE_URL$path" >"$AUTH_MFA_FIXTURE_TMP/concurrent-second.status" &
    local second_pid=$!
    wait "$first_pid"
    wait "$second_pid"
    first_status=$(<"$AUTH_MFA_FIXTURE_TMP/concurrent-first.status")
    second_status=$(<"$AUTH_MFA_FIXTURE_TMP/concurrent-second.status")
    winners=0
    [[ $first_status == 303 ]] && winners=$((winners + 1))
    [[ $second_status == 303 ]] && winners=$((winners + 1))
    [[ $winners == 1 && ( $first_status == 422 || $second_status == 422 ) ]] || \
        auth_mfa_fail "concurrent factor requests did not produce one winner"
}

matrix_create_mfa_user() {
    local scenario=$1 admin_jar admin_csrf admin_token user_jar user_csrf
    local enrollment_csrf challenge_token challenge_csrf recovery_count

    admin_jar=$(auth_mfa_cookie_jar admin)
    auth_mfa_login "$admin_jar" "$DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL" \
        "$DURPDEPLOY_AUTH_MFA_ADMIN_PASSWORD"
    auth_mfa_assert_status 303
    admin_csrf=$(auth_mfa_csrf_from_cookies "$admin_jar")
    admin_token=$(auth_mfa_mint_api_token "$admin_jar" "$admin_csrf" "$scenario")
    MATRIX_USER_EMAIL="$scenario@test.local"
    MATRIX_USER_PASSWORD="${scenario}-password-1234"
    auth_mfa_create_user "$admin_token" user deployer "$MATRIX_USER_EMAIL" \
        "$MATRIX_USER_PASSWORD"
    MATRIX_USER_ID=$(auth_mfa_user_id user)
    user_jar=$(auth_mfa_cookie_jar user)
    auth_mfa_login "$user_jar" "$MATRIX_USER_EMAIL" "$MATRIX_USER_PASSWORD"
    auth_mfa_assert_status 303
    user_csrf=$(auth_mfa_csrf_from_cookies "$user_jar")
    auth_mfa_form_request "$user_jar" POST /settings/security/reauth \
        "password=$(auth_mfa_urlencode "$MATRIX_USER_PASSWORD")&csrf_token=$(auth_mfa_urlencode "$user_csrf")"
    auth_mfa_assert_status 303
    user_csrf=$(auth_mfa_csrf_from_cookies "$user_jar")
    auth_mfa_form_request "$user_jar" POST /settings/security/totp/begin \
        "csrf_token=$(auth_mfa_urlencode "$user_csrf")"
    auth_mfa_assert_status 200
    auth_mfa_assert_header_contains Cache-Control no-store
    MATRIX_TOTP_SEED=$(python3 - "$AUTH_MFA_HTTP_BODY" <<'PY'
import re
import sys
from pathlib import Path

body = Path(sys.argv[1]).read_text("utf-8", "replace")
match = re.search(r'id="totp-manual-key"[^>]*>([^<]+)', body)
if match:
    print(match.group(1))
    raise SystemExit(0)
raise SystemExit(1)
PY
) || auth_mfa_fail "TOTP enrollment omitted manual seed"
    auth_mfa_secret_add "$MATRIX_TOTP_SEED"
    enrollment_csrf=$user_csrf
    challenge_token=$(matrix_input_value challenge_token) || auth_mfa_fail "TOTP enrollment omitted challenge token"
    challenge_csrf=$(matrix_input_value challenge_csrf) || auth_mfa_fail "TOTP enrollment omitted challenge CSRF"
    auth_mfa_secret_add "$challenge_token"
    auth_mfa_secret_add "$challenge_csrf"
    MATRIX_TOTP_CODE=$(matrix_totp_code "$MATRIX_TOTP_SEED")
    auth_mfa_secret_add "$MATRIX_TOTP_CODE"
    auth_mfa_form_request "$user_jar" POST /settings/security/totp/confirm \
        "csrf_token=$(auth_mfa_urlencode "$enrollment_csrf")&challenge_token=$(auth_mfa_urlencode "$challenge_token")&challenge_csrf=$(auth_mfa_urlencode "$challenge_csrf")&code=$(auth_mfa_urlencode "$MATRIX_TOTP_CODE")"
    auth_mfa_assert_status 200
    recovery_count=$(grep -o 'recovery-code' "$AUTH_MFA_HTTP_BODY" | wc -l)
    [[ $recovery_count == 10 ]] || auth_mfa_fail "TOTP enrollment did not create ten recovery codes"
    MATRIX_RECOVERY_CODE=$(python3 - "$AUTH_MFA_HTTP_BODY" <<'PY'
import re
import sys
from pathlib import Path

body = Path(sys.argv[1]).read_text("utf-8", "replace")
match = re.search(r'class="recovery-code[^>]*>([^<]+)', body)
if match:
    print(match.group(1))
    raise SystemExit(0)
raise SystemExit(1)
PY
) || auth_mfa_fail "TOTP enrollment omitted recovery code"
    auth_mfa_secret_add "$MATRIX_RECOVERY_CODE"
    MATRIX_USER_JAR=$(auth_mfa_cookie_jar pending)
    auth_mfa_login "$MATRIX_USER_JAR" "$MATRIX_USER_EMAIL" "$MATRIX_USER_PASSWORD"
    auth_mfa_assert_status 303
    auth_mfa_assert_header_contains Cache-Control no-store
    auth_mfa_assert_cookie "$MATRIX_USER_JAR" mfa_pending
    auth_mfa_assert_cookie "$MATRIX_USER_JAR" mfa_csrf
    matrix_assert_no_final_session
    matrix_assert_pending_challenge
}

matrix_login_password_session_transition() {
    local jar unknown_body invalid_body csrf

    matrix_scenario_init login-password-session-transition
    jar=$(auth_mfa_cookie_jar password)
    MATRIX_USER_ID=$(matrix_db_query "SELECT id FROM users WHERE email = '$DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL'")
    auth_mfa_login "$jar" "$DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL" wrong-password-1234
    auth_mfa_assert_status 422
    invalid_body=$(<"$AUTH_MFA_HTTP_BODY")
    matrix_assert_no_final_session
    auth_mfa_assert_audit_count login 0 "$MATRIX_USER_ID"
    auth_mfa_login "$jar" unknown-login@test.local wrong-password-1234
    auth_mfa_assert_status 422
    unknown_body=$(<"$AUTH_MFA_HTTP_BODY")
    [[ $invalid_body == "$unknown_body" ]] || auth_mfa_fail "known and unknown password failures differ"
    auth_mfa_login "$jar" "$DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL" "$DURPDEPLOY_AUTH_MFA_ADMIN_PASSWORD"
    auth_mfa_assert_status 303
    auth_mfa_assert_cookie "$jar" session
    auth_mfa_assert_db_count "one final browser session" 1 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID"
    csrf=$(auth_mfa_csrf_from_cookies "$jar")
    auth_mfa_form_request "$jar" POST /logout "csrf_token=$(auth_mfa_urlencode "$csrf")"
    auth_mfa_assert_status 303
    matrix_assert_no_final_session
}

matrix_pending_mfa_isolation_and_cancel() {
    local project_before

    matrix_scenario_init pending-mfa-isolation-and-cancel
    matrix_create_mfa_user pending-mfa
    auth_mfa_http_request "$MATRIX_USER_JAR" GET /login/mfa
    auth_mfa_assert_status 200
    auth_mfa_assert_header_contains Cache-Control no-store
    auth_mfa_http_request "$MATRIX_USER_JAR" GET /
    auth_mfa_assert_status 303
    project_before=$(matrix_db_query "SELECT COUNT(*) FROM projects")
    auth_mfa_form_request "$MATRIX_USER_JAR" POST /projects "name=pending-write"
    auth_mfa_assert_status 303
    auth_mfa_assert_db_count "pending write does not mutate projects" "$project_before" \
        "SELECT COUNT(*) FROM projects"
    auth_mfa_assert_audit_count create_project 0 "$MATRIX_USER_ID"
    MATRIX_PENDING_CSRF=$(matrix_pending_csrf)
    auth_mfa_form_request "$MATRIX_USER_JAR" POST /login/mfa/cancel \
        "csrf_token=$(auth_mfa_urlencode "$MATRIX_PENDING_CSRF")"
    auth_mfa_assert_status 303
    matrix_assert_no_final_session
    auth_mfa_assert_db_count "cancelled pending challenge" 0 \
        "SELECT COUNT(*) FROM mfa_challenges WHERE user_id = $MATRIX_USER_ID AND purpose = 'login_mfa'"
}

matrix_challenge_binding_expiry_replay_throttle() {
    local first_session_count

    matrix_scenario_init challenge-binding-expiry-replay-throttle
    matrix_create_mfa_user challenge-attack
    matrix_factor_request /login/mfa/totp not-a-code
    auth_mfa_assert_status 422
    auth_mfa_assert_header_contains Cache-Control no-store
    matrix_assert_no_final_session
    auth_mfa_assert_audit_count mfa_login_factor 0 "$MATRIX_USER_ID"
    matrix_db_query "UPDATE mfa_challenges SET expires_at = 0 WHERE user_id = $MATRIX_USER_ID AND purpose = 'login_mfa'"
    matrix_factor_request /login/mfa/totp "$MATRIX_TOTP_CODE"
    auth_mfa_assert_status 422
    matrix_assert_no_final_session
    auth_mfa_assert_db_count "expired challenge is discarded" 0 \
        "SELECT COUNT(*) FROM mfa_challenges WHERE user_id = $MATRIX_USER_ID AND purpose = 'login_mfa'"
    matrix_create_mfa_user challenge-replay
    MATRIX_TOTP_CODE=$(matrix_next_totp_code "$MATRIX_TOTP_SEED")
    matrix_concurrent_factor_requests /login/mfa/totp "$MATRIX_TOTP_CODE"
    first_session_count=$(matrix_db_query "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID")
    [[ $first_session_count == 1 ]] || auth_mfa_fail "valid factor did not create exactly one final session"
    auth_mfa_assert_db_count "replay creates no second final session" 1 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID"
}

matrix_totp_enrollment_and_login() {
    matrix_scenario_init totp-enrollment-and-login
    matrix_create_mfa_user totp-login
    matrix_factor_request /login/mfa/totp 12345
    auth_mfa_assert_status 422
    matrix_assert_no_final_session
    MATRIX_TOTP_CODE=$(matrix_next_totp_code "$MATRIX_TOTP_SEED")
    matrix_factor_request /login/mfa/totp "$MATRIX_TOTP_CODE"
    auth_mfa_assert_status 303
    auth_mfa_assert_db_count "one TOTP factor" 1 \
        "SELECT COUNT(*) FROM mfa_totp WHERE user_id = $MATRIX_USER_ID"
    auth_mfa_assert_db_count "one TOTP login session" 1 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID"
    auth_mfa_assert_audit_count mfa_login_factor 1 "$MATRIX_USER_ID"
}

matrix_recovery_use_regenerate_handoff() {
    matrix_scenario_init recovery-use-regenerate-handoff
    matrix_create_mfa_user recovery-login
    matrix_factor_request /login/mfa/recovery bad-code
    auth_mfa_assert_status 422
    matrix_assert_no_final_session
    matrix_concurrent_factor_requests /login/mfa/recovery "$MATRIX_RECOVERY_CODE"
    auth_mfa_assert_db_count "recovery code is consumed once" 1 \
        "SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = $MATRIX_USER_ID AND used_at IS NOT NULL"
    auth_mfa_assert_db_count "recovery login creates one session" 1 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID"
    auth_mfa_login "$MATRIX_USER_JAR" "$MATRIX_USER_EMAIL" "$MATRIX_USER_PASSWORD"
    auth_mfa_assert_status 303
    matrix_factor_request /login/mfa/recovery "$MATRIX_RECOVERY_CODE"
    auth_mfa_assert_status 422
    auth_mfa_assert_db_count "recovery replay creates no second session" 1 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID"
}

matrix_cache_and_artifact_secret_safety() {
    matrix_scenario_init cache-and-artifact-secret-safety
    matrix_create_mfa_user cache-safety
    MATRIX_PENDING_CSRF=$(matrix_pending_csrf)
    auth_mfa_http_request "$MATRIX_USER_JAR" POST /login/mfa/webauthn/finish '{}' \
        application/json "X-CSRF-Token: $MATRIX_PENDING_CSRF"
    auth_mfa_assert_status 422
    auth_mfa_assert_header_contains Cache-Control no-store
    auth_mfa_assert_body_not_contains "TOTP seed" "$MATRIX_TOTP_SEED"
    auth_mfa_assert_body_not_contains "recovery code" "$MATRIX_RECOVERY_CODE"
    matrix_assert_no_final_session
    matrix_assert_pending_challenge
    auth_mfa_assert_audit_count mfa_login_factor 0 "$MATRIX_USER_ID"
}

matrix_security_reauth_factors() {
    local primary_jar second_jar second_csrf reauth_token reauth_challenge_csrf

    matrix_scenario_init security-reauth-factors
    matrix_create_mfa_user security-reauth
    matrix_factor_request /login/mfa/recovery "$MATRIX_RECOVERY_CODE"
    auth_mfa_assert_status 303
    primary_jar=$MATRIX_USER_JAR
    second_jar=$(auth_mfa_cookie_jar second-session)
    auth_mfa_login "$second_jar" "$MATRIX_USER_EMAIL" "$MATRIX_USER_PASSWORD"
    auth_mfa_assert_status 303
    MATRIX_USER_JAR=$second_jar
    MATRIX_TOTP_CODE=$(matrix_next_totp_code "$MATRIX_TOTP_SEED")
    matrix_factor_request /login/mfa/totp "$MATRIX_TOTP_CODE"
    auth_mfa_assert_status 303
    MATRIX_USER_JAR=$primary_jar
    auth_mfa_assert_db_count "two active browser sessions" 2 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID"
    matrix_db_query "UPDATE sessions SET reauthenticated_at = NULL WHERE user_id = $MATRIX_USER_ID"
    auth_mfa_assert_db_count "no fresh reauthentication before proof" 0 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID AND reauthenticated_at IS NOT NULL"
    second_csrf=$(auth_mfa_csrf_from_cookies "$MATRIX_USER_JAR" /settings/security)
    auth_mfa_form_request "$MATRIX_USER_JAR" POST /settings/security/reauth \
        "csrf_token=$(auth_mfa_urlencode "$second_csrf")&password=$(auth_mfa_urlencode "$MATRIX_USER_PASSWORD")"
    auth_mfa_assert_status 200
    reauth_token=$(matrix_input_value challenge_token) || auth_mfa_fail "reauthentication omitted challenge token"
    reauth_challenge_csrf=$(matrix_input_value challenge_csrf) || auth_mfa_fail "reauthentication omitted challenge CSRF"
    auth_mfa_secret_add "$reauth_token"
    auth_mfa_secret_add "$reauth_challenge_csrf"
    auth_mfa_form_request "$MATRIX_USER_JAR" POST /settings/security/reauth/totp \
        "csrf_token=$(auth_mfa_urlencode "$second_csrf")&challenge_token=$(auth_mfa_urlencode "$reauth_token")&challenge_csrf=altered&code=000000"
    auth_mfa_assert_status 422
    auth_mfa_assert_db_count "altered reauth proof changes no session" 0 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID AND reauthenticated_at IS NOT NULL"
    auth_mfa_assert_audit_count reauthenticate 1 "$MATRIX_USER_ID"
    MATRIX_TOTP_CODE=$(matrix_next_totp_code "$MATRIX_TOTP_SEED")
    auth_mfa_form_request "$MATRIX_USER_JAR" POST /settings/security/reauth/totp \
        "csrf_token=$(auth_mfa_urlencode "$second_csrf")&challenge_token=$(auth_mfa_urlencode "$reauth_token")&challenge_csrf=$(auth_mfa_urlencode "$reauth_challenge_csrf")&code=$(auth_mfa_urlencode "$MATRIX_TOTP_CODE")"
    auth_mfa_assert_status 303
    auth_mfa_assert_header_contains Location /settings/security
    auth_mfa_assert_db_count "only active session reauthenticated" 1 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID AND reauthenticated_at IS NOT NULL"
    auth_mfa_assert_audit_count reauthenticate 2 "$MATRIX_USER_ID"
}

matrix_admin_mfa_reset_continuation() {
    local admin_jar admin_csrf admin_token admin_id target_token target_csrf

    matrix_scenario_init admin-mfa-reset-continuation
    matrix_create_mfa_user admin-reset-target
    matrix_factor_request /login/mfa/recovery "$MATRIX_RECOVERY_CODE"
    auth_mfa_assert_status 303
    target_csrf=$(auth_mfa_csrf_from_cookies "$MATRIX_USER_JAR" /settings/security)
    target_token=$(auth_mfa_mint_api_token "$MATRIX_USER_JAR" "$target_csrf" reset-target)
    admin_jar=$(auth_mfa_cookie_jar reset-admin)
    auth_mfa_login "$admin_jar" "$DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL" \
        "$DURPDEPLOY_AUTH_MFA_ADMIN_PASSWORD"
    auth_mfa_assert_status 303
    admin_id=$(matrix_db_query "SELECT id FROM users WHERE email = '$DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL'")
    matrix_db_query "UPDATE sessions SET reauthenticated_at = NULL WHERE user_id = $admin_id"
    admin_csrf=$(auth_mfa_csrf_from_cookies "$admin_jar" /admin/users)
    auth_mfa_form_request "$admin_jar" POST "/admin/users/$MATRIX_USER_ID/mfa-reset" \
        "csrf_token=$(auth_mfa_urlencode "$admin_csrf")&reason=invalid"
    auth_mfa_assert_status 422
    auth_mfa_assert_db_count "invalid reset reason preserves factors" 1 \
        "SELECT COUNT(*) FROM mfa_totp WHERE user_id = $MATRIX_USER_ID"
    auth_mfa_assert_audit_count mfa_admin_reset 0 "$admin_id"
    auth_mfa_form_request "$admin_jar" POST "/admin/users/$MATRIX_USER_ID/mfa-reset" \
        "csrf_token=$(auth_mfa_urlencode "$admin_csrf")&reason=administrative_reset"
    auth_mfa_assert_status 303
    auth_mfa_assert_header_contains Location /settings/security/reauth
    auth_mfa_assert_db_count "one reset continuation" 1 \
        "SELECT COUNT(*) FROM mfa_challenges WHERE user_id = $admin_id AND purpose = 'admin_mfa_reset'"
    auth_mfa_assert_db_count "stale reset preserves target factors" 1 \
        "SELECT COUNT(*) FROM mfa_totp WHERE user_id = $MATRIX_USER_ID"
    auth_mfa_form_request "$admin_jar" POST /settings/security/reauth \
        "csrf_token=$(auth_mfa_urlencode "$admin_csrf")&password=$(auth_mfa_urlencode "$DURPDEPLOY_AUTH_MFA_ADMIN_PASSWORD")"
    auth_mfa_assert_status 303
    auth_mfa_assert_header_contains Location /admin/users
    auth_mfa_assert_db_count "reset removes TOTP" 0 \
        "SELECT COUNT(*) FROM mfa_totp WHERE user_id = $MATRIX_USER_ID"
    auth_mfa_assert_db_count "reset removes recovery codes" 0 \
        "SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = $MATRIX_USER_ID"
    auth_mfa_assert_db_count "reset removes browser sessions" 0 \
        "SELECT COUNT(*) FROM sessions WHERE user_id = $MATRIX_USER_ID"
    auth_mfa_assert_db_count "reset removes rate limits" 0 \
        "SELECT COUNT(*) FROM mfa_rate_limits WHERE user_id = $MATRIX_USER_ID"
    auth_mfa_api_request "$target_token" GET /api/v1/users/me
    auth_mfa_assert_status 200
    auth_mfa_assert_audit_count mfa_admin_reset 1 "$admin_id"
    auth_mfa_form_request "$admin_jar" POST /settings/security/reauth \
        "csrf_token=$(auth_mfa_urlencode "$admin_csrf")&password=$(auth_mfa_urlencode "$DURPDEPLOY_AUTH_MFA_ADMIN_PASSWORD")"
    auth_mfa_assert_status 303
    auth_mfa_assert_header_contains Location /settings/security
    auth_mfa_assert_audit_count mfa_admin_reset 1 "$admin_id"
}

matrix_forced_failure() {
    matrix_scenario_init cache-and-artifact-secret-safety
    auth_mfa_secret_add forced-matrix-password-secret
    auth_mfa_http_request - GET /healthz
    auth_mfa_assert_status 418
}
