#!/usr/bin/env bash
E2E_PORT_RESERVATION_PID=

e2e_release_port() {
    if [[ -n $E2E_PORT_RESERVATION_PID ]]; then
        kill "$E2E_PORT_RESERVATION_PID" 2>/dev/null || true
        wait "$E2E_PORT_RESERVATION_PID" 2>/dev/null || true
        E2E_PORT_RESERVATION_PID=
    fi
}

# Keep the chosen port bound until immediately before the owned server starts.
e2e_reserve_port() {
    local file
    file=$(mktemp "$1/port.XXXXXX")
    python3 - "$file" <<'PY' &
import os
from pathlib import Path
import socket
import sys
import time

with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    listener.listen()
    pending = Path(sys.argv[1] + ".pending")
    pending.write_text(str(listener.getsockname()[1]))
    os.replace(pending, sys.argv[1])
    while True:
        time.sleep(60)
PY
    E2E_PORT_RESERVATION_PID=$!
    for _ in {1..120}; do
        if [[ -s $file ]]; then
            E2E_RESERVED_PORT=$(cat "$file")
            return 0
        fi
        kill -0 "$E2E_PORT_RESERVATION_PID" 2>/dev/null || break
        sleep 0.05
    done
    echo 'FAIL: could not reserve an E2E port' >&2
    e2e_release_port
    return 1
}

# The caller owns the process and log; only that process's bound port is ready.
e2e_wait_for_server() {
    local pid=$1 log=$2 base
    for _ in {1..120}; do
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "FAIL: owned E2E server exited before readiness" >&2
            return 1
        fi
        base=$(python3 - "$log" <<'PY'
import json
import sys

with open(sys.argv[1]) as log:
    for line in log:
        if not line.startswith("{") or not line.endswith("\n"):
            continue
        record = json.loads(line)
        if record.get("msg") == "server starting":
            print("http://localhost:" + record["addr"].rsplit(":", 1)[1])
            break
PY
        ) || return 1
        if [[ -n $base ]] && curl -fsS --max-time 2 "$base/healthz" >/dev/null 2>&1; then
            kill -0 "$pid" 2>/dev/null || return 1
            printf '%s\n' "$base"
            return 0
        fi
        sleep 0.25
    done
    echo "FAIL: owned E2E server did not become ready" >&2
    return 1
}
