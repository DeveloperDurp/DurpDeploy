#!/usr/bin/env bash
# Rebuild only a running demo's server; retain its ports, data and paired agent.
set -euo pipefail
umask 077
ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
[[ $# == 1 && -n "$1" ]] || { echo "Usage: $0 DEMO_DIRECTORY" >&2; exit 2; }
directory=$(CDPATH= cd -- "$1" && pwd)
for tool in go make python3 sqlite3 curl nohup setsid flock; do
    command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }
done
exec 9>"$directory/server-refresh.lock"
flock -n 9 || { echo 'Another server refresh is running.' >&2; exit 1; }
pid=$(cat "$directory/server.pid")
[[ "$pid" =~ ^[0-9]+$ ]] || { echo 'Invalid server PID.' >&2; exit 1; }
check_server() {
    [[ $(ps -p "$pid" -o args= || true) == "$directory/bin/durpdeploy" ]] || {
        echo 'Demo server is not running, or its PID belongs to another process.' >&2
        return 1
    }
}
check_idle() {
    [[ $(sqlite3 -readonly -cmd '.timeout 5000' "$directory/durpdeploy.db" \
        "SELECT (SELECT count(*) FROM deployments d
          WHERE status IN ('pending','running','publishing_artifact') OR
          (status='cleanup_unconfirmed' AND (container_namespace IS NOT NULL OR
            (cleanup_confirmed_at IS NULL AND
             NOT EXISTS (SELECT 1 FROM remote_step_runs s
               WHERE s.deployment_id=d.id AND s.state='cleanup_unconfirmed') AND
             NOT EXISTS (SELECT 1 FROM remote_deployment_claims c
               WHERE c.deployment_id=d.id AND c.state='cleanup_unconfirmed')))))
        + (SELECT count(*) FROM remote_step_runs
          WHERE state IN ('waiting','claimed','started','cancel_requested','lost','cancel_unconfirmed')
          OR (state='cleanup_unconfirmed' AND cleanup_confirmed_at IS NULL))
        + (SELECT count(*) FROM remote_deployment_claims
          WHERE state IN ('waiting','claimed','started','cancel_requested','lost','cancel_unconfirmed')
          OR (state='cleanup_unconfirmed' AND cleanup_confirmed_at IS NULL));") == 0 ]] || {
        echo 'Finish/cancel deployments and confirm agent cleanup before refreshing.' >&2
        return 1
    }
}
check_server
check_idle

# Read settings from the exact running process, not the caller's environment.
# NUL-separated data is never evaluated as shell code or printed to logs.
python3 -c 'import pathlib, sys
source = pathlib.Path("/proc") / sys.argv[1] / "environ"
settings = [entry for entry in source.read_bytes().split(b"\0")
            if entry.startswith(b"DURPDEPLOY_") or
            entry.partition(b"=")[0] in {b"DOCKER_HOST", b"CONTAINER_HOST"}]
if not settings:
    sys.exit("No demo server configuration found")
pathlib.Path(sys.argv[2]).write_bytes(b"\0".join(settings) + b"\0")' \
    "$pid" "$directory/server.env"
address=$(python3 -c 'import json, sys
with open(sys.argv[1]) as log:
    addresses = [json.loads(line)["addr"] for line in log
                 if "\"msg\":\"server starting\"" in line]
if not addresses:
    sys.exit("Demo listening address not found")
print(addresses[-1])' "$directory/server.log")

printf 'Building server in place. Logs: %s/server-refresh-build.log\n' "$directory"
cd "$ROOT"
make --no-print-directory templ-generate swagger-ui-copy tailwind-build js-build \
    >"$directory/server-refresh-build.log" 2>&1
go build -o "$directory/bin/durpdeploy.next" ./cmd/server \
    >>"$directory/server-refresh-build.log" 2>&1
check_server

for variable in ${!DURPDEPLOY_@}; do unset "$variable"; done
unset DOCKER_HOST CONTAINER_HOST
while IFS= read -r -d '' setting; do
    [[ "${setting%%=*}" =~ ^(DURPDEPLOY_[A-Z0-9_]+|DOCKER_HOST|CONTAINER_HOST)$ ]] || {
        echo 'Invalid saved server configuration.' >&2; exit 1;
    }
    export "$setting"
done <"$directory/server.env"
export DURPDEPLOY_ADDR="$address" TMPDIR="$directory/tmp"

# All deployment admission and agent claims write SQLite before execution.
# Hold its writer lock from the final idle check until the old server exits.
release_write_barrier() {
    if [[ -n ${barrier_pid:-} ]]; then
        printf 'ROLLBACK;\n.quit\n' >&"$barrier_input" || true
        wait "$barrier_pid" || true
        barrier_pid=
    fi
}
trap release_write_barrier EXIT
acquire_write_barrier() {
    coproc DEMO_WRITE_BARRIER {
        exec 9>&-
        exec sqlite3 -bail -cmd '.timeout 5000' "$directory/durpdeploy.db"
    }
    barrier_pid=$DEMO_WRITE_BARRIER_PID
    barrier_input=${DEMO_WRITE_BARRIER[1]}
    printf "BEGIN IMMEDIATE;\nSELECT 'refresh-locked';\n" >&"$barrier_input"
    if ! IFS= read -r -t 6 barrier_state <&"${DEMO_WRITE_BARRIER[0]}" || \
        [[ "$barrier_state" != refresh-locked ]]; then
        echo 'Could not close deployment admission; server left running.' >&2
        return 1
    fi
}
stop_server() {
    command_line=$(ps -p "$pid" -o args= || true)
    [[ -z "$command_line" || "$command_line" == *'<defunct>'* ]] && return 0
    check_server || return 1
    kill "$pid"
    for _ in {1..150}; do
        command_line=$(ps -p "$pid" -o args= || true)
        [[ -z "$command_line" || "$command_line" == *'<defunct>'* ]] && return 0
        sleep 0.1
    done
    echo 'Server did not stop; check server.log. Agent and proxy were left running.' >&2
    return 1
}
start_server() {
    (
        cd "$directory"
        exec 9>&-
        exec nohup setsid "$directory/bin/durpdeploy"
    ) >"$directory/server.log" 2>&1 </dev/null &
    pid=$!
    printf '%s\n' "$pid" >"$directory/server.pid"
}
wait_ready() {
    local deadline=$((SECONDS + 15))
    while ((SECONDS < deadline)); do
        kill -0 "$pid" 2>/dev/null || return 1
        if grep -q '"msg":"server starting"' "$directory/server.log" && \
            curl --cacert "$directory/tls/cert.pem" -fsS --max-time 2 "$url/healthz" >/dev/null 2>&1 && \
            check_server; then
            return 0
        fi
        sleep 0.1
    done
    return 1
}
acquire_write_barrier
check_idle
stop_server
release_write_barrier
mv "$directory/bin/durpdeploy" "$directory/bin/durpdeploy.previous"
mv "$directory/bin/durpdeploy.next" "$directory/bin/durpdeploy"
mv "$directory/server.log" "$directory/server-before-refresh.log"
url=$(cat "$directory/url")
start_server
if wait_ready; then
    printf 'Demo server refreshed: %s\n' "$url"
    cat "$directory/login.txt"
    printf 'Database, paired agent, HTTPS proxy and certificate retained: %s\n' "$directory"
    exit 0
fi
echo 'Replacement failed readiness; restoring the previous server.' >&2
acquire_write_barrier
check_idle
stop_server
release_write_barrier
mv "$directory/bin/durpdeploy" "$directory/bin/durpdeploy.failed"
mv "$directory/bin/durpdeploy.previous" "$directory/bin/durpdeploy"
mv "$directory/server.log" "$directory/server-refresh-failed.log"
start_server
if wait_ready; then
    echo "Server refresh failed; previous server restored. Inspect $directory/server-refresh-failed.log." >&2
else
    echo "Server rollback failed; inspect $directory/server.log. Data and binaries retained." >&2
fi
exit 1
