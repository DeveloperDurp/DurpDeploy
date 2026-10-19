#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workflow_dir=$repo_root/.gitlab/ci
if [[ ${1:-} == --workflow-dir ]]; then
  workflow_dir=${2:?missing workflow directory}
elif (($#)); then
  echo "usage: $0 [--workflow-dir DIR]" >&2
  exit 2
fi
failed=0
for workflow in "$workflow_dir"/*.yml; do
  if ! python3 - "$workflow" <<'PY'
import re
import sys
from pathlib import Path
path = Path(sys.argv[1])
text = path.read_text()
failed = False
if re.search(r"(?:image|name):\s*[^\s]+:latest(?:\s|$)", text):
    print(f"{path}: mutable CI image reference", file=sys.stderr)
    failed = True
if re.search(r"--severity\s+HIGH,CRITICAL[\s\S]{0,160}--exit-code\s+0|--exit-code\s+0[\s\S]{0,160}--severity\s+HIGH,CRITICAL", text):
    print(f"{path}: Trivy HIGH/CRITICAL scan does not fail", file=sys.stderr)
    failed = True
if re.search(r"allow_failure:\s*true", text):
    print(f"{path}: non-blocking CI gate", file=sys.stderr)
    failed = True
raise SystemExit(1 if failed else 0)
PY
  then failed=1; fi
done
[[ $failed == 0 ]]
grep -Fqx \
  'sonar.test.inclusions=**/*_test.go,internal/testdns/**,internal/testenv/**,scripts/*.mjs,scripts/run_named_go_tests_verify.go,scripts/go_test_cleanup.go' \
  "$repo_root/sonar-project.properties"
bash "$repo_root/scripts/check-security-contract.sh" "$workflow_dir/security.yml"
printf 'CI contract: PASS\n'
