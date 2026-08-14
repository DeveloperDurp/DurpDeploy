#!/usr/bin/env bash
set -euo pipefail

container=${DURPDEPLOY_HTTPS_PROXY_CONTAINER:-durpdeploy-dev-https}
port=${DURPDEPLOY_HTTPS_PROXY_PORT:-8443}
backend=${DURPDEPLOY_HTTPS_PROXY_BACKEND:-host.docker.internal:8080}
image=${DURPDEPLOY_HTTPS_PROXY_IMAGE:-caddy:2-alpine}
health_url="https://localhost:${port}/healthz"
config=""
app_pid=""
app_pgid=""

stop_app() {
	[[ -z "$app_pgid" ]] && return
	if kill -0 -- "-$app_pgid" 2>/dev/null; then
		kill -TERM -- "-$app_pgid" 2>/dev/null || true
		for _ in $(seq 1 10); do
			kill -0 -- "-$app_pgid" 2>/dev/null || break
			sleep 0.5
		done
		kill -KILL -- "-$app_pgid" 2>/dev/null || true
	fi
	wait "$app_pid" 2>/dev/null || true
}

cleanup() {
	local status=$?
	trap - EXIT INT TERM HUP
	stop_app
	docker rm -f "$container" >/dev/null 2>&1 || true
	[[ -z "$config" ]] || rm -f "$config"
	exit "$status"
}

fail() {
	echo "ERROR: $*" >&2
	exit 1
}

if (($# == 0)); then
	fail "usage: $0 <dev-server command...>"
fi

command -v docker >/dev/null 2>&1 || fail "Docker is required for the HTTPS development proxy."
docker info >/dev/null 2>&1 || fail "Docker is unavailable; start the Docker daemon and try again."
command -v setsid >/dev/null 2>&1 || fail "setsid is required to stop the development server process group."

if docker container inspect "$container" >/dev/null 2>&1; then
	fail "HTTPS proxy container '$container' already exists; choose DURPDEPLOY_HTTPS_PROXY_CONTAINER or remove it."
fi

config=$(mktemp "${TMPDIR:-/tmp}/durpdeploy-caddy.XXXXXX")
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

cat >"$config" <<EOF
https://localhost {
	tls internal
	reverse_proxy $backend
}
EOF

echo "WARNING: development HTTPS uses Caddy's self-signed internal CA; browser trust warnings are expected."
echo "Starting HTTPS proxy at https://localhost:${port} -> ${backend}"
if ! docker run -d --rm --name "$container" \
	--add-host host.docker.internal:host-gateway \
	-p "${port}:443" \
	-v "$config:/etc/caddy/Caddyfile:ro" \
	"$image" >/dev/null; then
	fail "Could not start Caddy. Docker must support the Linux host-gateway mapping required to reach the host backend."
fi

setsid "$@" &
app_pid=$!
app_pgid=$app_pid

for _ in $(seq 1 60); do
	if curl -kfsS "$health_url" >/dev/null 2>&1; then
		wait "$app_pid"
		exit $?
	fi
	if ! kill -0 "$app_pid" 2>/dev/null; then
		wait "$app_pid" || true
		fail "Development server stopped before the HTTPS proxy backend became healthy."
	fi
	sleep 0.5
done

docker logs "$container" >&2 || true
fail "HTTPS proxy backend is unhealthy at ${backend}; expected ${health_url}. Set DURPDEPLOY_HTTPS_PROXY_BACKEND to override it."
