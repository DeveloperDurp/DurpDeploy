#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
pid=
cleanup() {
    if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
    rm -rf -- "$tmp"
}
trap cleanup EXIT
export DEMO_TEST_LOG="$tmp/commands"
export DEMO_TEST_SQLITE=$(command -v sqlite3)
export DEMO_TEST_ADMIT_SQL="INSERT INTO deployments VALUES (2, 'pending', NULL, NULL);"
cat >"$tmp/engine" <<'ENGINE'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$DEMO_TEST_LOG"
ENGINE
chmod 0755 "$tmp/engine"
mkdir -p "$tmp/demo/bin"
printf '%s\n' "$tmp/engine" >"$tmp/demo/runtime"
printf '%s\n' durpdeploy-demo-test >"$tmp/demo/container"
printf 'retained\n' >"$tmp/demo/durpdeploy.db"

# Malformed metadata and reused PIDs must leave all resources untouched.
printf '%s\n' "$$" >"$tmp/demo/server.pid"
if "$root/scripts/demo.sh" stop "$tmp/demo" >"$tmp/output" 2>&1; then
    echo 'FAIL: accepted another process as the demo server' >&2; exit 1
fi
[[ ! -e "$DEMO_TEST_LOG" ]]
printf '%s\n' unsafe-container >"$tmp/demo/container"
if "$root/scripts/demo.sh" stop "$tmp/demo" >"$tmp/output" 2>&1; then
    echo 'FAIL: accepted invalid container metadata' >&2; exit 1
fi
[[ ! -e "$DEMO_TEST_LOG" ]]

# A real task-owned process stops, while its database stays available.
printf '%s\n' durpdeploy-demo-test >"$tmp/demo/container"
cp "$(command -v cat)" "$tmp/demo/bin/durpdeploy"
mkfifo "$tmp/input"
exec 3<>"$tmp/input"
env -u CONTAINER_HOST DURPDEPLOY_DEMO_TEST=retained \
    DOCKER_HOST=unix:///original/docker.sock "$tmp/demo/bin/durpdeploy" <&3 >/dev/null &
pid=$!
for _ in {1..100}; do
    [[ $(ps -p "$pid" -o args=) == "$tmp/demo/bin/durpdeploy" ]] && break
    sleep 0.01
done
printf '%s\n' "$pid" >"$tmp/demo/server.pid"

# Server-only refresh must reject reused PIDs, active work and failed builds
# before stopping a server or touching its agent/proxy/database.
mkdir "$tmp/tools"
cat >"$tmp/tools/sqlite3" <<'SQLITE'
#!/usr/bin/env bash
if [[ ${DEMO_TEST_REAL_DB:-0} == 1 ]]; then
    if [[ ${DEMO_TEST_ADMISSION:-0} == 1 && $1 == -readonly ]]; then
        result=$("$DEMO_TEST_SQLITE" "$@")
        if [[ -f "$DEMO_TEST_LOG.idle" ]]; then
            if "$DEMO_TEST_SQLITE" -cmd '.timeout 100' "$DEMO_TEST_DB" \
                "$DEMO_TEST_ADMIT_SQL" \
                >"$DEMO_TEST_LOG.writer" 2>&1; then
                touch "$DEMO_TEST_LOG.admitted"
            else
                touch "$DEMO_TEST_LOG.blocked"
            fi
        else
            touch "$DEMO_TEST_LOG.idle"
        fi
        printf '%s\n' "$result"
        exit
    fi
    exec "$DEMO_TEST_SQLITE" "$@"
fi
printf '%s\n' "${DEMO_TEST_BUSY:-0}"
SQLITE
cat >"$tmp/tools/make" <<'MAKE'
#!/usr/bin/env bash
[[ ${DEMO_TEST_BUILD_OK:-0} == 1 ]]
MAKE
chmod 0755 "$tmp/tools/"*
printf '%s\n' '{"msg":"server starting","addr":"127.0.0.1:12345"}' >"$tmp/demo/server.log"
printf '%s\n' "$$" >"$tmp/demo/server.pid"
if PATH="$tmp/tools:$PATH" bash "$root/scripts/demo-server-refresh.sh" "$tmp/demo" >"$tmp/output" 2>&1; then
    echo 'FAIL: refresh accepted another process as server' >&2; exit 1
fi
grep -q 'PID belongs to another process' "$tmp/output"
printf '%s\n' "$pid" >"$tmp/demo/server.pid"
if PATH="$tmp/tools:$PATH" DEMO_TEST_BUSY=1 bash "$root/scripts/demo-server-refresh.sh" "$tmp/demo" >"$tmp/output" 2>&1; then
    echo 'FAIL: refresh interrupted active work' >&2; exit 1
fi
grep -q 'confirm agent cleanup' "$tmp/output"

