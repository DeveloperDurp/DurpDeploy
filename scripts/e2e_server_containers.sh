#!/usr/bin/env bash
set -Eeuo pipefail
trap 'printf "E2E failed at line %s\n" "$LINENO" >&2' ERR

[[ "${GITHUB_ACTIONS:-}" == true ]] || {
	printf 'Run only on an ephemeral GitHub runner\n' >&2
	exit 2
}

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
namespace="e2e_${GITHUB_RUN_ID}_${GITHUB_RUN_ATTEMPT}"
app_name="durpdeploy-e2e-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
volume="${app_name}-data"
identity_volume="${app_name}-identity"
image="${app_name}:test"
server_pid=

cleanup() {
	local result=$?
	if [[ -n "$server_pid" ]]; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	if ((result != 0)); then
		docker logs "$app_name" 2>/dev/null || true
		local remaining
		remaining=$(docker ps -aq \
			--filter "label=io.durpdeploy.namespace=docker:$namespace") || true
		for id in $remaining; do
			docker inspect --format '{{.Name}} {{json .State}}' "$id" >&2 || true
		done
		if [[ -f "$tmp/bad-runtime.log" ]]; then
			grep -E 'error|ERROR|failed' "$tmp/bad-runtime.log" >&2 || true
		fi
	fi
	docker rm -f "$app_name" >/dev/null 2>&1 || true
	docker volume rm -f "$volume" >/dev/null 2>&1 || true
	docker volume rm -f "$identity_volume" >/dev/null 2>&1 || true
	rm -rf -- "$tmp"
}
trap cleanup EXIT

export DURPDEPLOY_CONTAINER_RUNTIME=docker
export DURPDEPLOY_CONTAINER_NAMESPACE="$namespace"
export DURPDEPLOY_CONTAINER_URL=unix:///var/run/docker.sock
docker info >/dev/null

cd "$root"
go build -o "$tmp/durpdeploy" ./cmd/server
export DURPDEPLOY_SECRET_KEY
DURPDEPLOY_SECRET_KEY=$(openssl rand -base64 32)

# A disabled embedded agent must fail closed instead of running on the host.
DURPDEPLOY_DB="$tmp/bad-runtime.db" "$tmp/durpdeploy" admin create \
	--email e2e-admin@test.local \
	--password e2e-admin-password-1234 >/dev/null
DURPDEPLOY_DB="$tmp/bad-runtime.db" \
	DURPDEPLOY_EMBEDDED_AGENT_ENABLED=false \
	DURPDEPLOY_EXECUTION_BOUNDARY=service DURPDEPLOY_ADDR=127.0.0.1:18082 \
	DURPDEPLOY_AGENT_LISTEN_ADDR=127.0.0.1:0 \
	DURPDEPLOY_AGENT_PUBLIC_URL=https://localhost \
	DURPDEPLOY_AGENT_IDENTITY_DIR="$tmp/bad-agent" \
	DURPDEPLOY_URL=http://127.0.0.1:18082 \
	"$tmp/durpdeploy" >"$tmp/bad-runtime.log" 2>&1 &
server_pid=$!
for _ in {1..100}; do
	curl -fsS http://127.0.0.1:18082/healthz >/dev/null 2>&1 && break
	sleep 0.1
done
base=http://127.0.0.1:18082
curl -fsS -c "$tmp/cookies" -o /dev/null -X POST \
	-d 'email=e2e-admin@test.local&password=e2e-admin-password-1234' \
	"$base/login"
csrf=$(curl -fsS -b "$tmp/cookies" "$base/" |
	grep -oP '<meta name="csrf-token" content="\K[^"]+' | head -1)
token=$(curl -fsS -b "$tmp/cookies" -D "$tmp/headers" -o /dev/null \
	-X POST -d "name=runtime-probe&csrf_token=$csrf" \
	"$base/settings/tokens";
	curl -fsS -b "$tmp/cookies" \
	"$base$(grep -i '^location:' "$tmp/headers" | tr -d '\r' |
		cut -d ' ' -f 2)" | grep -oE 'ddp_pat_[0-9a-f]{64}' | head -1)
