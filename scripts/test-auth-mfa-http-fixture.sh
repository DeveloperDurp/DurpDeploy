#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$SCRIPT_DIR/auth_mfa_http_fixture.sh"

TMP=$(mktemp -d)
PORT_FILE="$TMP/port"
SERVER_LOG="$TMP/server.log"
ARTIFACT_ROOT="$TMP/artifacts"
SERVER_PID=""
PORT=""

PASSWORD_SECRET='self-test-password-secret'
SESSION_SECRET='self-test-session-secret'
CSRF_SECRET='self-test-csrf-secret'
CHALLENGE_SECRET='self-test-challenge-secret'
RECOVERY_SECRET='self-test-recovery-secret'
TOTP_SEED_SECRET='JBSWY3DPEHPK3PXP'
WEBAUTHN_BLOB_SECRET='self-test-webauthn-blob-secret'
BEARER_TOKEN='ddp_pat_self_test_bearer_secret'

fail() {
    echo "auth/MFA HTTP fixture self-test: $1" >&2
    exit 1
}

for command in curl python3 sqlite3 ss; do
    command -v "$command" >/dev/null 2>&1 ||
        fail "required command is unavailable: $command"
done

stop_server() {
    if [[ -n "$SERVER_PID" ]]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
        SERVER_PID=""
    fi
}

cleanup() {
    stop_server
    rm -rf "$TMP"
}
trap cleanup EXIT

cat > "$TMP/server.py" <<'PY'
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import sys

port_file = Path(sys.argv[1])

class Handler(BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        pass

    def respond(self, status=200, body=b"ok", extra_headers=()):
        self.send_response(status)
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Set-Cookie", "session=self-test-session-secret; Path=/; HttpOnly")
        for name, value in extra_headers:
            self.send_header(name, value)
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/healthz":
            self.respond(body=b"healthy")
            return
        if self.path == "/leak":
            self.respond(body=(
                b'password=self-test-password-secret '
                b'challenge_token=self-test-challenge-secret '
                b'recovery_code=self-test-recovery-secret '
                b'seed=JBSWY3DPEHPK3PXP '
                b'webauthn_blob=self-test-webauthn-blob-secret '
                b'Authorization: Bearer ddp_pat_self_test_bearer_secret'
            ), extra_headers=(("Set-Cookie", "pending_mfa=self-test-challenge-secret; Path=/; HttpOnly"),))
            return
        self.respond(body=b'<meta name="csrf-token" content="self-test-csrf-secret">fixture page')

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        if self.path == "/settings/tokens":
            self.respond(
                status=303,
                body=b"token created",
                extra_headers=(("Location", "/settings/tokens?new_token=ddp_pat_self_test_bearer_secret"),),
            )
            return
        if self.path == "/api/v1/admin/users":
            self.respond(status=201, body=b'{"id":7}')
            return
        if self.path == "/api/v1/echo":
            authorized = self.headers.get("Authorization") == "Bearer ddp_pat_self_test_bearer_secret"
            self.respond(
                status=201 if authorized else 401,
                body=b"api request accepted " + body,
                extra_headers=(("X-Authorized", "yes" if authorized else "no"),),
            )
            return
        self.respond(status=200, body=body)

server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
port_file.write_text(str(server.server_address[1]), encoding="utf-8")
server.serve_forever()
PY

python3 "$TMP/server.py" "$PORT_FILE" >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!
SERVER_READY=0
for _ in $(seq 1 50); do
    if [[ -s "$PORT_FILE" ]]; then
        PORT=$(<"$PORT_FILE")
        if curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null; then
            SERVER_READY=1
            break
        fi
    fi
    sleep 0.1
done
[[ "$SERVER_READY" == 1 ]] || fail "throwaway server did not become ready"

db_query() {
    sqlite3 "$TMP/state.db" "$1"
}

sqlite3 "$TMP/state.db" <<'SQL'
CREATE TABLE audit_log (action TEXT NOT NULL, user_id INTEGER);
CREATE TABLE users (id INTEGER PRIMARY KEY);
INSERT INTO audit_log (action, user_id) VALUES ('login', 7);
INSERT INTO users (id) VALUES (7);
SQL

AUTH_MFA_ARTIFACT_DIR="$ARTIFACT_ROOT"
auth_mfa_fixture_init self-test-success "http://127.0.0.1:$PORT" db_query "$SERVER_LOG"
SUCCESS_TMP=$AUTH_MFA_FIXTURE_TMP
COOKIE_JAR=$(auth_mfa_cookie_jar admin)
auth_mfa_http_request "$COOKIE_JAR" GET /
auth_mfa_assert_status 200
auth_mfa_assert_header_contains Cache-Control no-store
auth_mfa_assert_body_contains "fixture page marker" "fixture page"
auth_mfa_assert_cookie "$COOKIE_JAR" session
CSRF=$(auth_mfa_csrf_from_cookies "$COOKIE_JAR")
[[ "$CSRF" == "$CSRF_SECRET" ]] || fail "CSRF extraction failed"
TOKEN=$(auth_mfa_mint_api_token "$COOKIE_JAR" "$CSRF" self-test)
[[ "$TOKEN" == "$BEARER_TOKEN" ]] || fail "API token extraction failed"
auth_mfa_api_request "$TOKEN" POST /api/v1/echo "{\"password\":\"$PASSWORD_SECRET\"}"
auth_mfa_assert_status 201
auth_mfa_assert_header_contains X-Authorized yes
auth_mfa_assert_body_contains "API marker" "api request accepted"
auth_mfa_create_user "$TOKEN" viewer viewer viewer@self.test "$PASSWORD_SECRET"
[[ "$(auth_mfa_user_id viewer)" == 7 ]] || fail "user helper did not retain ID"
[[ "$(auth_mfa_user_email viewer)" == viewer@self.test ]] || fail "user helper did not retain email"
[[ "$(auth_mfa_user_password viewer)" == "$PASSWORD_SECRET" ]] || fail "user helper did not retain password"
auth_mfa_assert_db_count "seed user" 1 "SELECT COUNT(*) FROM users"
auth_mfa_assert_audit_count login 1 7
auth_mfa_fixture_cleanup
[[ ! -e "$SUCCESS_TMP" ]] || fail "successful fixture left its temporary directory"
[[ ! -e "$ARTIFACT_ROOT/self-test-success" ]] || fail "successful fixture wrote an artifact"

run_forced_failure() {
    local scenario=$1
    local assertion=$2

    (
        AUTH_MFA_ARTIFACT_DIR="$ARTIFACT_ROOT"
        auth_mfa_fixture_init "$scenario" "http://127.0.0.1:$PORT" db_query "$SERVER_LOG"
        printf '%s\n' "$AUTH_MFA_FIXTURE_TMP" > "$TMP/$assertion.fixture-tmp"
        trap auth_mfa_fixture_cleanup EXIT
        local jar
        jar=$(auth_mfa_cookie_jar failure)
        auth_mfa_secret_add "$PASSWORD_SECRET"
        auth_mfa_secret_add "$SESSION_SECRET"
        auth_mfa_secret_add "$CSRF_SECRET"
        auth_mfa_secret_add "$CHALLENGE_SECRET"
        auth_mfa_secret_add "$RECOVERY_SECRET"
        auth_mfa_secret_add "$TOTP_SEED_SECRET"
        auth_mfa_secret_add "$WEBAUTHN_BLOB_SECRET"
        auth_mfa_secret_add "$BEARER_TOKEN"
        auth_mfa_http_request "$jar" GET /leak
        case "$assertion" in
        status) auth_mfa_assert_status 418 || exit 1 ;;
        header) auth_mfa_assert_header X-Missing || exit 1 ;;
        body) auth_mfa_assert_body_contains "missing marker" "not present" || exit 1 ;;
        cookie) auth_mfa_assert_cookie "$jar" absent || exit 1 ;;
        db) auth_mfa_assert_db_count "missing state" 2 "SELECT COUNT(*) FROM users" || exit 1 ;;
        audit) auth_mfa_assert_audit_count login 2 7 || exit 1 ;;
        *) fail "unknown forced assertion $assertion" ;;
        esac
    )
}

