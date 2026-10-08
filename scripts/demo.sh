#!/usr/bin/env bash
# Local, persistent demo for coding agents. Each invocation owns its resources.
set -euo pipefail
umask 077

ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)

stop_demo() {
    local directory=$1 engine container pid command_line proxy
    directory=$(CDPATH= cd -- "$directory" && pwd)
    engine=$(cat "$directory/runtime")
    container=$(cat "$directory/container")
    pid=$(cat "$directory/server.pid")
    [[ "$container" =~ ^durpdeploy-demo-[A-Za-z0-9]+$ && "$pid" =~ ^[0-9]+$ ]] || {
        echo 'Invalid demo metadata; nothing stopped.' >&2
        return 1
    }
    command_line=$(ps -p "$pid" -o args= || true)
    if [[ -n "$command_line" && "$command_line" != "$directory/bin/durpdeploy" && "$command_line" != *'<defunct>'* ]]; then
        echo 'Server PID belongs to another process; nothing stopped.' >&2
        return 1
    fi
    "$engine" info >/dev/null
    proxy="$container-https"
    if "$engine" container inspect "$proxy" >/dev/null 2>&1; then
        "$engine" stop "$proxy" >/dev/null
    fi
    if "$engine" container inspect "$container" >/dev/null 2>&1; then
        "$engine" stop "$container" >/dev/null
    fi
    if [[ "$command_line" == "$directory/bin/durpdeploy" ]]; then
        kill "$pid"
        for _ in {1..150}; do
            command_line=$(ps -p "$pid" -o args= || true)
            [[ -z "$command_line" || "$command_line" == *'<defunct>'* ]] && break
            sleep 0.1
        done
        if [[ -n "$command_line" && "$command_line" != *'<defunct>'* ]]; then
            echo 'Server did not stop; check server.log.' >&2
            return 1
        fi
    fi
    printf 'Demo stopped. Database, identity and logs retained in %s\n' "$directory"
}

case "${1:-start}" in
    stop)
        [[ $# == 2 && -n "$2" ]] || { echo "Usage: $0 stop DEMO_DIRECTORY" >&2; exit 2; }
        stop_demo "$2"
        exit
        ;;
    start) [[ $# -le 1 ]] || { echo "Usage: $0 [start]" >&2; exit 2; } ;;
    -h|--help)
        echo "Usage: $0 [start] | stop DEMO_DIRECTORY"
        echo 'Starts an isolated populated demo; prints its URL, login and stop command.'
        exit
        ;;
    *) echo "Usage: $0 [start] | stop DEMO_DIRECTORY" >&2; exit 2 ;;
esac

for tool in go make npm openssl python3 sqlite3 curl nohup setsid; do
    command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }
done
runtime=${DURPDEPLOY_CONTAINER_RUNTIME:-}
if [[ -z "$runtime" ]]; then
    for candidate in podman docker; do
        if command -v "$candidate" >/dev/null && "$candidate" info >/dev/null 2>&1; then
            runtime=$candidate
            break
        fi
    done
fi
[[ "$runtime" == podman || "$runtime" == docker ]] || {
    echo 'A working local Podman or Docker runtime is required.' >&2
    exit 1
}
engine=$(command -v "$runtime")
"$engine" info >/dev/null

