#!/usr/bin/env bash
# Acceptance test for P1-6 (backup/restore).
#
# Simulates a full disaster-recovery drill using Litestream against a local
# filesystem replica (no S3 bucket required):
#   1. Start a local server and seed it with 100 deployments via the API.
#   2. Start `litestream replicate` against a local replica directory.
#   3. Wait for replication to catch up, then kill the server and delete
#      the database (simulated disk failure).
#   4. `litestream restore` from the local replica.
#   5. Start the server again and verify all 100 deployments are present.
#
# Requires the `litestream` binary on PATH. See docs/backup-restore.md.
set -euo pipefail

if ! command -v litestream >/dev/null 2>&1; then
    echo "SKIP: litestream binary not found on PATH. Install it first:"
    echo "  see docs/backup-restore.md#option-1--litestream-continuous-replication"
    exit 0
fi

WORKDIR=$(mktemp -d)
REPLICA_DIR="$WORKDIR/replica"
DB_PATH="$WORKDIR/durpdeploy.db"
LITESTREAM_CONF="$WORKDIR/litestream.yml"
SERVER_LOG="$WORKDIR/server.log"
LITESTREAM_LOG="$WORKDIR/litestream.log"
BASE="http://localhost:8080"
COOKIES="$WORKDIR/cookies.txt"

cleanup() {
    kill "${SERVER_PID:-0}" 2>/dev/null || true
    kill "${LITESTREAM_PID:-0}" 2>/dev/null || true
    wait 2>/dev/null || true
    if [[ -n "${KEEP_WORKDIR:-}" ]]; then
        echo "workdir kept at $WORKDIR"
    else
        rm -rf "$WORKDIR"
    fi
}
trap cleanup EXIT

echo "=== Building durpdeploy ==="
go build -o "$WORKDIR/durpdeploy" ./cmd/server

export DURPDEPLOY_SECRET_KEY="${DURPDEPLOY_SECRET_KEY:-$(openssl rand -base64 32)}"

mkdir -p "$REPLICA_DIR"
cat >"$LITESTREAM_CONF" <<EOF
dbs:
  - path: $DB_PATH
    replicas:
      - type: file
        path: $REPLICA_DIR
        sync-interval: 100ms
EOF

echo "=== Seeding admin user ==="
ADMIN_EMAIL="backup-test@test.local"
ADMIN_PASS="backup-test-password-1234"
DURPDEPLOY_DB="$DB_PATH" "$WORKDIR/durpdeploy" admin create \
    --email "$ADMIN_EMAIL" --password "$ADMIN_PASS" >/dev/null

echo "=== Starting server on :8080 ==="
DURPDEPLOY_DB="$DB_PATH" \
    "$WORKDIR/durpdeploy" >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 30); do
    curl -s -o /dev/null "$BASE/" && break
    sleep 0.5
done

echo "=== Starting litestream replicate ==="
litestream replicate -config "$LITESTREAM_CONF" >"$LITESTREAM_LOG" 2>&1 &
LITESTREAM_PID=$!
sleep 1

echo "=== Logging in ==="
CODE=$(curl -s -c "$COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "email=$ADMIN_EMAIL&password=$ADMIN_PASS" "$BASE/login")
[[ "$CODE" == "303" ]] || { echo "FAIL: login got $CODE, want 303"; exit 1; }
SESSION_ID=$(awk '$6 == "session" { print $7 }' "$COOKIES")
CSRF=$(sqlite3 "$DB_PATH" "SELECT csrf_token FROM sessions WHERE id='$SESSION_ID';")
[[ -n "$CSRF" ]] || { echo "FAIL: no CSRF token found"; exit 1; }

echo "=== Seeding 100 deployments ==="
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "name=BackupTestProject&csrf_token=$CSRF" "$BASE/projects")
[[ "$CODE" == "303" ]] || { echo "FAIL: create project got $CODE"; exit 1; }
PROJECT_ID=$(curl -s -b "$COOKIES" "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | head -1)

CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "name=BackupTestEnv&csrf_token=$CSRF" "$BASE/environments")
[[ "$CODE" == "303" ]] || { echo "FAIL: create env got $CODE"; exit 1; }
ENV_ID=$(curl -s -b "$COOKIES" "$BASE/environments" | grep -oP 'href="/environments/\K[0-9]+' | head -1)

curl -s -b "$COOKIES" -o /dev/null -X POST \
    -d "name=Step1&script_body=echo+hello&csrf_token=$CSRF" \
    "$BASE/projects/$PROJECT_ID/steps"

CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "version=1.0.0&release_notes=backup-drill&csrf_token=$CSRF" \
    "$BASE/projects/$PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create release got $CODE"; exit 1; }
RELEASE_ID=$(curl -s -b "$COOKIES" "$BASE/projects/$PROJECT_ID/releases" \
    | grep -oP 'href="/projects/'"$PROJECT_ID"'/releases/\K[0-9]+' | sort -n | tail -1)

for i in $(seq 1 100); do
    # ponytail: the runner writes deployment rows from a background goroutine
    # concurrently with this request's own insert, which can trip SQLite's
    # busy_timeout under rapid-fire sequential POSTs. Retry a couple of times
    # instead of slowing every request down with a fixed sleep.
    for attempt in 1 2 3; do
        CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" -X POST \
            -d "release_id=$RELEASE_ID&environment_id=$ENV_ID&note=drill-$i&csrf_token=$CSRF" \
            "$BASE/projects/$PROJECT_ID/deploy")
        [[ "$CODE" == "303" ]] && break
        sleep 0.1
    done
    [[ "$CODE" == "303" ]] || echo "  deploy #$i got $CODE (after retries)"
done

DEPLOY_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM deployments;")
[[ "$DEPLOY_COUNT" == "100" ]] || { echo "FAIL: expected 100 deployments before backup, got $DEPLOY_COUNT"; exit 1; }
echo "  Seeded 100 deployments: OK"

echo "=== Waiting for litestream to catch up ==="
sleep 2
litestream ltx -config "$LITESTREAM_CONF" "$DB_PATH" || {
    echo "FAIL: litestream ltx reported no replica data"
    cat "$LITESTREAM_LOG"
    exit 1
}

echo "=== Simulating disk failure ==="
kill "$SERVER_PID" 2>/dev/null || true
wait "$SERVER_PID" 2>/dev/null || true
sleep 1
kill "$LITESTREAM_PID" 2>/dev/null || true
wait "$LITESTREAM_PID" 2>/dev/null || true
rm -f "$DB_PATH" "$DB_PATH-shm" "$DB_PATH-wal"
[[ ! -f "$DB_PATH" ]] || { echo "FAIL: db still exists after simulated failure"; exit 1; }
echo "  Database deleted: OK"

echo "=== Restoring from replica ==="
litestream restore -config "$LITESTREAM_CONF" -o "$DB_PATH" "$DB_PATH"
[[ -f "$DB_PATH" ]] || { echo "FAIL: restore did not produce a db file"; exit 1; }

echo "=== Restarting server and verifying data ==="
DURPDEPLOY_DB="$DB_PATH" \
    "$WORKDIR/durpdeploy" >>"$SERVER_LOG" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 30); do
    curl -s -o /dev/null "$BASE/" && break
    sleep 0.5
done

RESTORED_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM deployments;")
[[ "$RESTORED_COUNT" == "100" ]] || {
    echo "FAIL: expected 100 deployments after restore, got $RESTORED_COUNT"
    exit 1
}
echo "  Restored 100 deployments: OK"

echo "=== PASS: backup/restore drill succeeded ==="