for assertion in status header body cookie db audit; do
    scenario="self-test-failure-$assertion"
    if run_forced_failure "$scenario" "$assertion" >"$TMP/$assertion.out" 2>"$TMP/$assertion.err"; then
        fail "$assertion assertion unexpectedly passed"
    fi
    artifact="$ARTIFACT_ROOT/$scenario"
    [[ -f "$artifact/summary.txt" ]] || fail "$assertion failure wrote no summary"
    grep -Fq 'command=curl -X GET /leak' "$artifact/summary.txt" ||
        fail "$assertion failure omitted the safe command summary"
    failure_tmp=$(<"$TMP/$assertion.fixture-tmp")
    [[ ! -e "$failure_tmp" ]] || fail "$assertion failure left its temporary directory"
    grep -Fqx 'cleanup=complete' "$artifact/summary.txt" ||
        fail "$assertion failure did not record fixture cleanup"
    for secret in \
        "$PASSWORD_SECRET" "$SESSION_SECRET" "$CSRF_SECRET" "$CHALLENGE_SECRET" \
        "$RECOVERY_SECRET" "$TOTP_SEED_SECRET" "$WEBAUTHN_BLOB_SECRET" "$BEARER_TOKEN"; do
        if grep -RFq -- "$secret" "$artifact" "$TMP/$assertion.out" "$TMP/$assertion.err"; then
            fail "$assertion failure leaked a secret"
        fi
    done
done

stopped_pid=$SERVER_PID
stop_server
if ps -p "$stopped_pid" >/dev/null 2>&1; then
    fail "throwaway server still appears in ps"
fi
if ss -ltn "sport = :$PORT" | grep -q LISTEN; then
    fail "throwaway server still appears in ss"
fi
[[ ! -e "$SUCCESS_TMP" ]] || fail "fixture temporary path survived cleanup"

echo "auth/MFA HTTP fixture self-test: OK"
