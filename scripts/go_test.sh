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
    local status=$? directory child_pid session changed cleanup_status=0
    local known_sessions=' ' known_groups=" $test_pid "
    local -a sessions=()
    trap - EXIT HUP INT TERM
    if [[ -n $test_pid ]]; then
        # Stop the whole test process group before removing its containers.
        kill -KILL -- "-$test_pid" 2>/dev/null || true
        wait "$test_pid" 2>/dev/null || true
    fi
    # Nested runners live beneath this run's directory. Their process groups
    # and sessions also belong to this run if interruption skips their traps.
    # A detached nested group can create another runner during discovery.
    # Rescan after stopping new groups until no unprocessed sessions/groups remain.
    while :; do
        changed=0
        while IFS= read -r -d '' directory; do
            session=${directory##*/}
            case $known_sessions in
            *" $session "*) ;;
            *)
                sessions+=("$session")
                known_sessions+="$session "
                changed=1
                ;;
            esac
            if [[ -f $directory/process-group ]] && \
                read -r child_pid < "$directory/process-group"; then
                case $known_groups in
                *" $child_pid "*) ;;
                *)
                    known_groups+="$child_pid "
                    changed=1
                    kill -KILL -- "-$child_pid" 2>/dev/null || true
                    ;;
                esac
            fi
        done < <(find "$session_dir" -type d -name 'durpdeploy-go-test.*' -print0)
        if ((changed == 0)); then break; fi
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
    runner_pid=$1
    shift
    attempts=0
    # Tests cannot start until the parent has published this detached group.
    # If it dies before registration, this child exits without creating resources.
    while [[ ! -f $DURPDEPLOY_GO_TEST_DIRECTORY/process-group ]]; do
        if ! kill -0 "$runner_pid" 2>/dev/null || ((attempts >= 100)); then exit 1; fi
        sleep 0.05
        attempts=$((attempts + 1))
    done
    exec go test "$@"
' bash "$$" "$@" &
test_pid=$!
printf '%s\n' "$test_pid" > "$session_dir/process-group.tmp"
mv "$session_dir/process-group.tmp" "$session_dir/process-group"
status=0
wait "$test_pid" || status=$?
exit "$status"
