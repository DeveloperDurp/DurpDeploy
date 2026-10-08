#!/usr/bin/env bash
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