# Lost remote work remains unresolved even after its parent has failed.
rm "$tmp/demo/durpdeploy.db"
sqlite3 "$tmp/demo/durpdeploy.db" <<'SQL'
CREATE TABLE deployments (id INTEGER, status TEXT, container_namespace TEXT, cleanup_confirmed_at INTEGER);
CREATE TABLE remote_step_runs (deployment_id INTEGER, state TEXT, cleanup_confirmed_at INTEGER);
CREATE TABLE remote_deployment_claims (deployment_id INTEGER, state TEXT, cleanup_confirmed_at INTEGER);
INSERT INTO deployments VALUES (1, 'failed', NULL, NULL);
SQL
for table in remote_step_runs remote_deployment_claims; do
    sqlite3 "$tmp/demo/durpdeploy.db" "INSERT INTO $table VALUES (1, 'lost', NULL);"
    if PATH="$tmp/tools:$PATH" DEMO_TEST_REAL_DB=1 bash "$root/scripts/demo-server-refresh.sh" "$tmp/demo" >"$tmp/output" 2>&1; then
        echo "FAIL: refresh interrupted lost work in $table" >&2; exit 1
    fi
    if ! grep -q 'confirm agent cleanup' "$tmp/output"; then
        echo "FAIL: refresh did not reject lost work in $table before building" >&2
        cat "$tmp/output" >&2; exit 1
    fi
    kill -0 "$pid"
    [[ ! -e "$tmp/demo/server.env" ]]
    sqlite3 "$tmp/demo/durpdeploy.db" "DELETE FROM $table;"
    printf 'Refresh refuses lost work in %s: PASS\n' "$table"
done
rm "$tmp/demo/durpdeploy.db"
printf 'retained\n' >"$tmp/demo/durpdeploy.db"
if PATH="$tmp/tools:$PATH" bash "$root/scripts/demo-server-refresh.sh" "$tmp/demo" >"$tmp/output" 2>&1; then
    echo 'FAIL: refresh ignored a failed build' >&2; exit 1
fi
kill -0 "$pid"
[[ $(cat "$tmp/demo/server.pid") == "$pid" ]]
grep -Fxq retained "$tmp/demo/durpdeploy.db"
[[ ! -e "$DEMO_TEST_LOG" ]]
[[ $(stat -c %a "$tmp/demo/server.env") == 600 ]]
grep -q 'Building server in place' "$tmp/output"

# A successful refresh preserves original endpoint settings AND their absence,
# even when the caller supplies different container endpoints.
cat >"$tmp/tools/go" <<'GO'
#!/usr/bin/env bash
if [[ ${DEMO_TEST_BUSY_AFTER_BUILD:-0} == 1 ]]; then
    "$DEMO_TEST_SQLITE" "$DEMO_TEST_DB" "$DEMO_TEST_ADMIT_SQL"
fi
cp "$DEMO_TEST_SERVER_BINARY" "$3"
GO
cat >"$tmp/tools/nohup" <<'NOHUP'
#!/usr/bin/env bash
printf '%s\n' '{"msg":"server starting","addr":"127.0.0.1:12345"}'
printf 'docker=%s\npodman=%s\n' "${DOCKER_HOST-unset}" "${CONTAINER_HOST-unset}"
exec "$@" <"$DEMO_TEST_FIFO"
NOHUP
cat >"$tmp/tools/curl" <<'CURL'
#!/usr/bin/env bash
exit 0
CURL
chmod 0755 "$tmp/tools/"*
mkdir -p "$tmp/demo/tmp" "$tmp/demo/tls"
printf '%s\n' https://citadel.durp.loc:12345 >"$tmp/demo/url"
printf '%s\n' 'retained login' >"$tmp/demo/login.txt"
rm "$tmp/demo/durpdeploy.db"
sqlite3 "$tmp/demo/durpdeploy.db" <<'SQL'
CREATE TABLE deployments (id INTEGER, status TEXT, container_namespace TEXT, cleanup_confirmed_at INTEGER);
CREATE TABLE remote_step_runs (deployment_id INTEGER, state TEXT, cleanup_confirmed_at INTEGER);
CREATE TABLE remote_deployment_claims (deployment_id INTEGER, state TEXT, cleanup_confirmed_at INTEGER);
SQL
if PATH="$tmp/tools:$PATH" DEMO_TEST_BUILD_OK=1 DEMO_TEST_REAL_DB=1 \
    DEMO_TEST_BUSY_AFTER_BUILD=1 DEMO_TEST_DB="$tmp/demo/durpdeploy.db" \
    DEMO_TEST_SERVER_BINARY="$(command -v cat)" \
    bash "$root/scripts/demo-server-refresh.sh" "$tmp/demo" >"$tmp/output" 2>&1; then
    echo 'FAIL: refresh ignored work admitted during its build' >&2; exit 1
