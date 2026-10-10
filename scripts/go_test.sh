#!/usr/bin/env bash
set -euo pipefail

# Each invocation owns a unique Testcontainers session, including on Podman
# where Ryuk may be disabled. Never prune resources from another test or demo.
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

session_dir=$(mktemp -d \
    "${DURPDEPLOY_GO_TEST_DIRECTORY:-${TMPDIR:-/tmp}}/durpdeploy-go-test.XXXXXXXX")
export DURPDEPLOY_GO_TEST_DIRECTORY=$session_dir
export TESTCONTAINERS_SESSION_ID=${session_dir##*/}
test_pid=
provider_ready=0

cleanup() {
    local status=$? directory child_pid cleanup_status=0
    local -a directories=() sessions=()
    trap - EXIT HUP INT TERM
    if [[ -n $test_pid ]]; then
        # Stop the whole test process group before removing its containers.
        kill -KILL -- "-$test_pid" 2>/dev/null || true
        wait "$test_pid" 2>/dev/null || true
    fi
    # Nested runners live beneath this run's directory. Their process groups
    # and sessions also belong to this run if interruption skips their traps.
    while IFS= read -r -d '' directory; do
        directories+=("$directory")
    done < <(find "$session_dir" -type d -name 'durpdeploy-go-test.*' -print0)
    for directory in "${directories[@]}"; do
        if [[ -f $directory/process-group ]]; then
            read -r child_pid < "$directory/process-group"
            kill -KILL -- "-$child_pid" 2>/dev/null || true
        fi
    done
    for directory in "${directories[@]}"; do
        sessions+=("${directory##*/}")
    done
    if [[ -x $session_dir/cleanup ]]; then
        "$session_dir/cleanup" "${sessions[@]}" >&2 || cleanup_status=$?
        # Unit-only runs can work without a provider. A provider that was
        # reachable before testing must remain reachable for cleanup.
        if ((status == 0 && cleanup_status != 0 && (cleanup_status != 2 || provider_ready))); then
            status=1
        fi
    fi
    rm -rf -- "$session_dir"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

go build -o "$session_dir/cleanup" "$script_dir/go_test_cleanup.go"
if provider_host=$("$session_dir/cleanup" --host); then
    export DOCKER_HOST=$provider_host
    provider_ready=1
fi

# Bash job control gives go and its test binaries their own process group.
set -m
bash -c '
    printf "%s\n" "$$" > "$DURPDEPLOY_GO_TEST_DIRECTORY/process-group.tmp"
    mv "$DURPDEPLOY_GO_TEST_DIRECTORY/process-group.tmp" "$DURPDEPLOY_GO_TEST_DIRECTORY/process-group"
    exec go test "$@"
' bash "$@" &
test_pid=$!
status=0
wait "$test_pid" || status=$?
exit "$status"
