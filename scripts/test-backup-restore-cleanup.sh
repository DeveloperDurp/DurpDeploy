#!/usr/bin/env bash
# Exercise the shell runner's cleanup while a real restored-app fixture runs.
set -euo pipefail
cd "$(dirname "$0")/.."
export DURPDEPLOY_RESTORE_BACKEND=${DURPDEPLOY_RESTORE_BACKEND:-sqlite}
case $DURPDEPLOY_RESTORE_BACKEND in
    sqlite) test_timeout=2m; setup_seconds=240 ;;
    postgres|mssql) test_timeout=4m; setup_seconds=540 ;;
    *) echo 'ERROR: set DURPDEPLOY_RESTORE_BACKEND to sqlite, postgres, or mssql.' >&2; exit 1 ;;
esac
if [[ -n ${DURPDEPLOY_CONTAINER_URL:-} ]]; then
    export DOCKER_HOST=$DURPDEPLOY_CONTAINER_URL
fi

runtime=${DURPDEPLOY_CONTAINER_RUNTIME:-}
case $runtime in
    docker|podman) ;;
    *) echo 'ERROR: set DURPDEPLOY_CONTAINER_RUNTIME to docker or podman.' >&2; exit 1 ;;
esac
if [[ $runtime == podman && -z ${DURPDEPLOY_CONTAINER_URL:-} && -z ${DOCKER_HOST:-} ]]; then
    echo 'ERROR: select the Podman socket with DURPDEPLOY_CONTAINER_URL or DOCKER_HOST.' >&2
    exit 1
fi
work_dir=$(mktemp -d)
runner_pid=
unrelated=
runtime_args=()
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

# Discover once through the same SDK used by the runner, including its defaults.
provider_host=$(go run scripts/go_test_cleanup.go --host)
export DOCKER_HOST=$provider_host DURPDEPLOY_CONTAINER_URL=$provider_host
runtime_args=("--host=$provider_host")
if [[ $runtime == podman ]]; then
    runtime_args=(--remote "--url=$provider_host")
fi

unrelated=$(engine run --detach --network=none \
    --label "io.durpdeploy.namespace=restore-unrelated-${work_dir##*/}" \
    harbor.durp.info/dockerhub/library/bash:5.2 bash -c 'while :; do sleep 3600; done')

for mode in timeout TERM; do
    log_file="$work_dir/$mode.log"
    setup_deadline=$((SECONDS + setup_seconds))
    DURPDEPLOY_RESTORE_INTERRUPT_FIXTURE=1 \
        DURPDEPLOY_GO_TEST_DIRECTORY="$work_dir" \
        bash scripts/go_test.sh -tags=e2e,backuprestore -race -count=1 \
        "-timeout=$test_timeout" -v -run '^TestBackupRestoreInterruptFixture$' \
        ./internal/handler/api >"$log_file" 2>&1 &
    runner_pid=$!
    ready=
    # This shell deadline also covers compilation before Go starts its timer.
    # The fixture requires 30 seconds of native timeout headroom at readiness.
    while ((SECONDS < setup_deadline)); do
        ready=$(sed -n 's/.*restore-interrupt-ready pid=\([0-9]*\) namespace=\([^ ]*\) session=\([^ ]*\).*/\1 \2 \3/p' "$log_file")
        if [[ -n $ready ]]; then break; fi
        if ! kill -0 "$runner_pid" 2>/dev/null; then break; fi
        sleep 0.1
    done
    if [[ -z $ready ]]; then
        cat "$log_file" >&2
        echo "ERROR: $mode cleanup fixture did not become ready." >&2
        exit 1
    fi
    read -r server_pid namespace session <<< "$ready"
    grep 'restore-interrupt-timing ' "$log_file"
    database_containers=$(engine ps --all --quiet --filter "label=org.testcontainers.sessionId=$session")
    database_volumes=
    for id in $database_containers; do
        mounts=$(engine inspect --format '{{range .Mounts}}{{if eq .Type "volume"}}{{println .Name}}{{end}}{{end}}' "$id")
        database_volumes+=" $mounts"
    done
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
    remaining=$(engine ps --all --quiet --filter "label=org.testcontainers.sessionId=$session")
    [[ -z $remaining ]]
    available_volumes=$(engine volume ls --format '{{.Name}}')
    for volume in $database_volumes; do
        if grep -Fxq -- "$volume" <<< "$available_volumes"; then
            echo "ERROR: $mode left an owned database volume." >&2
            exit 1
        fi
    done
    remaining=$(find "$work_dir" -maxdepth 1 -type d -name 'durpdeploy-go-test.*' -print)
    [[ -z $remaining ]]
    unrelated_running=$(engine inspect --format '{{.State.Running}}' "$unrelated")
    [[ $unrelated_running == true ]]
    printf 'Backup restore %s cleanup: PASS\n' "$mode"
done
