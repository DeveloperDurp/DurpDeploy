#!/usr/bin/env bash
set -Eeuo pipefail
trap 'printf "E2E failed at line %s\n" "$LINENO" >&2' ERR

[[ "${GITHUB_ACTIONS:-}" == true ]] || { printf 'Run only on an ephemeral GitHub runner\n' >&2; exit 2; }

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
account=ddpe2e
namespace="e2e_${GITHUB_RUN_ID}_${GITHUB_RUN_ATTEMPT}"
app_name="durpdeploy-e2e-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
volume="${app_name}-data"
identity_volume="${app_name}-identity"
image="${app_name}:test"
server_pid=
bridge_pid=
uid=
cleanup() {
    local result=$?
    if [[ -n "$server_pid" ]]; then
        kill "$server_pid" 2>/dev/null || true
        wait "$server_pid" 2>/dev/null || true
    fi
    if [[ -n "$bridge_pid" ]]; then
        kill "$bridge_pid" 2>/dev/null || true
        wait "$bridge_pid" 2>/dev/null || true
    fi
    if ((result != 0)); then
        docker logs "$app_name" 2>/dev/null || true
        sudo journalctl -u ssh --no-pager -n 30 >&2 || true
        if [[ -f "$tmp/bad-runtime.log" ]]; then
            grep -E 'error|ERROR|failed' "$tmp/bad-runtime.log" >&2 || true
        fi
    fi
    docker rm -f "$app_name" >/dev/null 2>&1 || true
    docker volume rm -f "$volume" >/dev/null 2>&1 || true
    docker volume rm -f "$identity_volume" >/dev/null 2>&1 || true
    if [[ -n "$uid" ]]; then
        sudo loginctl disable-linger "$account" || true
        sudo systemctl stop "user@$uid.service" || true
        sudo userdel -r "$account" || true
    fi
    sudo rm -rf -- "$tmp/app-ssh"
    rm -rf "$tmp"
}
trap cleanup EXIT

printf 'Provisioning rootless execution account\n'
sudo useradd --create-home --shell /bin/bash "$account"
# Ubuntu's sshd closes public-key sessions for password-locked accounts.
# An empty password unlocks this ephemeral account; sshd still rejects
# password authentication because PermitEmptyPasswords defaults to no.
sudo passwd --delete "$account"
uid=$(id -u "$account")
sudo loginctl enable-linger "$account"
sudo systemctl start "user@$uid.service"
socket="/home/$account/podman-api.sock"
sudo -u "$account" env -i HOME="/home/$account" USER="$account" \
    LOGNAME="$account" PATH=/usr/local/bin:/usr/bin:/bin \
    XDG_CONFIG_HOME="/home/$account/.config" XDG_RUNTIME_DIR="/run/user/$uid" \
    DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$uid/bus" \
    podman system service --time=0 "unix://$socket" &
bridge_pid=$!
for i in {1..50}; do
    sudo -u "$account" test -S "$socket" && break
    sleep 0.1
done
if ! sudo -u "$account" test -S "$socket"; then
    printf 'Rootless Podman socket missing at %s\n' "$socket" >&2
    ps -fp "$bridge_pid" >&2 || true
    sudo ls -ld "/run/user/$uid" "/home/$account" >&2 || true
    exit 1
fi

mkdir -p "$tmp/host-home/.ssh" "$tmp/app-ssh"
chmod 700 "$tmp/host-home" "$tmp/host-home/.ssh"
ssh-keygen -q -t ed25519 -N '' -f "$tmp/host-home/.ssh/id_ed25519"
sudo install -d -m 700 -o "$account" -g "$account" "/home/$account/.ssh"
sudo install -m 600 -o "$account" -g "$account" \
    "$tmp/host-home/.ssh/id_ed25519.pub" "/home/$account/.ssh/authorized_keys"
sudo install -d -m 700 -o "$account" -g "$account" "/home/$account/bin"
printf '%s\n' '#!/bin/sh' \
    'expected="unix:///run/user/$(id -u)/docker.sock"' \
    'if [ "$1" = --host ]; then' \
    '    [ "$2" = "$expected" ] || exit 64' \
    '    shift 2' \
    'fi' \
    'export HOME="/home/$(id -un)"' \
    'export XDG_RUNTIME_DIR="/run/user/$(id -u)"' \
    'export XDG_CONFIG_HOME="$HOME/.config"' \
    'export CONTAINER_HOST="unix://$HOME/podman-api.sock"' \
    'exec /usr/bin/podman "$@"' \
    | sudo tee "/home/$account/bin/docker" >/dev/null
printf '%s\n' 'export PATH="$HOME/bin:$PATH"' \
    | sudo tee "/home/$account/.bashrc" >/dev/null
sudo chown "$account:$account" "/home/$account/bin/docker" \
    "/home/$account/.bashrc"
