#!/usr/bin/env bash
# Use an explicitly selected E2E binary, or build from the local checkout.
set -euo pipefail
destination=${1:?usage: e2e_build_server.sh DESTINATION}
if [[ -n ${DURPDEPLOY_E2E_BINARY:-} ]]; then
  [[ -x $DURPDEPLOY_E2E_BINARY ]]
  cp "$DURPDEPLOY_E2E_BINARY" "$destination"
else
  bash "$(dirname "${BASH_SOURCE[0]}")/build_server.sh" -o "$destination"
fi
