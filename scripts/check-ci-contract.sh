#!/usr/bin/env bash
set -euo pipefail

for workflow in .github/workflows/*.yml; do
	python3 - "$workflow" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
text = path.read_text()
if re.search(r"uses:\s+[^\s]+@(?![0-9a-f]{40}(?:\s|$))[^\s]+", text):
    raise SystemExit(f"{path}: mutable GitHub Action reference")
if re.search(r"--severity\s+HIGH,CRITICAL[\s\S]{0,160}--exit-code\s+0|--exit-code\s+0[\s\S]{0,160}--severity\s+HIGH,CRITICAL", text):
    raise SystemExit(f"{path}: Trivy HIGH/CRITICAL scan does not fail")
PY
done
printf '%s\n' 'CI contract: PASS'
