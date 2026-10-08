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
          WHERE state IN ('waiting','claimed','started','cancel_requested','cancel_unconfirmed')
          OR (state='cleanup_unconfirmed' AND cleanup_confirmed_at IS NULL))
        + (SELECT count(*) FROM remote_deployment_claims
          WHERE state IN ('waiting','claimed','started','cancel_requested','cancel_unconfirmed')
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
check_idle

for variable in ${!DURPDEPLOY_@}; do unset "$variable"; done
unset DOCKER_HOST CONTAINER_HOST
while IFS= read -r -d '' setting; do
    [[ "${setting%%=*}" =~ ^(DURPDEPLOY_[A-Z0-9_]+|DOCKER_HOST|CONTAINER_HOST)$ ]] || {
        echo 'Invalid saved server configuration.' >&2; exit 1;
    }
    export "$setting"
done <"$directory/server.env"
export DURPDEPLOY_ADDR="$address" TMPDIR="$directory/tmp"

kill "$pid"
for _ in {1..150}; do
    command_line=$(ps -p "$pid" -o args= || true)
    [[ -z "$command_line" || "$command_line" == *'<defunct>'* ]] && break
    sleep 0.1
done
if [[ -n "$command_line" && "$command_line" != *'<defunct>'* ]]; then
    echo 'Server did not stop; check server.log. Agent and proxy were left running.' >&2
    exit 1
fi
mv "$directory/bin/durpdeploy.next" "$directory/bin/durpdeploy"
mv "$directory/server.log" "$directory/server-before-refresh.log"
(
    cd "$directory"
    exec 9>&-
    exec nohup setsid "$directory/bin/durpdeploy"
) >"$directory/server.log" 2>&1 </dev/null &
pid=$!
printf '%s\n' "$pid" >"$directory/server.pid"
url=$(cat "$directory/url")
for _ in {1..150}; do
    kill -0 "$pid" 2>/dev/null || break
    if grep -q '"msg":"server starting"' "$directory/server.log" && \
        curl --cacert "$directory/tls/cert.pem" -fsS --max-time 2 "$url/healthz" >/dev/null 2>&1; then
        check_server
        printf 'Demo server refreshed: %s\n' "$url"
        cat "$directory/login.txt"
        printf 'Database, paired agent, HTTPS proxy and certificate retained: %s\n' "$directory"
        exit 0
    fi
    sleep 0.1
done
echo "Server refresh failed; inspect $directory/server.log. Agent, proxy and data retained." >&2
exit 1