# Never inherit a production database, encryption key, identity or SSO settings.
for variable in ${!DURPDEPLOY_@}; do unset "$variable"; done
mkdir -p "$ROOT/tmp"
directory=$(mktemp -d "$ROOT/tmp/demo.XXXXXX")
container="durpdeploy-demo-${directory##*.}"
proxy="$container-https"
mkdir "$directory/bin"
mkdir "$directory/tmp"
export TMPDIR="$directory/tmp"
printf '%s\n' "$engine" >"$directory/runtime"
printf '%s\n' "$container" >"$directory/container"
server_pid=
agent_launcher_pid=
e2e_pid=
complete=0
cleanup() {
    local status=$?
    if [[ "$complete" == 0 ]]; then
        for pid in "$e2e_pid" "$agent_launcher_pid"; do
            if [[ -n "$pid" ]]; then
                kill "$pid" 2>/dev/null || true
                wait "$pid" 2>/dev/null || true
            fi
        done
        "$engine" stop "$container" >/dev/null 2>&1 || true
        "$engine" stop "$proxy" >/dev/null 2>&1 || true
        if [[ -n "$server_pid" ]]; then
            kill "$server_pid" 2>/dev/null || true
            wait "$server_pid" 2>/dev/null || true
        fi
        if [[ -f "$directory/durpdeploy.db" ]]; then
            sqlite3 -cmd '.timeout 5000' "$directory/durpdeploy.db" \
                'UPDATE scheduled_deployments SET enabled=0;' \
                >>"$directory/bootstrap.log" 2>&1 || \
                echo 'Could not disable demo schedules; inspect bootstrap.log.' >&2
        fi
        printf 'Demo setup failed; stopped its resources. Logs/data: %s\n' "$directory" >&2
    fi
    return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'Building demo server. Logs: %s\n' "$directory"
cd "$ROOT"
make --no-print-directory templ-generate swagger-ui-copy >"$directory/build.log" 2>&1
go build -o "$directory/bin/durpdeploy" ./cmd/server >>"$directory/build.log" 2>&1
# Serialize writes before transactional reads so queue maintenance cannot
# invalidate the pairing transaction's SQLite snapshot during startup.
export DURPDEPLOY_DB="$directory/durpdeploy.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
export DURPDEPLOY_SECRET_KEY
DURPDEPLOY_SECRET_KEY=$(openssl rand -base64 32)
printf '%s\n' "$DURPDEPLOY_SECRET_KEY" >"$directory/secret-key"
password="demo-$(openssl rand -hex 4)"
printf 'Email: admin@durp.info\nPassword: %s\n' "$password" >"$directory/login.txt"
"$directory/bin/durpdeploy" admin create --email admin@durp.info \
    --password "$password" >"$directory/bootstrap.log" 2>&1

# Caddy owns the kernel-assigned HTTPS port before the app starts, so secure
# cookies and WebAuthn use the exact public origin from the first request.
mkdir "$directory/tls"
openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 7 \
    -subj '/CN=citadel.durp.loc' -addext 'subjectAltName=DNS:citadel.durp.loc' \
    -keyout "$directory/tls/key.pem" -out "$directory/tls/cert.pem" \
    >>"$directory/bootstrap.log" 2>&1
printf ':8443 {\n tls /demo/cert.pem /demo/key.pem\n respond "Demo starting" 503\n}\n' \
    >"$directory/tls/Caddyfile"
network_args=(--add-host=host.containers.internal:host-gateway)
http_host=127.0.0.1
if [[ "$runtime" == podman ]]; then
    if command -v slirp4netns >/dev/null; then
        network_args=(--network=slirp4netns:allow_host_loopback=true)
    else
        network_args=(--network=pasta:--map-host-loopback,169.254.1.2)
    fi
else
    # Keep the plaintext backend on Docker's bridge, outside the LAN listener.
    http_host=$("$engine" network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}')
fi
"$engine" run -d --rm --name "$proxy" "${network_args[@]}" \
    --read-only --cap-drop=ALL --cap-add=NET_BIND_SERVICE \
    --security-opt=no-new-privileges:true \
    --tmpfs /data:size=64m --tmpfs /config:size=64m --tmpfs /tmp:size=64m \
    --publish :8443 --volume "$directory/tls:/demo:ro,Z" \
    docker.io/library/caddy:2-alpine \
    caddy run --config /demo/Caddyfile --adapter caddyfile \
    >"$directory/proxy.log" 2>&1
https_address=$("$engine" port "$proxy" 8443/tcp | head -1)
export DURPDEPLOY_URL="https://citadel.durp.loc:${https_address##*:}"

# HTTP and the container-published agent port use kernel-assigned ports. The
# advertised TLS pull endpoint needs its port before launch; retry a bind race.
export DURPDEPLOY_CONTAINER_RUNTIME="$runtime"
export DURPDEPLOY_CONTAINER_NAMESPACE="$container"
export DURPDEPLOY_EXECUTION_BOUNDARY=service
export DURPDEPLOY_AGENT_IDENTITY_DIR="$directory/server-identity"
export DURPDEPLOY_ADDR="$http_host:0"
for attempt in {1..5}; do
    control_port=$(python3 -c 'import socket
with socket.socket() as listener:
    listener.bind(("0.0.0.0", 0))
    print(listener.getsockname()[1])')
    export DURPDEPLOY_AGENT_LISTEN_ADDR="0.0.0.0:$control_port"
    export DURPDEPLOY_AGENT_PUBLIC_URL="https://host.containers.internal:$control_port"
    (
        cd "$directory"
        exec nohup setsid "$directory/bin/durpdeploy"
    ) >"$directory/server.log" 2>&1 </dev/null &
    server_pid=$!
    for _ in {1..150}; do
        if grep -q '"msg":"server starting"' "$directory/server.log"; then break; fi
        kill -0 "$server_pid" 2>/dev/null || break
        sleep 0.1
    done
    if kill -0 "$server_pid" 2>/dev/null && grep -q '"msg":"server starting"' "$directory/server.log"; then
        break
    fi
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
    server_pid=
    grep -q 'bind: address already in use' "$directory/server.log" || exit 1
done
[[ -n "$server_pid" ]] || { echo 'Could not allocate demo ports.' >&2; exit 1; }
printf '%s\n' "$server_pid" >"$directory/server.pid"
address=$(python3 -c 'import json,sys
with open(sys.argv[1]) as log:
    print(next(json.loads(line)["addr"] for line in log if "\"msg\":\"server starting\"" in line))' "$directory/server.log")
http_port=${address##*:}
printf ':8443 {\n tls /demo/cert.pem /demo/key.pem\n reverse_proxy host.containers.internal:%s\n}\n' \
    "$http_port" >"$directory/tls/Caddyfile"
"$engine" exec "$proxy" caddy reload --config /demo/Caddyfile --adapter caddyfile \
    >>"$directory/proxy.log" 2>&1
export DURPDEPLOY_BASE_URL="$DURPDEPLOY_URL"
printf '%s\n' "$DURPDEPLOY_BASE_URL" >"$directory/url"
curl --cacert "$directory/tls/cert.pem" -fsS --max-time 5 \
    "$DURPDEPLOY_BASE_URL/healthz" >/dev/null

# Optional one-time administrator setup enables passwordless expiring access.
if [[ -x /usr/local/libexec/durpdeploy-demo-firewall ]]; then
    nohup setsid bash "$ROOT/scripts/demo-firewall-watch.sh" "$directory" \
        >"$directory/firewall.log" 2>&1 </dev/null &
    firewall_pid=$!
    for _ in {1..100}; do
        [[ -f "$directory/firewall.ready" ]] && break
        kill -0 "$firewall_pid" 2>/dev/null || break
        sleep 0.1
    done
    [[ -f "$directory/firewall.ready" ]] || {
        echo 'Temporary firewall access failed; inspect firewall.log.' >&2
        exit 1
    }
fi

printf 'Starting and pairing demo agent.\n'
if [[ "$runtime" == podman ]]; then
    agent_socket=$("$engine" info --format '{{.Host.RemoteSocket.Path}}')
else
    agent_socket=${DOCKER_HOST:-$("$engine" context inspect --format '{{.Endpoints.docker.Host}}')}
    [[ "$agent_socket" == unix:///* ]] || {
        echo 'Demo agent containers require a local Docker Unix socket.' >&2
        exit 1
    }
    agent_socket=${agent_socket#unix://}
fi
[[ "$agent_socket" == /* && -S "$agent_socket" ]] || {
    echo 'Start the selected local runtime socket before make demo (Podman: systemctl --user start podman.socket).' >&2
    exit 1
}
printf '%s\n' "$agent_socket" >"$directory/agent.runtime.socket"
nohup setsid make --no-print-directory -C "$ROOT" dev-agent \
    DEV_CONTAINER_ENGINE="$engine" DEV_AGENT_CONTAINER="$container" \
    DEV_AGENT_STATE_VOLUME="$container-state" DEV_AGENT_PORT= \
    DEV_AGENT_RUNTIME_SOCKET="$agent_socket" \
    >"$directory/agent.log" 2>&1 </dev/null &
agent_launcher_pid=$!
for _ in {1..600}; do
    if grep -q '^Agent fingerprint: ' "$directory/agent.log"; then break; fi
    kill -0 "$agent_launcher_pid" 2>/dev/null || break
    sleep 0.2
done
grep -q '^Agent fingerprint: ' "$directory/agent.log" || {
    echo 'Agent did not publish a pairing offer; see agent.log.' >&2
    exit 1
}
agent_address=$("$engine" port "$container" 10943/tcp)
code=$(sed -n 's/^Pairing code: //p' "$directory/agent.log" | head -1)
fingerprint=$(sed -n 's/^Agent fingerprint: //p' "$directory/agent.log" | head -1)
token=$("$directory/bin/durpdeploy" tokens create --user admin@durp.info \
    --name "$container-bootstrap" 2>>"$directory/bootstrap.log")
api() {
    local arguments=()
    if [[ $# == 3 ]]; then arguments=(--data "$3"); fi
    curl --cacert "$directory/tls/cert.pem" -fsS --max-time 30 \
        -H "Authorization: Bearer $token" \
        -H 'Content-Type: application/json' -X "$1" \
        "${arguments[@]}" "$DURPDEPLOY_BASE_URL/api/v1/$2"
}
pair_body=$(python3 -c 'import json,sys
print(json.dumps(dict(zip(("address", "code", "fingerprint"), sys.argv[1:], strict=True))))' \
    "https://$agent_address" "$code" "$fingerprint")
api POST admin/agents/pair "$pair_body" >"$directory/pairing.json"
agent_id=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["agent_id"])' <"$directory/pairing.json")
printf '%s\n' "$agent_id" >"$directory/agent.id"

# Retain an actual remote execution, not just an inventory registration.
api POST "admin/agents/$agent_id/labels" '{"label":"demo"}' >/dev/null
environment_id=$(api POST environments '{"name":"Demo agent"}' | \
    python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
api POST "admin/agents/$agent_id/environments" \
    "{\"environment_id\":$environment_id}" >/dev/null
agent_container_ready() {
    python3 -c 'import json,sys
agent=json.load(sys.stdin)["agent"]
sys.exit(not (agent["agent_protocol"]["String"] == "agent/3"
    and "container" in (agent["execution_modes"] or [])
    and sys.argv[1] in (agent["container_runtimes"] or [])
    and {"bash", "python3", "pwsh"}.issubset(agent["container_interpreters"] or [])))' \
        "$runtime" <"$directory/agent-capabilities.json"
}
for _ in {1..150}; do
    api GET "admin/agents/$agent_id" >"$directory/agent-capabilities.json"
    if agent_container_ready 2>/dev/null; then break; fi
    sleep 0.2
done
# Keep the last probe failure visible instead of seeding work that cannot run.
agent_container_ready || { echo 'Agent container capabilities did not become ready; inspect agent.log.' >&2; exit 1; }
project_id=$(api POST projects '{"name":"Demo agent execution"}' | \
    python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
api POST "projects/$project_id/steps" \
    '{"name":"Agent hello","script_body":"echo demo-agent-connected","execution_target":"agent","agent_selectors":["demo"]}' >/dev/null
api POST "projects/$project_id/steps" \
    '{"name":"Agent Bash container","script_body":"test \"$(id -u)\" = 65534; test ! -e /run/durpdeploy/runtime.sock; echo demo-agent-container-bash","interpreter":"bash","container_image":"docker.io/library/bash:5.2","execution_target":"agent","agent_execution_mode":"container","agent_selectors":["demo"]}' >/dev/null
api POST "projects/$project_id/steps" \
    '{"name":"Agent Python container","script_body":"import os\nassert os.getuid() == 65534\nassert not os.path.exists(\"/run/durpdeploy/runtime.sock\")\nprint(\"demo-agent-container-python\")","interpreter":"python3","container_image":"docker.io/library/python:3.12-alpine","execution_target":"agent","agent_execution_mode":"container","agent_selectors":["demo"]}' >/dev/null
api POST "projects/$project_id/steps" \
    '{"name":"Agent PowerShell container","script_body":"if (Test-Path /run/durpdeploy/runtime.sock) { throw \"Unexpected runtime socket\" }; Write-Output demo-agent-container-pwsh","interpreter":"pwsh","container_image":"mcr.microsoft.com/powershell:latest","execution_target":"agent","agent_execution_mode":"container","agent_selectors":["demo"]}' >/dev/null
release_id=$(api POST "projects/$project_id/releases" '{"version":"demo-v1"}' | \
    python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
deployment_id=$(api POST "projects/$project_id/deployments" \
    "{\"release_id\":$release_id,\"environment_id\":$environment_id}" | \
    python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
for _ in {1..300}; do
    status=$(api GET "deployments/$deployment_id/status" | \
        python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
    [[ "$status" =~ ^(succeeded|failed|cancelled)$ ]] && break
    sleep 0.2
done
[[ "$status" == succeeded ]] || { echo "Demo agent execution: $status" >&2; exit 1; }
api GET "deployments/$deployment_id/logs" | grep -q demo-agent-connected
api GET "deployments/$deployment_id/logs" >"$directory/agent-deployment-logs.json"
for marker in bash python pwsh; do
    grep -q "demo-agent-container-$marker" "$directory/agent-deployment-logs.json"
done
"$directory/bin/durpdeploy" tokens revoke "${token:0:12}" >>"$directory/bootstrap.log" 2>&1
unset token

printf 'Populating retained demo examples with make e2e-test.\n'
export DURPDEPLOY_E2E_CLI="$directory/bin/durpdeploy"
export DURPDEPLOY_E2E_CA_FILE="$directory/tls/cert.pem"
E2E_ADMIN_EMAIL=admin@durp.info E2E_ADMIN_PASSWORD="$password" \
    DURPDEPLOY_DB="$directory/durpdeploy.db" \
    make --no-print-directory e2e-test >"$directory/e2e.log" 2>&1 &
e2e_pid=$!
wait "$e2e_pid"
e2e_pid=
# This database belongs entirely to the demo. Verify the retained schedules
# stay off even if the E2E harness's best-effort cleanup encountered a lock.
sqlite3 -cmd '.timeout 5000' "$directory/durpdeploy.db" \
    'UPDATE scheduled_deployments SET enabled=0;'
[[ $(sqlite3 -cmd '.timeout 5000' "$directory/durpdeploy.db" \
    'SELECT count(*) FROM scheduled_deployments WHERE enabled=1;') == 0 ]]
complete=1
printf '\nDemo ready: %s\n' "$DURPDEPLOY_BASE_URL"
cat "$directory/login.txt"
printf 'Agent: %s/admin/agents/%s\n' "$DURPDEPLOY_BASE_URL" "$agent_id"
printf 'Agent deployment: %s/deployments/%s\n' "$DURPDEPLOY_BASE_URL" "$deployment_id"
printf 'Data and logs: %s\nStop: %q stop %q\n' "$directory" "$ROOT/scripts/demo.sh" "$directory"
printf 'Temporary HTTPS certificate (browser trust may be required): %s/tls/cert.pem\n' "$directory"
sed -n '/=== Retained manual checks/,$p' "$directory/e2e.log"
