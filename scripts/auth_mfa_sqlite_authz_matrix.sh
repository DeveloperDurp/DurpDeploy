#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$SCRIPT_DIR/auth_mfa_http_fixture.sh"
source "$SCRIPT_DIR/auth_mfa_sqlite_authz_scenarios.sh"

[[ ${DURPDEPLOY_AUTH_MFA_HTTP_MATRIX_CLIENT_ONLY:-} == 1 ]] || {
    echo "auth/MFA authorization matrix: use scripts/e2e_test.sh as lifecycle owner" >&2
    exit 2
}
for name in DURPDEPLOY_BASE_URL DURPDEPLOY_AUTH_MFA_DB DURPDEPLOY_AUTH_MFA_SERVER_LOG DURPDEPLOY_AUTH_MFA_ADMIN_EMAIL DURPDEPLOY_AUTH_MFA_ADMIN_PASSWORD; do
    [[ -n ${!name:-} ]] || { echo "auth/MFA authorization matrix: missing $name" >&2; exit 2; }
done
command -v sqlite3 >/dev/null 2>&1 || { echo "auth/MFA authorization matrix: sqlite3 is required" >&2; exit 2; }

passed=0
failed=0
run() {
    local id=$1
    set +e
    ( set -e; trap 'auth_mfa_fixture_cleanup || true' EXIT; "$2" )
    if (( $? == 0 )); then
        passed=$((passed + 1)); printf '{"scenario_id":"%s","engine":"sqlite","result":"pass"}\n' "$id"
    else
        failed=$((failed + 1)); printf '{"scenario_id":"%s","engine":"sqlite","result":"fail"}\n' "$id"
    fi
    set -e
}

run csrf-viewer-role-write-rejection matrix_csrf_viewer_role_write_rejection
run api-token-machine-credential-separation matrix_api_token_machine_credential_separation
printf '{"summary":{"engine":"sqlite","passed":%d,"failed":%d}}\n' "$passed" "$failed"
(( failed == 0 ))
