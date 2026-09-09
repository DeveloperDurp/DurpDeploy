#!/usr/bin/env bash
set -euo pipefail

exec bash "$(dirname "$0")/check-ci-contract.sh" "$@"
