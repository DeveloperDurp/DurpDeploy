#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workflow=${1:-$root/.gitlab/ci/security.yml}
require() {
  grep -Fq -- "$2" "$1" || { printf 'Security contract missing: %s\n' "$2" >&2; exit 1; }
}
for token in 'security-scan:' 'container-security:' 'timeout: 30 minutes' \
  'when: always' 'expire_in: 7 days' 'make security-scan-test' \
  'for scanner in govulncheck gosec gitleaks' 'exit "$result"' \
  'artifacts/security/' 'artifacts/container/'; do
  require "$workflow" "$token"
done
if grep -Eq 'GHCR_TOKEN|GITLAB_READ_TOKEN|SONAR_TOKEN|docker login|allow_failure: *true|environment:' "$workflow"; then
  echo 'Security contract: privileged or non-blocking pipeline' >&2
  exit 1
fi
require "$root/Makefile" 'bash scripts/install_security_tools.sh'
for token in 'govulncheck golang.org/x/vuln v1.8.0 /cmd/govulncheck' \
  'gosec github.com/securego/gosec/v2 v2.29.0 /cmd/gosec' \
  "gitleaks github.com/zricethezav/gitleaks/v8 v8.30.1 ''"; do
  require "$root/scripts/install_security_tools.sh" "$token"
done
for token in '-nosec=true' '-exclude-generated' '--redact=100' '--exit-code=10' \
  '-show traces,verbose,version'; do
  require "$root/scripts/security_scan.sh" "$token"
done
printf 'Security CI contract: PASS\n'
