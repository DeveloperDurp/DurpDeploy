#!/usr/bin/env bash
set -euo pipefail

engine=${1:-}
required=${DURPDEPLOY_AGENT_RUNTIME_PARITY_REQUIRED:-0}
artifact_root=${AGENT_RUNTIME_ARTIFACT_DIR:-artifacts/agent-runtime}
tmp=

cleanup() {
    local exit_code=$?
    [[ -z $tmp ]] || rm -rf -- "$tmp"
    return "$exit_code"
}
trap cleanup EXIT INT TERM

result() {
    printf '{"engine":"%s","suite":"remote-agent-runtime-parity","result":"%s"}\n' "$engine" "$1"
}

redact() {
    sed -E \
        -e 's#(sqlserver://[^:]+:)[^@]+@#\1<redacted>@#g' \
        -e 's#(postgres(ql)?://[^:]+:)[^@]+@#\1<redacted>@#g' \
        -e 's/(password|token|secret|session|credential)=?[^[:space:],]*/\1=<redacted>/gi'
}

failure_artifact() {
    local directory="$artifact_root/database-$engine"
    rm -rf -- "$directory"
    mkdir -p -- "$directory"
    chmod 700 "$directory"
    redact < "$tmp/test.log" > "$directory/test.redacted.log"
    printf 'engine=%s\ncommand=go test (redacted arguments)\ncleanup=complete\n' "$engine" > "$directory/summary.txt"
}

success_artifact() {
    local directory="$artifact_root/task-10-$engine"
    rm -rf -- "$directory"
    mkdir -p -- "$directory"
    chmod 700 "$directory"
    redact < "$tmp/test.log" > "$directory/test.redacted.log"
    printf 'engine=%s\nsuite=remote-agent-runtime-parity\nresult=pass\ncleanup=complete\n' \
        "$engine" > "$directory/summary.txt"
}

case "$engine" in
    postgres) test='^TestPostgres_RemoteAgentRuntimeParity$' ;;
    mssql) test='^TestMSSQL_RemoteAgentRuntimeParity$' ;;
    *) echo "usage: $0 {postgres|mssql}" >&2; exit 2 ;;
esac
test_name=${test#^}
test_name=${test_name%\$}

for command in go docker mktemp; do
    if ! command -v "$command" >/dev/null 2>&1; then
        result skip
        [[ $required == 1 ]] && exit 2
        exit 0
    fi
done
if ! docker info >/dev/null 2>&1; then
    result skip
    [[ $required == 1 ]] && exit 2
    exit 0
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-agent-runtime-${engine}.XXXXXX")
if go test -v -count=1 -run "$test" ./internal/agentserver > "$tmp/test.log" 2>&1; then
    if ! grep -q "=== RUN   $test_name" "$tmp/test.log"; then
        failure_artifact
        redact < "$tmp/test.log" >&2
        echo "required runtime parity test did not run" >&2
        result fail
        exit 1
    fi
    if grep -q '^--- SKIP:' "$tmp/test.log"; then
        result skip
        if [[ $required == 1 ]]; then
            failure_artifact
            exit 1
        fi
    else
        success_artifact
        result pass
    fi
else
    failure_artifact
    redact < "$tmp/test.log" >&2
    result fail
    exit 1
fi
