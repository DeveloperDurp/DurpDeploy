#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source "$root/scripts/e2e_wait.sh"
tmp=$(mktemp -d)
server_pid=
cleanup() {
    if [[ -n $server_pid ]]; then
        kill "$server_pid" 2>/dev/null || true
        wait "$server_pid" 2>/dev/null || true
    fi
    rm -rf -- "$tmp"
}
trap cleanup EXIT
trap 'exit 143' INT TERM

# Publish the owned server's log only after the first parser attempt finishes.
python3 - "$tmp" <<'PY' &
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import json
import sys
import time

directory = Path(sys.argv[1])

class Health(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200 if self.path == "/healthz" else 404)
        self.end_headers()

    def log_message(self, *_):
        pass

with HTTPServer(("127.0.0.1", 0), Health) as server:
    port = server.server_port
    (directory / "origin").write_text(f"http://localhost:{port}")
    while not (directory / "parsed").exists():
        time.sleep(0.01)
    (directory / "server.log").write_text(json.dumps({
        "msg": "server starting", "addr": f"127.0.0.1:{port}"
    }) + "\n")
    server.serve_forever()
PY
server_pid=$!
for _ in {1..100}; do
    [[ -s $tmp/origin ]] && break
    kill -0 "$server_pid" 2>/dev/null || exit 1
    sleep 0.05
done
[[ -s $tmp/origin && ! -e $tmp/server.log ]]

# Retain the parser's real status. The old helper fails its first missing open.
python3() {
    local result=0
    command python3 "$@" || result=$?
    : >"$tmp/parsed"
    return "$result"
}
base=$(e2e_wait_for_server "$server_pid" "$tmp/server.log")
unset -f python3
[[ $base == "$(cat "$tmp/origin")" ]]
curl -fsS --max-time 2 "$base/healthz" >/dev/null
printf 'Delayed log creation: PASS\n'

# Existing logs still identify the owned server and require its health check.
[[ $(e2e_wait_for_server "$server_pid" "$tmp/server.log") == "$base" ]]
printf 'Existing log readiness: PASS\n'

printf '{invalid}\n' >"$tmp/invalid.log"
if e2e_wait_for_server "$server_pid" "$tmp/invalid.log" >"$tmp/invalid.out" 2>"$tmp/invalid.err"; then
    echo 'FAIL: malformed complete JSON was accepted' >&2
    exit 1
fi
grep -q 'JSONDecodeError' "$tmp/invalid.err"
printf 'Malformed log rejection: PASS\n'

if e2e_wait_for_server "$server_pid" "$tmp" >"$tmp/directory.out" 2>"$tmp/directory.err"; then
    echo 'FAIL: a log directory was accepted' >&2
    exit 1
fi
grep -q 'IsADirectoryError' "$tmp/directory.err"
printf 'Other log error rejection: PASS\n'

kill "$server_pid"
wait "$server_pid" || true
stopped_pid=$server_pid
server_pid=
for log in "$tmp/server.log" "$tmp/absent.log"; do
    if e2e_wait_for_server "$stopped_pid" "$log" >"$tmp/exited.out" 2>"$tmp/exited.err"; then
        echo 'FAIL: an exited owner was accepted' >&2
        exit 1
    fi
    grep -q 'owned E2E server exited before readiness' "$tmp/exited.err"
done
printf 'Exited owner rejection with existing and absent logs: PASS\n'
