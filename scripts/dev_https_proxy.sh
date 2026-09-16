#!/usr/bin/env bash
set -euo pipefail

container=${DURPDEPLOY_HTTPS_PROXY_CONTAINER:-durpdeploy-dev-https}
port=${DURPDEPLOY_HTTPS_PROXY_PORT:-8443}
backend=${DURPDEPLOY_HTTPS_PROXY_BACKEND:-host.docker.internal:8080}
image=${DURPDEPLOY_HTTPS_PROXY_IMAGE:-docker.io/library/caddy:2-alpine}
health_url="https://localhost:${port}/healthz"
container_engine=""
config=""
tls_dir=""
app_pid=""
app_pgid=""
tls_hosts=(localhost 127.0.0.1 "[::1]")

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
	[[ -z "$container_engine" ]] || \
		"$container_engine" rm -f "$container" >/dev/null 2>&1 || true
	[[ -z "$config" ]] || rm -f "$config"
	if [[ -n "$tls_dir" ]]; then
		rm -f "$tls_dir/ca-key.pem" "$tls_dir/ca.pem" \
			"$tls_dir/ca.srl" "$tls_dir/dev-key.pem" \
			"$tls_dir/dev.csr" "$tls_dir/dev.ext" \
			"$tls_dir/dev-cert.pem"
		rmdir "$tls_dir" 2>/dev/null || true
	fi
	exit "$status"
}

fail() {
	echo "ERROR: $*" >&2
	exit 1
}

add_tls_ip() {
	local address=$1 host existing
	[[ "$address" =~ ^[0-9A-Fa-f:.]+$ ]] || return 0
	if [[ "$address" == *:* ]]; then
		host="[$address]"
	else
		host=$address
	fi
	for existing in "${tls_hosts[@]}"; do
		[[ "$existing" != "$host" ]] || return 0
	done
	tls_hosts+=("$host")
}

select_container_engine() {
	local candidate
	for candidate in docker podman; do
		if command -v "$candidate" >/dev/null 2>&1 && \
			"$candidate" info >/dev/null 2>&1; then
			container_engine=$candidate
			return
		fi
	done
	fail "Docker or Podman is unavailable; install one and start its engine."
}

if (($# == 0)); then
	fail "usage: $0 <dev-server command...>"
fi

select_container_engine
command -v setsid >/dev/null 2>&1 || fail "setsid is required to stop the development server process group."

if "$container_engine" container inspect "$container" >/dev/null 2>&1; then
	if curl -kfsS "$health_url" >/dev/null 2>&1; then
		echo "Development server is already running at $health_url"
		exit 0
	fi
	echo "Replacing unhealthy HTTPS proxy container '$container'."
	"$container_engine" rm -f "$container" >/dev/null || \
		fail "Could not remove unhealthy HTTPS proxy container '$container'."
fi

host_ip_output=$(hostname -I 2>/dev/null) || host_ip_output=""
for address in $host_ip_output; do
	add_tls_ip "$address"
done
config=$(mktemp "${TMPDIR:-/tmp}/durpdeploy-caddy.XXXXXX")
tls_dir=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-tls.XXXXXX")
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

san="DNS:localhost"
for host in "${tls_hosts[@]:1}"; do
	address=${host#[}
	address=${address%]}
	san+=",IP:${address}"
done

openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 7 \
	-subj "/CN=DurpDeploy Development CA" \
	-addext "basicConstraints=critical,CA:TRUE" \
	-addext "keyUsage=critical,keyCertSign,cRLSign" \
	-keyout "$tls_dir/ca-key.pem" -out "$tls_dir/ca.pem" \
	>/dev/null 2>&1
openssl req -new -newkey rsa:2048 -sha256 -nodes \
	-subj "/CN=localhost" \
	-keyout "$tls_dir/dev-key.pem" -out "$tls_dir/dev.csr" \
	>/dev/null 2>&1
cat >"$tls_dir/dev.ext" <<EOF
subjectAltName=$san
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
EOF
openssl x509 -req -sha256 -days 7 \
	-in "$tls_dir/dev.csr" \
	-CA "$tls_dir/ca.pem" -CAkey "$tls_dir/ca-key.pem" \
	-CAcreateserial -extfile "$tls_dir/dev.ext" \
	-out "$tls_dir/dev-cert.pem" >/dev/null 2>&1
chmod 600 "$tls_dir/ca-key.pem" "$tls_dir/dev-key.pem"

cat >"$config" <<EOF
:443 {
	tls /etc/caddy/dev-cert.pem /etc/caddy/dev-key.pem
	reverse_proxy $backend
}
EOF

echo "WARNING: development HTTPS uses a temporary local CA; browser trust warnings are expected."
echo "Starting HTTPS proxy at https://localhost:${port} -> ${backend}"
echo "TLS certificate hosts: ${tls_hosts[*]}"
echo "Development CA certificate: $tls_dir/ca.pem"
if ! "$container_engine" run -d --rm --name "$container" \
	--add-host host.docker.internal:host-gateway \
	-p "${port}:443" \
	-v "$config:/etc/caddy/Caddyfile:ro,Z" \
	-v "$tls_dir/dev-cert.pem:/etc/caddy/dev-cert.pem:ro,Z" \
	-v "$tls_dir/dev-key.pem:/etc/caddy/dev-key.pem:ro,Z" \
	"$image" >/dev/null; then
	fail "Could not start Caddy. The container engine must support the Linux host-gateway mapping required to reach the host backend."
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

"$container_engine" logs "$container" >&2 || true
fail "HTTPS proxy backend is unhealthy at ${backend}; expected ${health_url}. Set DURPDEPLOY_HTTPS_PROXY_BACKEND to override it."