sudo chmod 700 "/home/$account/bin/docker"
sudo systemctl start ssh
for host in 127.0.0.1 host.containers.internal; do
    for public_key in /etc/ssh/ssh_host_*_key.pub; do
        host_key=$(sudo cut -d ' ' -f 1,2 "$public_key")
        printf '%s %s\n' "$host" "$host_key" \
            >>"$tmp/host-home/.ssh/known_hosts"
    done
done
cp "$tmp/host-home/.ssh/id_ed25519" "$tmp/app-ssh/id_ed25519"
cp "$tmp/host-home/.ssh/known_hosts" "$tmp/app-ssh/known_hosts"
chmod 700 "$tmp/app-ssh"
chmod 600 "$tmp/host-home/.ssh/"* "$tmp/app-ssh/"*
sudo chown -R 10001:10001 "$tmp/app-ssh"
chmod 755 "$tmp"
# The native Docker SSH helper uses the passwd home, not an overridden HOME.
install -d -m 700 "$HOME/.ssh"
touch "$HOME/.ssh/config"
chmod 600 "$HOME/.ssh/config"
printf '%s\n' \
    'Host 127.0.0.1' \
    "    IdentityFile $tmp/host-home/.ssh/id_ed25519" \
    "    UserKnownHostsFile $tmp/host-home/.ssh/known_hosts" \
    '    IdentitiesOnly yes' >>"$HOME/.ssh/config"

exec_podman() {
    sudo -u "$account" env -i \
        HOME="/home/$account" USER="$account" \
        LOGNAME="$account" PATH=/usr/local/bin:/usr/bin:/bin \
        XDG_RUNTIME_DIR="/run/user/$uid" \
        DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$uid/bus" \
        /bin/sh -c 'cd "$HOME" && exec /usr/bin/podman "$@"' sh "$@"
}
for step_image in docker.io/library/bash:5.2 docker.io/library/python:3.12-alpine \
    mcr.microsoft.com/powershell:7.4-ubuntu-22.04; do
    printf 'Preloading %s\n' "$step_image"
    exec_podman pull "$step_image"
done
exec_podman run --rm --pull=never --entrypoint=bash docker.io/library/bash:5.2 -c 'test -x /usr/local/bin/bash || command -v bash'
exec_podman run --rm --pull=never --entrypoint=python3 docker.io/library/python:3.12-alpine -c 'print("python ready")'
exec_podman run --rm --pull=never --entrypoint=pwsh mcr.microsoft.com/powershell:7.4-ubuntu-22.04 -NoProfile -Command 'Write-Output "pwsh ready"'

export DURPDEPLOY_CONTAINER_RUNTIME=docker
export DURPDEPLOY_CONTAINER_NAMESPACE="$namespace"
export DURPDEPLOY_CONTAINER_URL="ssh://$account@127.0.0.1/run/user/$uid/docker.sock"
printf 'Checking rootless SSH connection\n'
sudo -u "$account" python3 -c \
    'import socket,sys; s=socket.socket(socket.AF_UNIX); s.connect(sys.argv[1])' \
    "$socket"
HOME="$tmp/host-home" docker --host="$DURPDEPLOY_CONTAINER_URL" info \
    --format '{{json .SecurityOptions}}' | grep -q 'name=rootless'

cd "$root"
go build -o "$tmp/durpdeploy" ./cmd/server
export DURPDEPLOY_SECRET_KEY
DURPDEPLOY_SECRET_KEY=$(openssl rand -base64 32)
DURPDEPLOY_DB="$tmp/bad-runtime.db" "$tmp/durpdeploy" admin create \
    --email e2e-admin@test.local --password e2e-admin-password-1234 >/dev/null
HOME="$tmp/host-home" DURPDEPLOY_DB="$tmp/bad-runtime.db" \
    DURPDEPLOY_CONTAINER_URL="ssh://$account@127.0.0.1/run/user/99999/docker.sock" \
    DURPDEPLOY_EXECUTION_BOUNDARY=service DURPDEPLOY_ADDR=127.0.0.1:18082 \
    DURPDEPLOY_AGENT_LISTEN_ADDR=127.0.0.1:0 \
    DURPDEPLOY_AGENT_PUBLIC_URL=https://localhost \
    DURPDEPLOY_AGENT_IDENTITY_DIR="$tmp/bad-agent" \
    DURPDEPLOY_URL=http://127.0.0.1:18082 \
    "$tmp/durpdeploy" >"$tmp/bad-runtime.log" 2>&1 &
server_pid=$!
for i in {1..100}; do
    curl -fsS http://127.0.0.1:18082/healthz >/dev/null 2>&1 && break
    sleep 0.1
done
base=http://127.0.0.1:18082
curl -fsS -c "$tmp/cookies" -o /dev/null -X POST \
    -d 'email=e2e-admin@test.local&password=e2e-admin-password-1234' "$base/login"
