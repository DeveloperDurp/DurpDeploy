#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$SCRIPT_DIR/auth_mfa_http_fixture.sh"
source "$SCRIPT_DIR/auth_mfa_sqlite_http_scenarios.sh"

MATRIX_RESULTS=${DURPDEPLOY_AUTH_MFA_HTTP_MATRIX_RESULTS:-}
MATRIX_PASSED=0
MATRIX_FAILED=0
MATRIX_FORCE_FAILURE=0

matrix_usage() {
    echo "usage: DURPDEPLOY_AUTH_MFA_HTTP_MATRIX_CLIENT_ONLY=1 $0 [--force-failure]" >&2
}

matrix_require_environment() {
    local name value

    [[ ${DURPDEPLOY_AUTH_MFA_HTTP_MATRIX_CLIENT_ONLY:-} == 1 ]] || {
        echo "auth/MFA HTTP matrix: use scripts/e2e_test.sh as the isolated lifecycle owner" >&2
        return 2
    }
    for name in \
        DURPDEPLOY_BASE_URL \
        DURPDEPLOY_AUTH_MFA_DB \
        DURPDEPLOY_AUTH_MFA_SERVER_LOG \
        DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL \
        DURPDEPLOY_AUTH_MFA_ADMIN_PASSWORD; do
        value=${!name:-}
        [[ -n "$value" ]] || {
            echo "auth/MFA HTTP matrix: missing $name" >&2
            return 2
        }
    done
    command -v sqlite3 >/dev/null 2>&1 || {
        echo "auth/MFA HTTP matrix: sqlite3 is required" >&2
        return 2
    }
    [[ -f $DURPDEPLOY_AUTH_MFA_DB ]] || {
        echo "auth/MFA HTTP matrix: SQLite database is unavailable" >&2
        return 2
    }
    if [[ -n $MATRIX_RESULTS && ! -d $(dirname -- "$MATRIX_RESULTS") ]]; then
        echo "auth/MFA HTTP matrix: result directory does not exist" >&2
        return 2
    fi
}

matrix_db_query() {
    sqlite3 "$DURPDEPLOY_AUTH_MFA_DB" "$1"
}

matrix_emit() {
    local result=$1
    local line

    line=$(printf '{"scenario_id":"%s","engine":"sqlite","result":"%s"}' \
        "$MATRIX_SCENARIO" "$result")
    printf '%s\n' "$line"
    if [[ -n $MATRIX_RESULTS ]]; then
        printf '%s\n' "$line" >> "$MATRIX_RESULTS"
    fi
}

matrix_run() {
    local status

    MATRIX_SCENARIO=$1
    shift
    set +e
    (
        set -e
        trap 'auth_mfa_fixture_cleanup || true' EXIT
        "$@"
    )
    status=$?
    set -e
    if (( status == 0 )); then
        MATRIX_PASSED=$((MATRIX_PASSED + 1))
        matrix_emit pass
        return 0
    fi
    MATRIX_FAILED=$((MATRIX_FAILED + 1))
    matrix_emit fail
    return 0
}

matrix_summary() {
    local line

    line=$(printf '{"summary":{"engine":"sqlite","passed":%d,"failed":%d}}' \
        "$MATRIX_PASSED" "$MATRIX_FAILED")
    printf '%s\n' "$line"
    if [[ -n $MATRIX_RESULTS ]]; then
        printf '%s\n' "$line" >> "$MATRIX_RESULTS"
    fi
}

matrix_parse_args() {
    case $# in
    0) ;;
    1)
        [[ $1 == --force-failure ]] || {
            matrix_usage
            return 2
        }
        MATRIX_FORCE_FAILURE=1
        ;;
    *)
        matrix_usage
        return 2
        ;;
    esac
}

main() {
    matrix_parse_args "$@"
    matrix_require_environment
    : > "${MATRIX_RESULTS:-/dev/null}"

    if (( MATRIX_FORCE_FAILURE )); then
        matrix_run cache-and-artifact-secret-safety matrix_forced_failure
    else
        matrix_run login-password-session-transition matrix_login_password_session_transition
        matrix_run pending-mfa-isolation-and-cancel matrix_pending_mfa_isolation_and_cancel
        matrix_run challenge-binding-expiry-replay-throttle matrix_challenge_binding_expiry_replay_throttle
        matrix_run totp-enrollment-and-login matrix_totp_enrollment_and_login
        matrix_run recovery-use-regenerate-handoff matrix_recovery_use_regenerate_handoff
        matrix_run cache-and-artifact-secret-safety matrix_cache_and_artifact_secret_safety
        matrix_run security-reauth-factors matrix_security_reauth_factors
        matrix_run admin-mfa-reset-continuation matrix_admin_mfa_reset_continuation
    fi
    matrix_summary
    (( MATRIX_FAILED == 0 ))
}

main "$@"
