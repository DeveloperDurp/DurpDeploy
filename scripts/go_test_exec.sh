#!/usr/bin/env bash
set -euo pipefail

# Separate package binaries must not adopt each other's expiring Ryuk reaper.
# The outer runner owns this directory and sweeps its session on every exit.
package_dir=$(mktemp -d \
    "${DURPDEPLOY_GO_TEST_DIRECTORY:?}/durpdeploy-go-test.XXXXXXXX")
export DURPDEPLOY_GO_TEST_DIRECTORY=$package_dir
export TESTCONTAINERS_SESSION_ID=${package_dir##*/}
exec "$@"
