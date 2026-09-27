#!/usr/bin/env bash
set -euo pipefail

ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)

export DURPDEPLOY_AGENT_E2E_HOST_PROCESS=${DURPDEPLOY_AGENT_E2E_HOST_PROCESS:-1}

bash "$ROOT/scripts/agent_e2e_test.sh"
exec bash "$ROOT/scripts/agent_e2e_test.sh" --mixed-interpreter