api() {
	curl -fsS -H "Authorization: Bearer $token" \
		-H 'Content-Type: application/json' -d "$1" "$base/api/v1/$2"
}
project=$(api '{"name":"runtime-probe"}' projects |
	python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
environment=$(api '{"name":"runtime-probe"}' environments |
	python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
api '{"name":"probe","script_body":"echo must-not-run","container_image":"docker.io/library/bash:5.2"}' \
	"projects/$project/steps" >/dev/null
release=$(api '{"version":"v1"}' "projects/$project/releases" |
	python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
deployment=$(api \
	"{\"release_id\":$release,\"environment_id\":$environment}" \
	"projects/$project/deployments" |
	python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
for _ in {1..200}; do
	status=$(curl -fsS -H "Authorization: Bearer $token" \
		"$base/api/v1/deployments/$deployment/status" |
		python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])')
	[[ "$status" =~ ^(failed|succeeded|cancelled)$ ]] && break
	sleep 0.1
done
[[ "$status" == failed ]]
if curl -fsS -H "Authorization: Bearer $token" \
	"$base/api/v1/deployments/$deployment/logs" | grep -q must-not-run; then
	printf 'Missing runtime executed the step\n' >&2
	exit 1
fi
kill "$server_pid"
wait "$server_pid" || true
server_pid=

printf 'Running existing-server SQLite E2E\n'
DURPDEPLOY_DB="$tmp/running-server.db" "$tmp/durpdeploy" admin create \
	--email e2e-admin@test.local \
	--password e2e-admin-password-1234 >/dev/null
DURPDEPLOY_DB="$tmp/running-server.db" \
	DURPDEPLOY_EXECUTION_BOUNDARY=service DURPDEPLOY_ADDR=127.0.0.1:18082 \
	DURPDEPLOY_AGENT_LISTEN_ADDR=127.0.0.1:0 \
	DURPDEPLOY_AGENT_PUBLIC_URL=https://localhost \
	DURPDEPLOY_AGENT_IDENTITY_DIR="$tmp/running-agent" \
	DURPDEPLOY_URL=http://127.0.0.1:18082 \
	"$tmp/durpdeploy" >"$tmp/running-server.log" 2>&1 &
server_pid=$!
for _ in {1..100}; do
	curl -fsS http://127.0.0.1:18082/healthz >/dev/null 2>&1 && break
	sleep 0.1
done
suite_started=$(date -u +%FT%TZ)
DURPDEPLOY_BASE_URL=http://127.0.0.1:18082 \
	DURPDEPLOY_DB="$tmp/running-server.db" \
	DURPDEPLOY_E2E_CLI="$tmp/durpdeploy" make e2e-test
kill "$server_pid"
wait "$server_pid"
server_pid=
bash scripts/check_e2e_containers.sh "$namespace" "$suite_started"

printf 'Running host control-plane E2E\n'
suite_started=$(date -u +%FT%TZ)
DURPDEPLOY_E2E_PORT=18080 ./scripts/e2e_test.sh
bash scripts/check_e2e_containers.sh "$namespace" "$suite_started"

printf 'Building containerized control plane\n'
docker build -t "$image" .
docker volume create "$volume" >/dev/null
docker volume create "$identity_volume" >/dev/null
docker run --rm --volume "$volume:/data" -e DURPDEPLOY_SECRET_KEY \
	"$image" admin create --email e2e-admin@test.local \
	--password e2e-admin-password-1234 >/dev/null
docker run -d --name "$app_name" --user 0 --read-only \
	--cap-drop ALL --cap-add SETUID --cap-add SETGID \
	--security-opt no-new-privileges --tmpfs /tmp:size=64m,mode=1777 \
	--volume "$volume:/data" \
	--volume "$identity_volume:/var/lib/durpdeploy/agent-identity" \
	--volume /var/run/docker.sock:/var/run/durpdeploy-runtime.sock \
	-p 127.0.0.1:18081:8080 \
	-e DURPDEPLOY_SECRET_KEY -e DURPDEPLOY_CONTAINER_RUNTIME \
	-e DURPDEPLOY_CONTAINER_NAMESPACE \
	-e DURPDEPLOY_CONTAINER_URL=unix:///var/run/durpdeploy-runtime.sock \
	-e DURPDEPLOY_DB=/data/durpdeploy.db \
	-e DURPDEPLOY_ADDR=0.0.0.0:8080 \
	-e DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943 \
	-e DURPDEPLOY_AGENT_PUBLIC_URL=https://localhost \
	-e DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity \
	-e DURPDEPLOY_URL=http://127.0.0.1:18081 \
	"$image" >/dev/null
for _ in {1..100}; do
	curl -fsS http://127.0.0.1:18081/healthz >/dev/null 2>&1 && break
	sleep 0.1
done
docker exec "$app_name" /bin/sh -ceu \
	'test "$(awk '\''/^Uid:/{print $2}'\'' /proc/1/status)" = 10001; command -v docker; command -v podman'
printf 'Running containerized control-plane E2E\n'
suite_started=$(date -u +%FT%TZ)
DURPDEPLOY_E2E_CLIENT_ONLY=1 DURPDEPLOY_E2E_CONTROL_PLANE_PORT=18081 \
	DURPDEPLOY_BASE_URL=http://127.0.0.1:18081 \
	./scripts/e2e_test.sh
docker stop "$app_name" >/dev/null
bash scripts/check_e2e_containers.sh "$namespace" "$suite_started"
printf 'Host and in-container API/web E2E and container cleanup: PASS\n'
