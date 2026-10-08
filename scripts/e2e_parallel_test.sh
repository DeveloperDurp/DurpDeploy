#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
scope="parallel_${tmp##*/}"
scope=${scope//[^a-zA-Z0-9_-]/_}
runtime=${DURPDEPLOY_CONTAINER_RUNTIME:-}
if [[ -z $runtime ]]; then
	for runtime in docker podman; do
		command -v "$runtime" >/dev/null 2>&1 && \
			"$runtime" info >/dev/null 2>&1 && break
	done
fi
export DURPDEPLOY_EMBEDDED_AGENT_ENABLED=true
engine=("$runtime")
if [[ -n ${DURPDEPLOY_CONTAINER_URL:-} ]]; then
	if [[ $runtime == podman ]]; then
		engine+=(--remote "--url=$DURPDEPLOY_CONTAINER_URL")
	else
		engine+=("--host=$DURPDEPLOY_CONTAINER_URL")
	fi
fi
pids=()
cleanup() {
	for pid in "${pids[@]}"; do
		kill "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
	done
	"${engine[@]}" rm --force "$scope" >/dev/null 2>&1 || true
	rm -rf -- "$tmp"
}
trap cleanup EXIT
trap 'exit 143' INT TERM

# Given: a neighboring container owned by this regression, never a live app.
"${engine[@]}" run --detach --rm --name="$scope" \
	--label="io.durpdeploy.namespace=$runtime:$scope" \
	--network=none --read-only --cap-drop=ALL \
	--security-opt=no-new-privileges --user=65534:65534 \
	--entrypoint=bash docker.io/library/bash:5.2 \
	-c 'exec sleep 300' >/dev/null

# When: two independent processes start servers and run real API/web retries.
for lane in 1 2; do
	(
		unset DURPDEPLOY_CONTAINER_NAMESPACE
		export DURPDEPLOY_CONTAINER_RUNTIME="$runtime"
		export DURPDEPLOY_E2E_PORT=0 DURPDEPLOY_STAGING_E2E_ONLY=1
		export DURPDEPLOY_E2E_CLIENT_ONLY=0 DURPDEPLOY_AUTH_MFA_HTTP_MATRIX=0
		export DURPDEPLOY_E2E_START_GATE="$tmp/start"
		exec bash "$root/scripts/e2e_test.sh"
	) >"$tmp/$lane.log" 2>&1 &
	pids+=("$!")
done
for lane in 1 2; do
	ready=0
	for _ in {1..480}; do
		if grep -q '^E2E instance:' "$tmp/$lane.log"; then
			ready=1
			break
		fi
		kill -0 "${pids[lane-1]}" 2>/dev/null || break
		sleep 0.25
	done
	if ((ready == 0)); then
		cat "$tmp/$lane.log" >&2
		exit 1
	fi
done

# Registration and assertion must use each allocated server's real origin.
for lane in 1 2; do
	base=$(awk '/^E2E instance:/ { sub(/^url=/, "", $3); print $3 }' "$tmp/$lane.log")
	(
		unset DURPDEPLOY_E2E_CONTROL_PLANE_PORT
		export DURPDEPLOY_E2E_CLIENT_ONLY=1 DURPDEPLOY_BASE_URL="$base"
		ROOT_DIR=$root
		source "$root/scripts/e2e_wait.sh"
		source "$root/scripts/e2e_lifecycle.sh"
		[[ $CONTROL_PLANE_PORT == "${BASE##*:}" ]]
	)
	DURPDEPLOY_BASE_URL="$base" node "$root/scripts/e2e_origin_test.mjs" \
		>"$tmp/origin-$lane.log" 2>&1 &
	pids+=("$!")
done
for lane in 1 2; do
	if ! wait "${pids[lane+1]}"; then
		cat "$tmp/origin-$lane.log" >&2
		exit 1
	fi
	cat "$tmp/origin-$lane.log"
done
pids=("${pids[0]}" "${pids[1]}")
printf 'Client-only control-plane port: PASS\n'
for invalid_port in 0 65536 18446744073709551617; do
	if (
		export DURPDEPLOY_E2E_CLIENT_ONLY=1 DURPDEPLOY_BASE_URL="$base"
		export DURPDEPLOY_E2E_CONTROL_PLANE_PORT="$invalid_port"
		ROOT_DIR=$root
		source "$root/scripts/e2e_wait.sh"
		source "$root/scripts/e2e_lifecycle.sh"
	) >"$tmp/client-invalid.log" 2>&1; then
		echo 'FAIL: client-only probe accepted an invalid port' >&2
		exit 1
	fi
	grep -q 'FAIL: control-plane probe port must be from 1 to 65535' "$tmp/client-invalid.log"
done

# A real input failure must fail the check without logging its password.
probe_password=$(openssl rand -hex 16)
if DURPDEPLOY_BASE_URL="$base" DURPDEPLOY_ADMIN_PASSWORD="$probe_password" \
	node "$root/scripts/e2e_origin_test.mjs" --force-selector-failure \
	>"$tmp/origin-failure.log" 2>&1; then
	echo 'FAIL: forced origin input error succeeded' >&2
	exit 1
fi
if grep -Fq -- "$probe_password" "$tmp/origin-failure.log"; then
	echo 'FAIL: origin input error exposed its password' >&2
	exit 1
fi
grep -q '^Allocated E2E WebAuthn origin: FAIL (details redacted)$' "$tmp/origin-failure.log"
printf 'Origin browser failure redaction: PASS\n'

# An occupied explicit port must fail even though the other app is healthy.
port=$(awk '/^E2E instance:/ { sub(/^.*:/, "", $3); print $3 }' "$tmp/1.log")
status=0
(
	unset DURPDEPLOY_CONTAINER_NAMESPACE
	DURPDEPLOY_CONTAINER_RUNTIME="$runtime" DURPDEPLOY_E2E_PORT="$port" \
		DURPDEPLOY_E2E_CLIENT_ONLY=0 DURPDEPLOY_STAGING_E2E_ONLY=1 \
		bash "$root/scripts/e2e_test.sh"
) >"$tmp/occupied.log" 2>&1 || status=$?
if ((status == 0)) || grep -q '^E2E instance:' "$tmp/occupied.log"; then
	echo 'FAIL: occupied port accepted another process as the owned server' >&2
	exit 1
fi
grep -q 'browser listener:.*address already in use' "$tmp/occupied.log"

# Constructors in ordinary Go suites must not sweep an inherited namespace.
cd "$root"
DURPDEPLOY_CONTAINER_RUNTIME="$runtime" \
	DURPDEPLOY_CONTAINER_NAMESPACE="$scope" \
	go test -count=1 \
	-run '^TestWebAuthn_BaselineRepositoryAndRouterCompatibility$' \
	./cmd/server ./internal/handler ./internal/handler/api ./internal/mfa \
	./internal/runner ./internal/scheduler ./internal/server ./internal/agentserver >"$tmp/go.log" 2>&1 || {
	cat "$tmp/go.log" >&2
	exit 1
}
touch "$tmp/start"
result=0
for lane in 1 2; do
	wait "${pids[lane-1]}" || result=1
	cat "$tmp/$lane.log"
done
pids=()
((result == 0))

# Then: both scopes and ports differ, both contracts pass, neighbor survives.
for field in 3 4; do
	count=$(awk -v field="$field" '/^E2E instance:/ { print $field }' \
		"$tmp/1.log" "$tmp/2.log" | sort -u | wc -l)
	[[ $count == 2 ]]
done
for lane in 1 2; do
	grep -q 'api file handoff, retry, and deployment isolation: OK' "$tmp/$lane.log"
	grep -q 'web file handoff, retry, and deployment isolation: OK' "$tmp/$lane.log"
done
[[ $("${engine[@]}" inspect --format '{{.State.Running}}' "$scope") == true ]]
printf 'Concurrent agent E2E isolation: PASS\n'
