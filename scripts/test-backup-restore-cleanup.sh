#!/usr/bin/env bash
# Exercise the shell runner's cleanup while a real restored-app fixture runs.
set -euo pipefail
cd "$(dirname "$0")/.."

work_dir=$(mktemp -d)
runner_pid=
unrelated=
runtime=${DURPDEPLOY_CONTAINER_RUNTIME:-}
if [[ -z $runtime ]]; then
    runtime=docker
    if command -v podman >/dev/null 2>&1; then runtime=podman; fi
fi
runtime_args=()
if [[ -n ${DURPDEPLOY_CONTAINER_URL:-} ]]; then
    runtime_args=("--host=$DURPDEPLOY_CONTAINER_URL")
    if [[ $runtime == podman ]]; then
        runtime_args=(--remote "--url=$DURPDEPLOY_CONTAINER_URL")
    fi
fi
engine() { "$runtime" "${runtime_args[@]}" "$@"; }
cleanup() {
    local status=$?
    trap - EXIT HUP INT TERM
    if [[ -n $runner_pid ]]; then
        kill -TERM "$runner_pid" 2>/dev/null || true
        wait "$runner_pid" 2>/dev/null || true
    fi
    if [[ -n $unrelated ]]; then engine rm --force "$unrelated" >/dev/null; fi
    rm -rf -- "$work_dir"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

unrelated=$(engine run --detach --network=none \
    --label "io.durpdeploy.namespace=restore-unrelated-${work_dir##*/}" \
    docker.io/library/bash:5.2 sleep 300)

for mode in timeout TERM; do
    log_file="$work_dir/$mode.log"
    DURPDEPLOY_RESTORE_INTERRUPT_FIXTURE=1 \
        DURPDEPLOY_GO_TEST_DIRECTORY="$work_dir" \
        bash scripts/go_test.sh -tags=e2e,backuprestore -race -count=1 \
        -timeout=45s -v -run '^TestBackupRestoreInterruptFixture$' \
        ./internal/handler/api >"$log_file" 2>&1 &
    runner_pid=$!
    ready=
    for _ in $(seq 1 900); do
        ready=$(sed -n 's/.*restore-interrupt-ready pid=\([0-9]*\) namespace=\([^ ]*\).*/\1 \2/p' "$log_file")
        if [[ -n $ready ]]; then break; fi
        if ! kill -0 "$runner_pid" 2>/dev/null; then break; fi
        sleep 0.1
    done
    if [[ -z $ready ]]; then
        cat "$log_file" >&2
        echo "ERROR: $mode cleanup fixture did not become ready." >&2
        exit 1
    fi
    read -r server_pid namespace <<< "$ready"
    if [[ $mode == TERM ]]; then kill -TERM "$runner_pid"; fi
    status=0
    wait "$runner_pid" || status=$?
    runner_pid=
    if [[ $mode == timeout ]]; then
        if [[ $status == 0 ]] || ! grep -q 'test timed out' "$log_file"; then
            echo 'ERROR: the fixture did not reach the expected test timeout.' >&2
            exit 1
        fi
    else
        [[ $status == 143 ]]
    fi
    # A killed process can briefly remain a zombie until its new parent reaps it.
    state=$(ps -o stat= -p "$server_pid" || true)
    if [[ -n $state && $state != Z* ]]; then
        echo "ERROR: $mode left the application process running." >&2
        exit 1
    fi
    remaining=$(engine ps --all --quiet --filter "label=io.durpdeploy.namespace=$namespace")
    [[ -z $remaining ]]
    remaining=$(engine volume ls --quiet --filter "label=io.durpdeploy.namespace=$namespace")
    [[ -z $remaining ]]
    remaining=$(find "$work_dir" -maxdepth 1 -type d -name 'durpdeploy-go-test.*' -print)
    [[ -z $remaining ]]
    unrelated_running=$(engine inspect --format '{{.State.Running}}' "$unrelated")
    [[ $unrelated_running == true ]]
    printf 'Backup restore %s cleanup: PASS\n' "$mode"
done