csrf=$(curl -fsS -b "$tmp/cookies" "$base/" | grep -oP '<meta name="csrf-token" content="\K[^"]+' | head -1)
token=$(curl -fsS -b "$tmp/cookies" -D "$tmp/headers" -o /dev/null -X POST \
    -d "name=runtime-probe&csrf_token=$csrf" "$base/settings/tokens"; \
    curl -fsS -b "$tmp/cookies" "$base$(grep -i '^location:' "$tmp/headers" | tr -d '\r' | cut -d ' ' -f 2)" \
        | grep -oE 'ddp_pat_[0-9a-f]{64}' | head -1)
api() { curl -fsS -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -d "$1" "$base/api/v1/$2"; }
project=$(api '{"name":"runtime-probe"}' projects | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
environment=$(api '{"name":"runtime-probe"}' environments | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
api '{"name":"probe","script_body":"echo must-not-run","container_image":"docker.io/library/bash:5.2"}' \
    "projects/$project/steps" >/dev/null
release=$(api '{"version":"v1"}' "projects/$project/releases" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
deployment=$(api "{\"release_id\":$release,\"environment_id\":$environment}" \
    "projects/$project/deployments" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
for i in {1..200}; do
    status=$(curl -fsS -H "Authorization: Bearer $token" \
        "$base/api/v1/deployments/$deployment/status" \
        | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])')
    [[ "$status" =~ ^(failed|succeeded|cancelled)$ ]] && break
    sleep 0.1
done
[[ "$status" == failed ]] || { printf 'Missing runtime status: %s\n' "$status" >&2; exit 1; }
if curl -fsS -H "Authorization: Bearer $token" \
    "$base/api/v1/deployments/$deployment/logs" | grep -q must-not-run; then
    printf 'Missing runtime executed the step\n' >&2
    exit 1
fi
kill "$server_pid"
wait "$server_pid" || true
server_pid=

printf 'Running host control-plane E2E\n'
HOME="$tmp/host-home" DURPDEPLOY_E2E_PORT=18080 ./scripts/e2e_test.sh
test -z "$(exec_podman ps -aq --filter "label=io.durpdeploy.namespace=docker:$namespace")"

printf 'Building containerized control plane\n'
docker build -t "$image" .
docker volume create "$volume" >/dev/null
docker volume create "$identity_volume" >/dev/null
docker run --rm --volume "$volume:/data" -e DURPDEPLOY_SECRET_KEY \
    "$image" admin create --email e2e-admin@test.local \
    --password e2e-admin-password-1234 >/dev/null
docker run -d --name "$app_name" --read-only --user 10001:10001 \
    --cap-drop ALL --security-opt no-new-privileges --tmpfs /tmp:size=64m,mode=1777 \
    --add-host host.containers.internal:host-gateway \
    --volume "$volume:/data" \
    --volume "$identity_volume:/var/lib/durpdeploy/agent-identity" \
    --volume "$tmp/app-ssh:/home/durpdeploy/.ssh:ro" \
    -p 127.0.0.1:18081:8080 \
    -e DURPDEPLOY_SECRET_KEY -e DURPDEPLOY_CONTAINER_RUNTIME \
    -e DURPDEPLOY_CONTAINER_NAMESPACE \
    -e "DURPDEPLOY_CONTAINER_URL=ssh://$account@host.containers.internal/run/user/$uid/docker.sock" \
    -e DURPDEPLOY_DB=/data/durpdeploy.db \
    -e DURPDEPLOY_ADDR=0.0.0.0:8080 \
    -e DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943 \
    -e DURPDEPLOY_AGENT_PUBLIC_URL=https://localhost \
    -e DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity \
    -e DURPDEPLOY_URL=http://127.0.0.1:18081 \
    "$image" >/dev/null
for i in {1..100}; do
    curl -fsS http://127.0.0.1:18081/healthz >/dev/null 2>&1 && break
    sleep 0.1
done
docker exec "$app_name" /bin/sh -c 'command -v podman && command -v ssh && test ! -e /run/docker.sock && test ! -e /run/podman/podman.sock'
docker exec "$app_name" docker \
    --host="ssh://$account@host.containers.internal/run/user/$uid/docker.sock" \
    info --format '{{json .SecurityOptions}}' | grep -q 'name=rootless'
printf 'Running containerized control-plane E2E\n'
DURPDEPLOY_E2E_CLIENT_ONLY=1 DURPDEPLOY_E2E_CONTROL_PLANE_PORT=18081 \
    DURPDEPLOY_BASE_URL=http://127.0.0.1:18081 \
    ./scripts/e2e_test.sh
docker stop "$app_name" >/dev/null
test -z "$(exec_podman ps -aq --filter "label=io.durpdeploy.namespace=docker:$namespace")"
printf 'Host and in-container API/web E2E and container cleanup: PASS\n'
