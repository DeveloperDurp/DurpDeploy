#!/usr/bin/env bash
# Resolve a fixture image once per private runtime, without changing app pulls.
set -euo pipefail
image=${1:?usage: ensure_fixture_image.sh IMAGE ENGINE [ENGINE_OPTIONS...]}
shift
if ! "$@" image inspect "$image" >/dev/null 2>&1; then
  "$@" pull "$image" >/dev/null
fi
