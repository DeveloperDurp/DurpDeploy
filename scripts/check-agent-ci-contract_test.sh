#!/usr/bin/env bash
set -euo pipefail

bash scripts/check-agent-ci-contract.sh

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
cat > "$workdir/bad.yml" <<'YAML'
bad:
  image: docker:latest
  script:
    - trivy --severity HIGH,CRITICAL --exit-code 0
YAML

if output=$(bash scripts/check-agent-ci-contract.sh --workflow-dir "$workdir" 2>&1); then
	echo 'CI contract fixture unexpectedly passed' >&2
	exit 1
fi
grep -Fq 'mutable CI image reference' <<<"$output"
grep -Fq 'Trivy HIGH/CRITICAL scan does not fail' <<<"$output"
printf '%s\n' 'CI contract rejection: PASS'
