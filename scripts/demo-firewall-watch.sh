#!/usr/bin/env bash
# Unprivileged watcher; the root-owned helper only grants expiring TCP leases.
set -euo pipefail
[[ $# == 1 ]] || { echo "Usage: $0 DEMO_DIRECTORY" >&2; exit 2; }
directory=$(CDPATH= cd -- "$1" && pwd)
exec 8>"$directory/firewall.watch.lock"
flock -n 8 || { echo 'This demo already has a firewall watcher.' >&2; exit 1; }
engine=$(cat "$directory/runtime")
container=$(cat "$directory/container")
[[ $container =~ ^durpdeploy-demo-[A-Za-z0-9]+$ ]] || exit 2
proxy="$container-https"
identity=$("$engine" inspect --format '{{.Id}}' "$proxy")
[[ $identity =~ ^[a-f0-9]{64}$ ]] || exit 2
address=$("$engine" port "$identity" 8443/tcp | head -1)
port=${address##*:}
[[ $port =~ ^[0-9]{5}$ ]] || exit 2
helper=/usr/local/libexec/durpdeploy-demo-firewall
release() {
    sudo -n "$helper" release "$port" || \
        echo 'Firewall release failed; the last lease expires within 120 seconds.' >&2
}
trap release EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
while [[ $("$engine" inspect --format '{{.State.Running}}' "$identity" 2>/dev/null || true) == true ]]; do
    sudo -n "$helper" lease "$port"
    touch "$directory/firewall.ready"
    # Check proxy identity every second; renew every 30 seconds.
    for _ in {1..30}; do
        sleep 1
        [[ $("$engine" inspect --format '{{.State.Running}}' "$identity" 2>/dev/null || true) == true ]] || exit 0
    done
done
