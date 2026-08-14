#!/usr/bin/env bash
set -euo pipefail

engine=${1:-}
required=${DURPDEPLOY_AUTH_MFA_PARITY_REQUIRED:-0}
artifact_root=${AUTH_MFA_ARTIFACT_DIR:-artifacts/auth-mfa}
tmp=

cleanup() {
    [[ -n $tmp ]] && rm -rf -- "$tmp"
}
trap cleanup EXIT INT TERM

result() {
    local state=$1 detail=${2:-}

    printf '{"engine":"%s","suite":"auth-mfa-database-parity","result":"%s"' \
        "$engine" "$state"
    if [[ -n $detail ]]; then
        printf ',"detail":"%s"' "$detail"
    fi
    printf '}\n'
}

unavailable() {
    local detail=$1

    result skip "$detail"
    if [[ $required == 1 ]]; then
        echo "FAIL: [$engine] parity is required but unavailable: $detail" >&2
        exit 2
    fi
    exit 0
}

redact() {
    sed -E \
        -e 's#(sqlserver://[^:]+:)[^@]+@#\1<redacted>@#g' \
        -e 's#(postgres(ql)?://[^:]+:)[^@]+@#\1<redacted>@#g' \
        -e 's/(ddp_pat_)[A-Za-z0-9_-]+/\1<redacted>/g' \
        -e 's/(password|token|secret|session|challenge|recovery|credential)=?[^[:space:],]*/\1=<redacted>/gi'
}

failure_artifact() {
    local directory="$artifact_root/database-$engine"

    rm -rf -- "$directory"
    mkdir -p -- "$directory"
    chmod 700 "$directory"
    redact < "$tmp/test.log" > "$directory/test.redacted.log"
    printf 'engine=%s\ncommand=go test (redacted arguments)\ncleanup=complete\n' \
        "$engine" > "$directory/summary.txt"
}

run() {
    local name=$1
    shift

    printf '[%s] %s\n' "$engine" "$name"
    if "$@" > "$tmp/test.log" 2>&1; then
        if grep -q '^--- SKIP:' "$tmp/test.log"; then
            result skip "$name"
        else
            result pass "$name"
        fi
        return 0
    fi
    failure_artifact
    redact < "$tmp/test.log" >&2
    result fail "$name"
    return 1
}

case "$engine" in
postgres)
    tests='^(TestPostgres_MigrationsRun|TestPostgres_WebAuthnCredentialCRUD|TestPostgres_ChallengeGuardedConsume|TestPostgres_RepositoryWithTx)$'
    packages=(./internal/migrate ./internal/mfa ./internal/repository)
    ;;
mssql)
    tests='^(TestMSSQL_MFASchemaParity|TestMSSQL_MFASchemaPreservesBinaryFieldsAndRollsBack|TestSQLServer_SchemaParityDefaultsAndIndexes|TestSQLServer_SourceQueryRewritesPersistResults|TestSQLServer_FinalWaveRuntimeParity|TestSQLServer_MigrationsAndQueries)$'
    packages=(./internal/migrate)
    ;;
*)
    echo "usage: $0 {postgres|mssql}" >&2
    exit 2
    ;;
esac

command -v go >/dev/null 2>&1 || unavailable go-not-found
command -v docker >/dev/null 2>&1 || unavailable docker-not-found
docker info >/dev/null 2>&1 || unavailable docker-daemon-unavailable
command -v mktemp >/dev/null 2>&1 || unavailable mktemp-not-found
tmp=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-auth-mfa-parity-${engine}.XXXXXX")

if [[ ${DURPDEPLOY_AUTH_MFA_PARITY_FORCE_FAILURE:-0} == 1 ]]; then
    run forced-parity-assertion-mismatch sh -c \
        'printf "password=forced-parity-secret\n" >&2; exit 1'
    exit 1
fi

run container-parity go test -v -count=1 -run "$tests" "${packages[@]}"

if [[ $engine == mssql ]]; then
    if [[ -z ${DURPDEPLOY_MSSQL_TEST_DSN:-} ]]; then
        result skip mssql-dsn-not-configured
    else
        run configured-dsn-parity env \
            DURPDEPLOY_MSSQL_TEST_DSN="$DURPDEPLOY_MSSQL_TEST_DSN" \
            go test -v -count=1 -run '^TestMSSQL_ChallengeGuardedConsume$' \
            ./internal/mfa
    fi
fi
