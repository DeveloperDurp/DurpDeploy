#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${SONAR_TOKEN:-}" ]]; then
    echo "ERROR: SONAR_TOKEN is required." >&2
    exit 2
fi

pull_request=${1:-}
if [[ -z "$pull_request" ]]; then
    echo "Usage: SONAR_TOKEN=... $0 <pull-request-number>" >&2
    exit 2
fi

SONAR_PULL_REQUEST="$pull_request" python3 -c '
import json
import os
import urllib.parse
import urllib.request

query = urllib.parse.urlencode({
    "componentKeys": "developerdurp_durpdeploy",
    "pullRequest": os.environ["SONAR_PULL_REQUEST"],
    "resolved": "false",
    "ps": "500",
})
request = urllib.request.Request(
    "https://sonarcloud.io/api/issues/search?" + query,
    headers={"Authorization": "Bearer " + os.environ["SONAR_TOKEN"]},
)
with urllib.request.urlopen(request) as response:
    payload = json.load(response)
issues = payload.get("issues", [])
if not issues:
    print("No unresolved SonarCloud findings.")
for issue in issues:
    component = issue.get("component", "")
    path = component.split(":", 1)[-1]
    line = issue.get("line")
    location = f"{path}:{line}" if line else path
    severity = issue.get("severity", "UNKNOWN")
    rule = issue.get("rule", "unknown-rule")
    message = issue.get("message", "")
    print(f"{severity} {rule} {location} - {message}")
'