fi
kill -0 "$pid"
[[ $(cat "$tmp/demo/server.pid") == "$pid" ]]
grep -q 'confirm agent cleanup' "$tmp/output"
# A rejected final check releases its barrier and leaves the original server.
sqlite3 "$tmp/demo/durpdeploy.db" 'DELETE FROM deployments;'
printf 'Refresh rejects work admitted during build and releases its barrier: PASS\n'
PATH="$tmp/tools:$PATH" DEMO_TEST_BUILD_OK=1 \
    DEMO_TEST_REAL_DB=1 DEMO_TEST_ADMISSION=1 DEMO_TEST_DB="$tmp/demo/durpdeploy.db" \
    DEMO_TEST_SERVER_BINARY="$(command -v cat)" DEMO_TEST_FIFO="$tmp/input" \
    DOCKER_HOST=unix:///caller/docker.sock CONTAINER_HOST=unix:///caller/podman.sock \
    bash "$root/scripts/demo-server-refresh.sh" "$tmp/demo" >"$tmp/output" 2>&1
wait "$pid" 2>/dev/null || true
pid=$(cat "$tmp/demo/server.pid")
kill -0 "$pid"
if [[ ! -f "$DEMO_TEST_LOG.blocked" || -e "$DEMO_TEST_LOG.admitted" ]]; then
    echo 'FAIL: refresh admitted new deployment after its final idle check' >&2; exit 1
fi
grep -q 'database is locked' "$DEMO_TEST_LOG.writer"
[[ $(sqlite3 "$tmp/demo/durpdeploy.db" 'SELECT count(*) FROM deployments;') == 0 ]]
# Shutdown releases the write barrier before the replacement starts.
sqlite3 "$tmp/demo/durpdeploy.db" "$DEMO_TEST_ADMIT_SQL"
printf 'Refresh blocks concurrent admission and releases its barrier: PASS\n'
rm "$tmp/demo/durpdeploy.db"
printf 'retained\n' >"$tmp/demo/durpdeploy.db"
grep -Fxq docker=unix:///original/docker.sock "$tmp/demo/server.log"
grep -Fxq podman=unset "$tmp/demo/server.log"
grep -Fxq retained "$tmp/demo/durpdeploy.db"
[[ ! -e "$DEMO_TEST_LOG" ]]

"$root/scripts/demo.sh" stop "$tmp/demo" >"$tmp/output"
wait "$pid" 2>/dev/null || true; pid=
exec 3>&-
grep -Fxq retained "$tmp/demo/durpdeploy.db"
: >"$DEMO_TEST_LOG"

# Already-stopped servers are safe to stop again, retaining all state.
printf '%s\n' 999999999 >"$tmp/demo/server.pid"
"$root/scripts/demo.sh" stop "$tmp/demo" >"$tmp/output"
grep -Fxq 'container inspect durpdeploy-demo-test-https' "$DEMO_TEST_LOG"
grep -Fxq 'stop durpdeploy-demo-test-https' "$DEMO_TEST_LOG"
grep -Fxq 'container inspect durpdeploy-demo-test' "$DEMO_TEST_LOG"
grep -Fxq 'stop durpdeploy-demo-test' "$DEMO_TEST_LOG"
grep -Fxq retained "$tmp/demo/durpdeploy.db"
[[ $(wc -l <"$DEMO_TEST_LOG") == 5 ]]
"$root/scripts/demo.sh" --help >/dev/null
if "$root/scripts/demo.sh" stop >"$tmp/output" 2>&1; then
    echo 'FAIL: accepted a stop command without its directory' >&2; exit 1
fi

# The E2E bootstrap must honor a supplied CA before sending credentials.
cat >"$tmp/curl" <<'CURL'
#!/usr/bin/env bash
printf '%s\n' "$@" >"$DEMO_TEST_CURL_LOG"
exit 1
CURL
chmod 0755 "$tmp/curl"
export DEMO_TEST_CURL_LOG="$tmp/curl-arguments"
if PATH="$tmp:$PATH" DURPDEPLOY_BASE_URL=https://citadel.durp.loc:1 \
    DURPDEPLOY_E2E_CA_FILE="$tmp/demo certificate.pem" \
    bash "$root/scripts/e2e_db_test.sh" sqlite >"$tmp/output" 2>&1; then
    echo 'FAIL: E2E ignored a failed TLS health check' >&2; exit 1
fi
grep -Fxq -- --cacert "$DEMO_TEST_CURL_LOG"
grep -Fxq -- "$tmp/demo certificate.pem" "$DEMO_TEST_CURL_LOG"
if grep -Fxq -- -k "$DEMO_TEST_CURL_LOG"; then
    echo 'FAIL: E2E disabled TLS verification with a supplied CA' >&2; exit 1
fi
printf 'Demo lifecycle safety checks: PASS\n'
