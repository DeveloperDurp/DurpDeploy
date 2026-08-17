#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
MATRIX_RESULTS=${DURPDEPLOY_OIDC_HTTP_MATRIX_RESULTS:-}
MATRIX_ARTIFACT_DIR=${DURPDEPLOY_OIDC_HTTP_MATRIX_ARTIFACT_DIR:-"$ROOT_DIR/artifacts/auth-mfa"}
MATRIX_TMP=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-oidc-matrix.XXXXXX")
MATRIX_PASSED=0
MATRIX_FAILED=0
MATRIX_FORCE_FAILURE=0
MATRIX_LAST_TEST_STATUS=""

cleanup() {
    rm -rf -- "$MATRIX_TMP"
}
trap cleanup EXIT

matrix_usage() {
    echo "usage: $0 [--force-failure]" >&2
    echo "--force-failure runs one local-TLS fixture test, then intentionally fails its focused exit-status assertion." >&2
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

matrix_require_environment() {
    command -v go >/dev/null 2>&1 || {
        echo "OIDC HTTP matrix: go is required" >&2
        return 2
    }
    if [[ -n $MATRIX_RESULTS && ! -d $(dirname -- "$MATRIX_RESULTS") ]]; then
        echo "OIDC HTTP matrix: result directory does not exist" >&2
        return 2
    fi
}

matrix_emit() {
    local scenario=$1 result=$2 line

    line=$(printf '{"scenario_id":"%s","engine":"sqlite","result":"%s"}' \
        "$scenario" "$result")
    printf '%s\n' "$line"
    if [[ -n $MATRIX_RESULTS ]]; then
        printf '%s\n' "$line" >> "$MATRIX_RESULTS"
    fi
}

matrix_write_failure_artifact() {
    local scenario=$1 artifact

    artifact="$MATRIX_ARTIFACT_DIR/$scenario"
    rm -rf -- "$artifact"
    mkdir -p -- "$artifact"
    chmod 700 "$artifact"
    # Keep Go test output transient: fixture values must never become artifacts.
    printf '{"scenario_id":"%s","engine":"sqlite","command":"go test","status":"fail"}\n' \
        "$scenario" > "$artifact/summary.json"
}

matrix_clear_artifact() {
    rm -rf -- "$MATRIX_ARTIFACT_DIR/$1"
}

matrix_go_test() {
    local package=$1 pattern=$2 output="$MATRIX_TMP/go-test.out"

    if go test -count=1 -timeout=30s -run "$pattern" "$package" > "$output" 2>&1; then
        MATRIX_LAST_TEST_STATUS=0
        return 0
    fi
    MATRIX_LAST_TEST_STATUS=1
    return 1
}

matrix_expect_test_status() {
    [[ $MATRIX_LAST_TEST_STATUS == "$1" ]]
}

matrix_run() {
    local scenario=$1

    shift
    matrix_clear_artifact "$scenario"
    if "$@"; then
        MATRIX_PASSED=$((MATRIX_PASSED + 1))
        matrix_emit "$scenario" pass
        return 0
    fi
    MATRIX_FAILED=$((MATRIX_FAILED + 1))
    matrix_write_failure_artifact "$scenario"
    matrix_emit "$scenario" fail
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

oidc_valid_login() {
    matrix_go_test ./internal/handler \
        '^(TestOIDCLoginStart_RedirectsWithBoundTransaction|TestOIDCCallback_linksExistingUserAndIssuesSingleAuditedSession)$'
}

oidc_state_nonce_pkce_replay() {
    # ponytail: one scheduler thread stabilizes the in-memory fixture race; both callback clients still race.
    GOMAXPROCS=1 matrix_go_test ./internal/handler \
        '^(TestOIDCCallback_rejectsStateBeforeExchangingCode|TestOIDCCallback_consumesTransactionBeforeRejectingPKCEMismatch|TestOIDCCallback_rejectsReplayAfterIssuingOneSessionAndAudit|TestOIDCCallback_rejectsConcurrentReplayAfterOneSuccessfulExchange)$'
}

oidc_verifier() {
    matrix_go_test ./internal/oidc '^TestProviderExchangeRejectsUnverifiedTokens$' || return 1
    matrix_go_test ./internal/handler '^TestOIDCCallback_rejectsVerifiedToken_whenTrustBindingFails$'
}

oidc_verified_email_linking() {
    matrix_go_test ./internal/oidc '^TestParseClaims$' || return 1
    matrix_go_test ./internal/handler \
        '^(TestOIDCCallback_linksExistingUserAndIssuesSingleAuditedSession|TestOIDCCallback_rejectsClaims_whenSubjectOrEmailIsInvalid)$'
}

oidc_concurrent_first_login() {
    matrix_go_test ./internal/handler '^TestOIDCCallback_JITProvisionsMappedUser_whenIdentityIsNew$' || return 1
    matrix_go_test ./internal/oidc '^TestIdentity_ResolvesOneUserWhenConcurrent$'
}

oidc_group_role_downgrade_invalidation() {
    matrix_go_test ./internal/handler '^TestOIDCCallback_appliesConfiguredViewerRoleMapping$' || return 1
    matrix_go_test ./internal/oidc '^TestIdentity_InvalidatesSessionsOnRoleDowngrade$'
}

oidc_reauth_session_subject_binding() {
    matrix_go_test ./internal/handler \
        '^(TestOIDCReauthStart_requiresProtectedSession|TestOIDCReauthStart_bindsStoredIdentitySessionAndContinuation|TestOIDCReauthCallback_refreshesOnlyBoundSessionAndResumesContinuation|TestOIDCReauthCallback_rejectsDifferentSessionUserOrSubject|TestOIDCReauthCallback_requiresActiveBoundSession|TestOIDCReauthCallback_consumesTransactionOnlyOnce|TestOIDCReauthCallback_rejectsMissingOrStaleAuthTime)$'
}

oidc_provider_outage_password_fallback() {
    matrix_go_test ./internal/handler \
        '^(TestOIDCLoginStart_RendersGenericLogin_whenDiscoveryUnavailable|TestOIDCLoginStart_AllowsPasswordFallbackAfterOutage)$' || return 1
    matrix_go_test ./internal/server '^TestOIDCOutage_LeavesOtherAuthenticationSurfacesUsable$'
}

oidc_secret_cache_log_safety() {
    matrix_go_test ./internal/oidc \
        '^(TestProviderAuthorizationURLRetriesFailedDiscoveryAndCachesSuccess|TestProviderExchangeReusesJWKSForFixedKey|TestProviderExchangeSharesJWKSDuringConcurrentFirstUse|TestProviderExchangeRefreshesJWKSAfterKeyRotation|TestProviderCachedVerifierDoesNotRetainRequestContextValues)$' || return 1
    matrix_go_test ./internal/handler '^TestOIDCCallback_consumesTransactionBeforeRejectingExchangeFailure$'
}

oidc_disabled_regression() {
    matrix_go_test ./internal/server '^TestOIDCRoutes_AreAbsent_when_disabled$'
}

oidc_forced_failure() {
    oidc_valid_login || return 1
    matrix_expect_test_status 1
}

main() {
    matrix_parse_args "$@"
    matrix_require_environment
    : > "${MATRIX_RESULTS:-/dev/null}"
    cd "$ROOT_DIR"

    if (( MATRIX_FORCE_FAILURE )); then
        matrix_run oidc-valid-login oidc_forced_failure
    else
        matrix_run oidc-valid-login oidc_valid_login
        matrix_run oidc-state-nonce-pkce-replay oidc_state_nonce_pkce_replay
        matrix_run oidc-issuer-audience-signature-expiry-verifier oidc_verifier
        matrix_run oidc-verified-email-linking oidc_verified_email_linking
        matrix_run oidc-concurrent-first-login oidc_concurrent_first_login
        matrix_run oidc-group-role-downgrade-invalidation oidc_group_role_downgrade_invalidation
        matrix_run oidc-reauth-session-subject-binding oidc_reauth_session_subject_binding
        matrix_run oidc-provider-outage-password-fallback oidc_provider_outage_password_fallback
        matrix_run oidc-secret-cache-log-safety oidc_secret_cache_log_safety
        matrix_run oidc-disabled-regression oidc_disabled_regression
    fi
    matrix_summary
    (( MATRIX_FAILED == 0 ))
}

main "$@"
