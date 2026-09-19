#!/usr/bin/env bash
set -euo pipefail

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
cat > "$workdir/bad.yml" <<'YAML'
jobs:
  bad:
    steps:
      - uses: actions/checkout@v7
      - run: trivy --severity HIGH,CRITICAL --exit-code 0
YAML

if output=$(bash scripts/check-agent-ci-contract.sh --workflow-dir "$workdir" 2>&1); then
	echo 'CI contract fixture unexpectedly passed' >&2
	exit 1
fi
grep -Fq 'mutable GitHub Action reference' <<<"$output"
grep -Fq 'Trivy HIGH/CRITICAL scan does not fail' <<<"$output"
printf '%s\n' 'CI contract rejection: PASS'
