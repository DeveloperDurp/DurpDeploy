#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
workflow=${1:-$root/.github/workflows/security.yml}
require() {
  grep -Fq -- "$2" "$1" || { printf 'Security contract missing: %s\n' "$2" >&2; exit 1; }
}
for token in 'pull_request:' 'branches: [main]' 'schedule:' 'workflow_dispatch:' \
  'contents: read' 'persist-credentials: false' 'fetch-depth: 0' \
  'timeout-minutes: 30' 'retention-days: 7' 'make security-scan-test' \
  'bash scripts/security_scan.sh govulncheck' 'bash scripts/security_scan.sh gosec' \
  'bash scripts/security_scan.sh gitleaks'; do
  require "$workflow" "$token"
done
if grep -Eq 'secrets\.|pull_request_target|continue-on-error:|contents: write|permissions: write-all' "$workflow"; then
  printf 'Security contract: privileged or non-blocking workflow\n' >&2
  exit 1
fi
for token in 'golang.org/x/vuln/cmd/govulncheck@v1.8.0' \
  'github.com/securego/gosec/v2/cmd/gosec@v2.29.0' \
  'github.com/zricethezav/gitleaks/v8@v8.30.1'; do
  require "$root/Makefile" "$token"
done
for token in '-nosec=true' '-exclude-generated' '--redact=100' '--exit-code=10' \
  '-show traces,verbose,version'; do
  require "$root/scripts/security_scan.sh" "$token"
done
printf 'Security CI contract: PASS\n'
