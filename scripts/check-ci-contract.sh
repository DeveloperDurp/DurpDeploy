#!/usr/bin/env bash
set -euo pipefail

workflow_dir=.github/workflows
if [ "${1:-}" = "--workflow-dir" ]; then
	workflow_dir=${2:?missing workflow directory}
elif [ "$#" -ne 0 ]; then
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
if re.search(r"uses:\s+[^\s]+@(?![0-9a-f]{40}(?:\s|$))[^\s]+", text):
    print(f"{path}: mutable GitHub Action reference", file=sys.stderr)
    failed = True
if re.search(r"--severity\s+HIGH,CRITICAL[\s\S]{0,160}--exit-code\s+0|--exit-code\s+0[\s\S]{0,160}--severity\s+HIGH,CRITICAL", text):
    print(f"{path}: Trivy HIGH/CRITICAL scan does not fail", file=sys.stderr)
    failed = True
raise SystemExit(1 if failed else 0)
PY
	then
		failed=1
	fi
done
[ "$failed" -eq 0 ] || exit 1
printf '%s\n' 'CI contract: PASS'
