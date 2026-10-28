#!/usr/bin/env bash
# Emit the report before the container's private tmpfs disappears.
set -euo pipefail
result=0
/opt/testssl/testssl.sh --quiet --warnings batch --nodns none \
    --protocols --cipher-per-proto --server-defaults \
    --add-ca /opt/fixture-ca.pem --jsonfile-pretty /tmp/result.json \
    "$@" >/tmp/testssl.log 2>&1 || result=$?
if [[ -s /tmp/result.json ]]; then cat /tmp/result.json; fi
if ((result != 0)); then cat /tmp/testssl.log >&2; fi
exit "$result"
