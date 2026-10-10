#!/usr/bin/env bash
set -euo pipefail

# Each invocation owns a unique Testcontainers session, including on Podman
# where Ryuk may be disabled. Never prune resources from another test or demo.
engine_path=$(command -v docker || command -v podman || true)
if [[ -z $engine_path ]]; then
    if [[ -n ${DOCKER_HOST:-} ]]; then
        echo 'Go test cleanup requires a Docker or Podman CLI for DOCKER_HOST.' >&2
        exit 1
    fi
    exec go test "$@"
fi
engine=("$engine_path")
if [[ ${engine_path##*/} == podman && -n ${DOCKER_HOST:-} ]]; then
    engine+=(--remote --url "$DOCKER_HOST")
fi

session_dir=$(mktemp -d \
    "${DURPDEPLOY_GO_TEST_DIRECTORY:-${TMPDIR:-/tmp}}/durpdeploy-go-test.XXXXXXXX")
export DURPDEPLOY_GO_TEST_DIRECTORY=$session_dir
export TESTCONTAINERS_SESSION_ID=${session_dir##*/}
test_pid=

owned_containers() {
    "${engine[@]}" ps -aq --filter 'label=org.testcontainers=true' \
        --filter "label=org.testcontainers.sessionId=$1"
}

cleanup() {
    local status=$? ids remaining directory child_pid session
    local -a containers=()
    trap - EXIT HUP INT TERM
    if [[ -n $test_pid ]]; then
        # Stop the whole test process group before removing its containers.
        kill -KILL -- "-$test_pid" 2>/dev/null || true
        wait "$test_pid" 2>/dev/null || true
    fi
    # Nested runners live beneath this run's directory. Their process groups
    # and sessions also belong to this run if interruption skips their traps.
    shopt -s globstar nullglob
    local -a directories=("$session_dir" "$session_dir"/**/durpdeploy-go-test.*)
    for directory in "${directories[@]}"; do
        if [[ -f $directory/process-group ]]; then
            read -r child_pid < "$directory/process-group"
            kill -KILL -- "-$child_pid" 2>/dev/null || true
        fi
    done
    for directory in "${directories[@]}"; do
        session=${directory##*/}
        if ids=$(owned_containers "$session"); then
            if [[ -n $ids ]]; then
                read -r -a containers <<< "${ids//$'\n'/ }"
                if ! "${engine[@]}" rm -f -v "${containers[@]}" >&2; then
                    # Ryuk may have removed them after the listing.
                    if ! remaining=$(owned_containers "$session") || [[ -n $remaining ]]; then
                        echo 'Failed to remove containers owned by this test run.' >&2
                        if ((status == 0)); then status=1; fi
                    fi
                fi
            fi
        else
            echo 'Failed to list containers owned by this test run.' >&2
            if ((status == 0)); then status=1; fi
        fi
    done
    rm -rf -- "$session_dir"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# Bash job control gives go and its test binaries their own process group.
set -m
(
    printf '%s\n' "$BASHPID" > "$session_dir/process-group.tmp"
    mv "$session_dir/process-group.tmp" "$session_dir/process-group"
    exec go test "$@"
) &
test_pid=$!
status=0
wait "$test_pid" || status=$?
exit "$status"
